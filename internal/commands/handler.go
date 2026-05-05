package commands

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"net/http"

	"github.com/bugscave/sshttpd/internal/auth"
	"github.com/bugscave/sshttpd/internal/backend"
	"github.com/bugscave/sshttpd/internal/config"
	"github.com/bugscave/sshttpd/internal/packfile"
	"github.com/bugscave/sshttpd/internal/proxy"
)

// Handler dispatches SSH-Web commands to their implementations for a single site.
type Handler struct {
	cfg     *config.Config
	siteIdx int
	backend *backend.Backend
	proxy   *proxy.Cache
}

// NewHandler creates a command handler bound to cfg.Sites[0]. Equivalent to
// NewHandlerForSite(cfg, 0). Retained for backwards compatibility with single-site callers.
func NewHandler(cfg *config.Config) (*Handler, error) {
	return NewHandlerForSite(cfg, 0)
}

// NewHandlerForSite creates a command handler bound to a specific site index.
// When the site has a `backend` configured, an upstream proxy is set up for
// api-call and mcp forwarding. proxy-call uses the per-site allowlisted
// proxy.Cache when configured.
func NewHandlerForSite(cfg *config.Config, siteIdx int) (*Handler, error) {
	if siteIdx < 0 || siteIdx >= len(cfg.Sites) {
		return nil, fmt.Errorf("site index %d out of range", siteIdx)
	}
	site := cfg.Sites[siteIdx]

	b, err := backend.New(site.Backend)
	if err != nil {
		return nil, fmt.Errorf("setting up backend: %w", err)
	}

	var pc *proxy.Cache
	if len(site.ProxyCache.AllowedOrigins) > 0 {
		pc = proxy.NewCache(site.ProxyCache)
	}

	return &Handler{
		cfg:     cfg,
		siteIdx: siteIdx,
		backend: b,
		proxy:   pc,
	}, nil
}

// site returns the SiteConfig this handler serves.
func (h *Handler) site() config.SiteConfig {
	return h.cfg.Sites[h.siteIdx]
}

// Execute runs a command and returns the response string (text-mode for interactive sessions).
func (h *Handler) Execute(cmd string, args []string, tier string) (string, error) {
	site := h.site()

	// Authorization: capabilities is always allowed; other commands must be in the
	// tier's allowed list (tier inheritance: identified gets anonymous + identified;
	// trusted gets everything).
	if cmd != "capabilities" {
		fullCommand := joinCommand(cmd, args)
		if !auth.IsCommandAllowed(tier, fullCommand, &site.Auth) {
			return "", fmt.Errorf("permission denied: %q not allowed for tier %q", fullCommand, tier)
		}
	}

	switch cmd {
	case "capabilities":
		return h.capabilities()
	case "receive-pack":
		return h.receivePack(args)
	case "api-call":
		return h.apiCall(args)
	case "proxy-call":
		return h.proxyCall(args)
	case "rss-feed":
		return h.rssFeed(args)
	case "sitemap":
		return h.sitemap()
	case "robots":
		return h.robots()
	case "mcp":
		return h.mcp(args)
	default:
		return "", fmt.Errorf("unknown command: %s", cmd)
	}
}

func joinCommand(cmd string, args []string) string {
	if len(args) == 0 {
		return cmd
	}
	return cmd + " " + strings.Join(args, " ")
}

// ExecuteBinary runs a command and writes binary output (for exec-mode SSH channels).
// Used for receive-pack so the client gets real packfile bytes, not the text summary.
// Falls back to writing the text response when no binary form is defined.
func (h *Handler) ExecuteBinary(cmd string, args []string, tier string, w io.Writer) error {
	site := h.site()

	if cmd != "capabilities" {
		fullCommand := joinCommand(cmd, args)
		if !auth.IsCommandAllowed(tier, fullCommand, &site.Auth) {
			return fmt.Errorf("permission denied: %q not allowed for tier %q", fullCommand, tier)
		}
	}

	switch cmd {
	case "receive-pack":
		return h.receivePackBinary(args, w)
	case "proxy-call":
		return h.proxyCallBinary(args, w)
	default:
		// Fall back to text mode for commands without a binary form.
		resp, err := h.Execute(cmd, args, tier)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, resp)
		return err
	}
}

