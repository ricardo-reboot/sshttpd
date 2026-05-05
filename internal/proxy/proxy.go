package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bugscave/sshttpd/internal/config"
)

// Defaults applied when ProxyCacheConfig leaves a knob at its zero value.
const (
	DefaultMaxRedirects     = 10
	DefaultMaxResponseBytes = 64 * 1024 * 1024 // 64 MiB
)

// Cache is a caching proxy for external resources.
type Cache struct {
	cfg     config.ProxyCacheConfig
	client  *http.Client
	entries map[string]*cacheEntry
	mu      sync.RWMutex
}

// Response is a captured HTTP response from upstream — the bits we need to
// reconstruct the full HTTP/1.1 wire format on the SSH channel so the browser
// sees the same status code, headers (Content-Type, Cache-Control, ETag, etc.)
// and body the upstream origin sent.
type Response struct {
	StatusCode int
	Status     string // e.g. "200 OK"
	Header     http.Header
	Body       []byte
}

type cacheEntry struct {
	resp      *Response
	fetchedAt time.Time
	ttl       time.Duration
}

func (e *cacheEntry) expired() bool {
	return time.Since(e.fetchedAt) > e.ttl
}

// NewCache creates a proxy cache with the given configuration. The HTTP
// client is pre-wired with SSRF defenses: a dialer that rejects connections
// to private/loopback/link-local/cloud-metadata IPs (unless the operator
// opted in via allow-private-ips) and a redirect policy that re-checks the
// allowlist on every hop and caps the chain length.
func NewCache(cfg config.ProxyCacheConfig) *Cache {
	c := &Cache{
		cfg:     cfg,
		entries: make(map[string]*cacheEntry),
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           c.guardedDialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	c.client = &http.Client{
		Transport:     transport,
		Timeout:       30 * time.Second,
		CheckRedirect: c.checkRedirect,
	}
	return c
}

// guardedDialContext is the SSRF gate: every TCP dial passes through here.
// Standard net.Dialer resolves the host via DNS and tries each candidate IP;
// we wrap that with a Control func that runs after resolution but before the
// connect, vetoing any address pointing inside the operator's network.
func (c *Cache) guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("split host:port %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("expected resolved IP, got %q", host)
			}
			if !c.cfg.AllowPrivateIPs && isBlockedIP(ip) {
				return fmt.Errorf("blocked: %s resolves to non-public address %s", addr, ip)
			}
			return nil
		},
	}
	return d.DialContext(ctx, network, addr)
}

// checkRedirect re-runs allowlist + scheme + IP validation for every redirect
// hop. http.Client invokes this *before* dialing the new URL.
func (c *Cache) checkRedirect(req *http.Request, via []*http.Request) error {
	if c.cfg.AllowRedirects != nil && !*c.cfg.AllowRedirects {
		return http.ErrUseLastResponse
	}
	limit := c.cfg.MaxRedirects
	if limit <= 0 {
		limit = DefaultMaxRedirects
	}
	if len(via) >= limit {
		return fmt.Errorf("redirect chain exceeded %d hops", limit)
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("redirect to disallowed scheme: %s", req.URL.Scheme)
	}
	if req.URL.User != nil {
		return fmt.Errorf("redirect with userinfo rejected")
	}
	if !c.isAllowed(req.URL.Host) {
		return fmt.Errorf("redirect host not in allowlist: %s", req.URL.Host)
	}
	return nil
}

// Fetch retrieves a resource as a body-only byte slice, using cache if available.
// Retained for callers that don't need upstream metadata (text-mode shell
// sessions, simple tests). For SSH-Web binary responses use FetchResponse.
func (c *Cache) Fetch(rawURL string) ([]byte, error) {
	resp, err := c.FetchResponse(rawURL)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// FetchResponse retrieves a resource and returns the upstream HTTP response
// (status code, headers, body). The result is cached per URL and re-served
// from cache until TTL expiry. Allowlist enforcement happens before any
// network activity, preserving the privacy guarantee.
func (c *Cache) FetchResponse(rawURL string) (*Response, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("disallowed URL scheme: %s", parsed.Scheme)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("URL with userinfo rejected")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("URL missing host")
	}

	if !c.isAllowed(parsed.Host) {
		return nil, fmt.Errorf("origin not in allowlist: %s", parsed.Host)
	}

	// Reject IP-literal hosts that point inside the operator's network even
	// before DNS — the dialer also enforces this, but rejecting up front gives
	// a clearer error and avoids opening a socket for a doomed request.
	if !c.cfg.AllowPrivateIPs {
		if ip := net.ParseIP(parsed.Hostname()); ip != nil && isBlockedIP(ip) {
			return nil, fmt.Errorf("blocked: URL resolves to non-public address %s", ip)
		}
	}

	c.mu.RLock()
	entry, ok := c.entries[rawURL]
	c.mu.RUnlock()
	if ok && !entry.expired() {
		return entry.resp, nil
	}

	httpResp, err := c.client.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", rawURL, err)
	}
	defer httpResp.Body.Close()

	// We deliberately do NOT short-circuit on non-2xx here — the client wants
	// to know about 304s, 4xx, etc. with their original status + body.
	limit := c.maxResponseBytes()
	body, err := io.ReadAll(io.LimitReader(httpResp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("upstream response exceeded max-size (%d bytes)", limit)
	}

	resp := &Response{
		StatusCode: httpResp.StatusCode,
		Status:     httpResp.Status,
		Header:     httpResp.Header.Clone(),
		Body:       body,
	}

	// Drop hop-by-hop headers that have no meaning to the browser end
	// (these are tied to the upstream TCP connection, not the resource).
	for _, h := range hopByHopHeaders {
		resp.Header.Del(h)
	}

	c.mu.Lock()
	c.entries[rawURL] = &cacheEntry{
		resp:      resp,
		fetchedAt: time.Now(),
		ttl:       c.parseTTL(),
	}
	c.mu.Unlock()

	return resp, nil
}

// hopByHopHeaders are connection-level headers per RFC 7230 §6.1; they
// describe the upstream HTTP connection and must not be forwarded.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

func (c *Cache) isAllowed(host string) bool {
	for _, allowed := range c.cfg.AllowedOrigins {
		if allowed == "*" && !c.cfg.DenyAll {
			return true
		}
		if allowed == host {
			return true
		}
		if strings.HasPrefix(allowed, "*.") && strings.HasSuffix(host, allowed[1:]) {
			return true
		}
	}
	return false
}

func (c *Cache) parseTTL() time.Duration {
	// TODO: Parse TTL string from config (e.g., "24h", "1d")
	return 24 * time.Hour
}

// maxResponseBytes returns the byte cap for a single upstream body. If
// max-size is set in the config it wins; otherwise the default applies.
func (c *Cache) maxResponseBytes() int64 {
	if n, ok := parseSize(c.cfg.MaxSize); ok && n > 0 {
		return n
	}
	return DefaultMaxResponseBytes
}

// parseSize accepts strings like "10MB", "512K", "1G", "64MiB" (case
// insensitive). Bare numbers are interpreted as bytes. Returns (0, false) on
// parse failure so the caller falls back to the default. The suffix table is
// searched longest-first so "2mb" matches "mb" not "m".
func parseSize(s string) (int64, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, false
	}
	type sizeUnit struct {
		suffix string
		mult   int64
	}
	units := []sizeUnit{
		{"gib", 1 << 30},
		{"mib", 1 << 20},
		{"kib", 1 << 10},
		{"gb", 1000 * 1000 * 1000},
		{"mb", 1000 * 1000},
		{"kb", 1000},
		{"g", 1 << 30},
		{"m", 1 << 20},
		{"k", 1 << 10},
		{"b", 1},
	}
	mult := int64(1)
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			mult = u.mult
			s = strings.TrimSpace(s[:len(s)-len(u.suffix)])
			break
		}
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int64(r-'0')
	}
	return n * mult, n > 0
}

// blockedNets enumerates the address ranges proxy-call must never reach.
// Sources: RFC 1918, RFC 6598, RFC 3927, RFC 4193, link-local IPv6, IPv4
// loopback, IPv6 loopback, IPv4-mapped IPv6, and the AWS/GCP/Azure metadata
// endpoints (which all sit on link-local 169.254.0.0/16 anyway).
var blockedNets = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",       // "this network"
		"10.0.0.0/8",      // RFC 1918
		"100.64.0.0/10",   // RFC 6598 CGNAT
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local + cloud metadata
		"172.16.0.0/12",   // RFC 1918
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"192.168.0.0/16",  // RFC 1918
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved
		"255.255.255.255/32",
		"::/128",        // IPv6 unspecified
		"::1/128",       // IPv6 loopback
		// IPv4-mapped IPv6 (::ffff:0:0/96) is intentionally NOT a blocked net —
		// To4() in isBlockedIP unwraps these to their v4 form before matching,
		// so blocking the entire v4-mapped range here would falsely flag every
		// IPv4 address (Go's IPNet.Contains treats v4-mapped as a v6 prefix).
		"64:ff9b::/96", // NAT64 well-known
		"100::/64",      // IPv6 discard prefix
		"fc00::/7",      // ULA
		"fe80::/10",     // link-local IPv6
		"ff00::/8",      // multicast IPv6
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

// isBlockedIP returns true if ip is in any reserved/private range that
// proxy-call must not reach. IPv4-mapped IPv6 addresses are unwrapped first
// so 127.0.0.1 expressed as ::ffff:127.0.0.1 is still blocked.
func isBlockedIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