func (h *Handler) capabilities() (string, error) {
	site := h.site()

	manifest := CapabilitiesManifest{
		Protocol: "ssh-web/0.1",
		Site: SiteInfo{
			Name: site.Host,
			Host: site.Host,
		},
		Commands: make(map[string]interface{}),
		Auth: AuthInfo{
			Modes:    []string{"anonymous", "identified", "trusted"},
			KeyTypes: []string{"ed25519", "ecdsa-sha2-nistp256"},
		},
	}

	// receive-pack: collect all configured routes.
	receivePackRoutes := []string{}
	apiCallRoutes := map[string]map[string]interface{}{}
	for _, cmd := range site.Commands {
		switch cmd.Type {
		case "receive-pack":
			receivePackRoutes = append(receivePackRoutes, cmd.Route)
		case "api-call":
			key := cmd.Method + " " + cmd.Route
			tier := tierForCommand(joinCommand("api-call", []string{cmd.Method, cmd.Route}), &site.Auth)
			apiCallRoutes[key] = map[string]interface{}{
				"method": cmd.Method,
				"route":  cmd.Route,
				"auth":   tier,
			}
		}
	}
	if len(receivePackRoutes) > 0 {
		manifest.Commands["receive-pack"] = map[string]interface{}{
			"description": "Fetch site content as packfile or HTTP/1.1 wire response",
			"routes":      receivePackRoutes,
			"supports":    []string{"delta", "incremental", "backend-fallback"},
		}
	}
	if len(apiCallRoutes) > 0 {
		manifest.Commands["api-call"] = map[string]interface{}{
			"description": "Dynamic API endpoints",
			"routes":      apiCallRoutes,
		}
	}

	// proxy-call: surface allowlist if configured.
	if len(site.ProxyCache.AllowedOrigins) > 0 {
		manifest.Commands["proxy-call"] = map[string]interface{}{
			"description":      "Fetch external resources through server proxy",
			"allowed-origins":  site.ProxyCache.AllowedOrigins,
			"supports":         []string{"caching"},
		}
	}

	// Discovery metadata.
	if len(site.Meta.Feeds) > 0 {
		feeds := map[string]interface{}{}
		for _, f := range site.Meta.Feeds {
			feeds[f.Path] = map[string]interface{}{
				"format": f.Format,
				"path":   f.Path,
			}
		}
		manifest.Feeds = feeds
	}
	if site.Meta.Sitemap.Path != "" {
		manifest.Sitemap = &SitemapInfo{
			Dynamic: site.Meta.Sitemap.Dynamic,
			Path:    site.Meta.Sitemap.Path,
		}
	}
	if site.Meta.Robots.CrawlDelay != 0 || len(site.Meta.Robots.AllowedPaths) > 0 || len(site.Meta.Robots.BlockedPaths) > 0 {
		manifest.Robots = &RobotsInfo{
			CrawlDelay:    site.Meta.Robots.CrawlDelay,
			AllowedPaths:  site.Meta.Robots.AllowedPaths,
			BlockedPaths:  site.Meta.Robots.BlockedPaths,
		}
	}

	// MCP tools.
	if len(site.MCP) > 0 {
		tools := []map[string]interface{}{}
		for _, tool := range site.MCP {
			t := map[string]interface{}{
				"name": tool.Name,
				"auth": tool.Auth,
			}
			if tool.Description != "" {
				t["description"] = tool.Description
			}
			if len(tool.Params) > 0 {
				params := map[string]interface{}{}
				for _, p := range tool.Params {
					params[p.Name] = map[string]interface{}{
						"type":     p.Type,
						"required": p.Required,
					}
				}
				t["params"] = params
			}
			tools = append(tools, t)
		}
		manifest.MCP = &MCPInfo{
			Version: "1.0",
			Tools:   tools,
		}
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling capabilities: %w", err)
	}
	return string(data), nil
}

// tierForCommand reports the lowest tier that has the command in its allowed list.
// Used to annotate the capabilities manifest so clients know what auth a route requires.
func tierForCommand(command string, authCfg *config.AuthConfig) string {
	if auth.IsCommandAllowed(auth.TierAnonymous, command, authCfg) {
		return auth.TierAnonymous
	}
	if auth.IsCommandAllowed(auth.TierIdentified, command, authCfg) {
		return auth.TierIdentified
	}
	return auth.TierTrusted
}

// receivePackBinary serves content via exec-mode SSH channels.
//
// Mode selection:
//  1. If root is configured and the path exists: build a packfile and write
//     raw PACK bytes (existing behaviour — browsers with Packfile.cpp use this).
//  2. If root is absent/empty OR the path is not found in root, AND a backend
//     is configured: GET <backend><path> and write the full HTTP/1.1 wire
//     response (status line + CRLF headers + blank line + body), same shape as
//     proxyCallBinary. The browser ResourceLoader reads this directly.
//  3. If neither condition is met: return an error.
func (h *Handler) receivePackBinary(args []string, w io.Writer) error {
	site := h.site()

	// Attempt filesystem mode when root is set.
	if site.Root != "" {
		pw, err := h.buildPackfile(args)
		if err == nil {
			_, werr := pw.WriteTo(w)
			return werr
		}
		// File not found in root — fall through to backend if configured.
		if h.backend == nil {
			return err
		}
	}

	// Backend fallback.
	if h.backend == nil {
		return fmt.Errorf("receive-pack: no content root configured and no backend set")
	}

	path := "/"
	if len(args) > 0 {
		path = args[0]
	}

	resp, err := h.backend.FetchResource(path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading backend response: %w", err)
	}

	// Write HTTP/1.1 wire format (mirrors proxyCallBinary / httpFetchBinary).
	statusText := statusTextOnly(resp.Status)
	if _, err := fmt.Fprintf(w, "HTTP/1.1 %d %s\r\n", resp.StatusCode, statusText); err != nil {
		return err
	}

	hdr := resp.Header.Clone()
	for _, h := range hopByHopHeaders {
		hdr.Del(h)
	}
	hdr.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	hdr.Set("Connection", "close")

	for key, values := range hdr {
		for _, v := range values {
			if _, err := fmt.Fprintf(w, "%s: %s\r\n", key, v); err != nil {
				return err
			}
		}
	}

	if _, err := io.WriteString(w, "\r\n"); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func (h *Handler) buildPackfile(args []string) (*packfile.Writer, error) {
	route := "/"
	if len(args) > 0 {
		route = args[0]
	}

	site := h.site()
	root := site.Root
	if root == "" {
		return nil, fmt.Errorf("no content root configured")
	}

	contentPath := root
	if route != "/" {
		contentPath = filepath.Join(root, filepath.Clean(route))
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root: %w", err)
	}
	absContent, err := filepath.Abs(contentPath)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}
	if !strings.HasPrefix(absContent, absRoot) {
		return nil, fmt.Errorf("path outside content root")
	}

	info, err := os.Stat(absContent)
	if err != nil {
		return nil, fmt.Errorf("content not found: %s", route)
	}

	pw := packfile.NewWriter()

	if info.IsDir() {
		err = filepath.Walk(absContent, func(path string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relPath, _ := filepath.Rel(absContent, path)
			pw.AddBlob(data)
			pw.AddFile(relPath, data)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("reading content: %w", err)
		}
	} else {
		data, err := os.ReadFile(absContent)
		if err != nil {
			return nil, fmt.Errorf("reading file: %w", err)
		}
		relName := filepath.Base(absContent)
		pw.AddBlob(data)
		pw.AddFile(relName, data)
	}

	return pw, nil
}

func (h *Handler) receivePack(args []string) (string, error) {
	pw, err := h.buildPackfile(args)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if _, err := pw.WriteTo(&buf); err != nil {
		return "", fmt.Errorf("writing packfile: %w", err)
	}

	// Text-mode: human-readable summary. Binary-mode (ExecuteBinary) writes
	// the raw packfile bytes via receivePackBinary.
	var out strings.Builder
	fmt.Fprintf(&out, "PACK v2 (%d objects, %d bytes)\n", pw.ObjectCount(), buf.Len())
	fmt.Fprintf(&out, "checksum: %s\n", hex.EncodeToString(buf.Bytes()[buf.Len()-20:]))
	fmt.Fprintf(&out, "\nObjects:\n")
	for _, f := range pw.Files() {
		fmt.Fprintf(&out, "  blob %s  %s (%d bytes)\n", f.SHA[:7], f.Name, f.Size)
	}
	return out.String(), nil
}

// rssFeed returns an Atom feed for a configured feed path.
// Currently generates a feed listing files in the site root; a future revision
// can drive the entries from an HTTP backend or config-defined items.
func (h *Handler) rssFeed(args []string) (string, error) {
	site := h.site()
	requestedPath := "/"
	if len(args) > 0 {
		requestedPath = args[0]
	}

	var feed *config.FeedConfig
	for i := range site.Meta.Feeds {
		if site.Meta.Feeds[i].Path == requestedPath {
			feed = &site.Meta.Feeds[i]
			break
		}
	}
	if feed == nil {
		return "", fmt.Errorf("no feed configured at %s", requestedPath)
	}

	// Build a minimal Atom feed listing the top-level files in site.Root.
	var entries []string
	if site.Root != "" {
		_ = filepath.Walk(site.Root, func(path string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(site.Root, path)
			entry := fmt.Sprintf(
				`  <entry><title>%s</title><id>ssh-web://%s/%s</id></entry>`,
				rel, site.Host, rel)
			entries = append(entries, entry)
			return nil
		})
	}

	out := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>%s</title>
  <id>ssh-web://%s%s</id>
%s
</feed>
`, site.Host, site.Host, feed.Path, strings.Join(entries, "\n"))
	return out, nil
}

// sitemap returns a JSON sitemap listing all known routes.
func (h *Handler) sitemap() (string, error) {
	site := h.site()
	type entry struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	var entries []entry

	for _, cmd := range site.Commands {
		if cmd.Type == "receive-pack" {
			entries = append(entries, entry{Path: cmd.Route, Type: "receive-pack"})
		}
	}

	if site.Meta.Sitemap.Dynamic && site.Root != "" {
		_ = filepath.Walk(site.Root, func(path string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(site.Root, path)
			entries = append(entries, entry{Path: "/" + rel, Type: "static"})
			return nil
		})
	}

	data, err := json.MarshalIndent(map[string]interface{}{
		"site":    site.Host,
		"entries": entries,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// robots returns the robots policy as JSON. SSH-Web's `robots` command
// replaces robots.txt with a structured, authenticated policy.
func (h *Handler) robots() (string, error) {
	site := h.site()
	policy := map[string]interface{}{
		"crawl-delay":    site.Meta.Robots.CrawlDelay,
		"allowed-paths":  site.Meta.Robots.AllowedPaths,
		"blocked-paths":  site.Meta.Robots.BlockedPaths,
	}
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// mcp invokes a configured MCP tool. The first arg is the tool name; remaining
// args are key=value parameter assignments. For now this validates the request
// against the configured tool schema and (when configured) forwards the call
// to a backend HTTP endpoint via h.backend.
func (h *Handler) mcp(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: mcp TOOL_NAME [key=value...]")
	}
	site := h.site()
	toolName := args[0]

	var tool *config.MCPTool
	for i := range site.MCP {
		if site.MCP[i].Name == toolName {
			tool = &site.MCP[i]
			break
		}
	}
	if tool == nil {
		return "", fmt.Errorf("unknown MCP tool: %s", toolName)
	}

	// Parse key=value pairs.
	params := map[string]string{}
	for _, kv := range args[1:] {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			return "", fmt.Errorf("invalid mcp argument %q (expected key=value)", kv)
		}
		params[kv[:eq]] = kv[eq+1:]
	}

	// Validate required params.
	for _, p := range tool.Params {
		if p.Required {
			if _, ok := params[p.Name]; !ok {
				return "", fmt.Errorf("missing required parameter: %s", p.Name)
			}
		}
	}

	// If a backend is configured, forward the call as a POST.
	if h.backend != nil {
		return h.backend.InvokeMCP(tool, params, "")
	}

	// No backend: echo the validated invocation as a placeholder result.
	body, _ := json.MarshalIndent(map[string]interface{}{
		"tool":   toolName,
		"params": params,
		"status": "validated (no backend configured)",
	}, "", "  ")
	return string(body), nil
}

// apiCall forwards an api-call to the configured HTTP backend.
// Usage: api-call METHOD /path [body-json]
// When no backend is configured, returns a not-implemented error so clients
// know the site exposes the route but has no handler wired up.
func (h *Handler) apiCall(args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: api-call METHOD /path [body]")
	}
	method := strings.ToUpper(args[0])
	path := args[1]

	// Verify the (METHOD, route) is one the config exposes. The auth check
	// already passed; this prevents api-call from reaching arbitrary backend
	// routes the operator didn't intend to expose.
	if !h.routeAllowed("api-call", method, path) {
		return "", fmt.Errorf("api-call %s %s not configured", method, path)
	}

	if h.backend == nil {
		return "", fmt.Errorf("api-call has no backend configured")
	}

	var body []byte
	if len(args) > 2 {
		body = []byte(strings.Join(args[2:], " "))
	}

	resp, status, err := h.backend.APICall(method, path, body, "")
	if err != nil {
		// Include the body so callers can see the upstream error detail.
		return "", fmt.Errorf("backend error (status %d): %w; body=%s", status, err, string(resp))
	}
	return string(resp), nil
}

// proxyCall fetches an external resource through the per-site allowlist.
// Usage: proxy-call METHOD url
// Currently only GET is implemented. The proxy.Cache enforces the allowlist
// and provides on-disk caching when configured.
//
// Text-mode form: returns just the response body as a string. Useful for
// debugging from the interactive shell. Browsers use the binary form below.
func (h *Handler) proxyCall(args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: proxy-call METHOD url")
	}
	method := strings.ToUpper(args[0])
	rawURL := args[1]
	if method != "GET" {
		return "", fmt.Errorf("proxy-call: only GET is supported in this version")
	}
	if h.proxy == nil {
		return "", fmt.Errorf("proxy-call: proxy-cache not configured for this site")
	}
	data, err := h.proxy.Fetch(rawURL)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// proxyCallBinary writes the upstream HTTP response — status line, headers,
// blank line, body — to w in HTTP/1.1 wire format. The browser parses this
// directly and uses the upstream Content-Type / Cache-Control / ETag, which
// removes a whole class of "I had to guess the MIME type" bugs and lets
// proper conditional fetches and caching land later.
//
// Hop-by-hop headers (Connection, Keep-Alive, Transfer-Encoding, ...) are
// stripped by proxy.FetchResponse before we get here; we set our own
// Content-Length and Connection: close instead.
func (h *Handler) proxyCallBinary(args []string, w io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: proxy-call METHOD url")
	}
	method := strings.ToUpper(args[0])
	rawURL := args[1]
	if method != "GET" {
		return fmt.Errorf("proxy-call: only GET is supported in this version")
	}
	if h.proxy == nil {
		return fmt.Errorf("proxy-call: proxy-cache not configured for this site")
	}

	resp, err := h.proxy.FetchResponse(rawURL)
	if err != nil {
		return err
	}

	status := resp.Status
	if status == "" {
		status = http.StatusText(resp.StatusCode)
	}
	if _, err := fmt.Fprintf(w, "HTTP/1.1 %d %s\r\n", resp.StatusCode, statusTextOnly(status)); err != nil {
		return err
	}

	// Force end-to-end framing we control. We write Content-Length explicitly
	// rather than chunking; we override any upstream value.
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(resp.Body)))
	resp.Header.Set("Connection", "close")

	for key, values := range resp.Header {
		for _, v := range values {
			if _, err := fmt.Fprintf(w, "%s: %s\r\n", key, v); err != nil {
				return err
			}
		}
	}

	if _, err := io.WriteString(w, "\r\n"); err != nil {
		return err
	}
	if _, err := w.Write(resp.Body); err != nil {
		return err
	}
	return nil
}

// hopByHopHeaders mirrors the list in proxy/proxy.go. Used by receivePackBinary
// when writing HTTP/1.1 wire format for the backend-fallback path.
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

// statusTextOnly strips a leading status code from a "200 OK"-style status
// string so we can format the status line consistently. Go's http.Response
// gives us "200 OK" but we already wrote the code separately.
func statusTextOnly(status string) string {
	parts := strings.SplitN(status, " ", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return status
}

// routeAllowed reports whether the (cmd, method, path) tuple was declared in
// the site's `commands` block. This is a defense-in-depth check on top of the
// tier authorization, ensuring sshttpd only forwards traffic to routes the
// operator explicitly declared.
func (h *Handler) routeAllowed(cmdType, method, path string) bool {
	site := h.site()
	for _, cmd := range site.Commands {
		if cmd.Type != cmdType {
			continue
		}
		if cmdType == "api-call" {
			if !strings.EqualFold(cmd.Method, method) {
				continue
			}
		}
		if routeMatches(cmd.Route, path) {
			return true
		}
	}
	return false
}

// routeMatches matches a configured route pattern against an actual path.
// `{name}` matches a single non-slash segment (e.g. `/posts/{id}` matches
// `/posts/42` but not `/posts/42/x`). `{name*}` is a terminal catch-all that
// matches zero or more remaining segments (e.g. `/{path*}` matches `/`,
// `/foo`, and `/foo/bar/baz`).
func routeMatches(pattern, path string) bool {
	if pattern == path {
		return true
	}
	if q := strings.IndexByte(path, '?'); q >= 0 {
		path = path[:q]
	}
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")

	for i, pp := range patternParts {
		isCatchAll := strings.HasPrefix(pp, "{") && strings.HasSuffix(pp, "*}")
		if isCatchAll {
			return i == len(patternParts)-1
		}
		if i >= len(pathParts) {
			return false
		}
		if strings.HasPrefix(pp, "{") && strings.HasSuffix(pp, "}") {
			continue
		}
		if pp != pathParts[i] {
			return false
		}
	}
	return len(patternParts) == len(pathParts)
}

// CapabilitiesManifest is the JSON structure returned by the capabilities command.
type CapabilitiesManifest struct {
	Protocol string                 `json:"protocol"`
	Site     SiteInfo               `json:"site"`
	Commands map[string]interface{} `json:"commands"`
	Auth     AuthInfo               `json:"auth"`
	MCP      *MCPInfo               `json:"mcp,omitempty"`
	Feeds    map[string]interface{} `json:"feeds,omitempty"`
	Sitemap  *SitemapInfo           `json:"sitemap,omitempty"`
	Robots   *RobotsInfo            `json:"robots,omitempty"`
}

// SitemapInfo describes the site's sitemap discovery surface.
type SitemapInfo struct {
	Dynamic bool   `json:"dynamic"`
	Path    string `json:"path"`
}

// RobotsInfo describes the site's robots policy.
type RobotsInfo struct {
	CrawlDelay   int      `json:"crawl-delay"`
	AllowedPaths []string `json:"allowed-paths,omitempty"`
	BlockedPaths []string `json:"blocked-paths,omitempty"`
}

type SiteInfo struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	Description string `json:"description,omitempty"`
}

type AuthInfo struct {
	Modes    []string `json:"modes"`
	KeyTypes []string `json:"key-types"`
}

type MCPInfo struct {
	Version string                   `json:"version"`
	Tools   []map[string]interface{} `json:"tools"`
}
