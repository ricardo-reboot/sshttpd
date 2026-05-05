package proxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bugscave/sshttpd/internal/config"
)

func TestFetch_AllowedOrigin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello-from-origin"))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{host},
		AllowPrivateIPs: true,
	})

	data, err := cache.Fetch(srv.URL + "/x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != "hello-from-origin" {
		t.Errorf("got %q", data)
	}
}

func TestFetch_DisallowedOrigin(t *testing.T) {
	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins: []string{"trusted.example"},
	})

	_, err := cache.Fetch("http://evil.example/x")
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("expected allowlist error, got %v", err)
	}
}

func TestFetch_CacheHitDoesntContactOrigin(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("v1"))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{host},
		AllowPrivateIPs: true,
	})

	for i := 0; i < 5; i++ {
		if _, err := cache.Fetch(srv.URL); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
	}
	if hits != 1 {
		t.Errorf("expected exactly 1 origin hit, got %d", hits)
	}
}

func TestFetchResponse_PreservesStatusAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "font/ttf")
		w.Header().Set("Cache-Control", "max-age=31536000")
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte("FONT_BYTES"))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{host},
		AllowPrivateIPs: true,
	})

	resp, err := cache.FetchResponse(srv.URL + "/font.ttf")
	if err != nil {
		t.Fatalf("FetchResponse: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status=%d want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "font/ttf" {
		t.Errorf("content-type=%q want font/ttf", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "max-age=31536000" {
		t.Errorf("cache-control=%q", got)
	}
	if got := resp.Header.Get("ETag"); got != `"abc123"` {
		t.Errorf("etag=%q", got)
	}
	if string(resp.Body) != "FONT_BYTES" {
		t.Errorf("body=%q", resp.Body)
	}
}

func TestFetchResponse_PassesThroughUpstreamErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "kaboom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{host},
		AllowPrivateIPs: true,
	})

	resp, err := cache.FetchResponse(srv.URL)
	if err != nil {
		t.Fatalf("FetchResponse should not fail on 500: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("status=%d want 500", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "kaboom") {
		t.Errorf("body should preserve upstream message, got %q", resp.Body)
	}
}

func TestFetch_WildcardAllowsAnyHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{"*"},
		AllowPrivateIPs: true,
	})

	if _, err := cache.Fetch(srv.URL); err != nil {
		t.Fatalf("wildcard should allow any host: %v", err)
	}
}

func TestFetch_WildcardWithDenyAllIsIgnored(t *testing.T) {
	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins: []string{"*"},
		DenyAll:        true,
	})

	_, err := cache.Fetch("http://anywhere.example/x")
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("DenyAll must veto wildcard, got %v", err)
	}
}

func TestFetch_SubdomainGlob(t *testing.T) {
	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins: []string{"*.cartocdn.com"},
	})

	if !cache.isAllowed("basemaps.cartocdn.com") {
		t.Error("subdomain glob should match basemaps.cartocdn.com")
	}
	if !cache.isAllowed("tiles.basemaps.cartocdn.com") {
		t.Error("subdomain glob should match nested subdomains")
	}
	if cache.isAllowed("cartocdn.com") {
		t.Error("subdomain glob should NOT match apex")
	}
	if cache.isAllowed("evil.com") {
		t.Error("subdomain glob should not match other domains")
	}
}

func TestFetchResponse_StripsHopByHopHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{host},
		AllowPrivateIPs: true,
	})

	resp, err := cache.FetchResponse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Connection") != "" {
		t.Errorf("Connection should be stripped, got %q", resp.Header.Get("Connection"))
	}
	if resp.Header.Get("Keep-Alive") != "" {
		t.Errorf("Keep-Alive should be stripped")
	}
	if resp.Header.Get("Content-Type") != "text/plain" {
		t.Errorf("end-to-end Content-Type should be preserved")
	}
}

func TestFetch_RejectsNonHTTPScheme(t *testing.T) {
	cache := NewCache(config.ProxyCacheConfig{AllowedOrigins: []string{"*"}})
	_, err := cache.Fetch("file:///etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Errorf("expected scheme rejection, got %v", err)
	}
	_, err = cache.Fetch("gopher://anything.example/")
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Errorf("expected scheme rejection, got %v", err)
	}
}

func TestFetch_RejectsURLWithUserInfo(t *testing.T) {
	cache := NewCache(config.ProxyCacheConfig{AllowedOrigins: []string{"*"}})
	_, err := cache.Fetch("http://attacker:pw@example.com/")
	if err == nil || !strings.Contains(err.Error(), "userinfo") {
		t.Errorf("expected userinfo rejection, got %v", err)
	}
}

func TestFetch_BlocksPrivateIPLiteralByDefault(t *testing.T) {
	cache := NewCache(config.ProxyCacheConfig{AllowedOrigins: []string{"*"}})
	for _, target := range []string{
		"http://127.0.0.1/x",
		"http://10.0.0.1/x",
		"http://192.168.1.1/x",
		"http://169.254.169.254/latest/meta-data/", // AWS metadata
		"http://[::1]/x",
		"http://[fe80::1]/x",
		"http://[fc00::1]/x",
	} {
		if _, err := cache.Fetch(target); err == nil || !strings.Contains(err.Error(), "non-public") {
			t.Errorf("%s should be blocked, got %v", target, err)
		}
	}
}

func TestIsBlockedIP_IPv4MappedLoopback(t *testing.T) {
	// IPv4-mapped IPv6 form: ::ffff:127.0.0.1 — net.ParseIP unwraps to v4
	// and isBlockedIP must still flag it as loopback. Go's URL parser does
	// not accept this form in the host component, so we test the runtime
	// guard directly (this is the path a malicious DNS response would take).
	if !isBlockedIP(net.ParseIP("::ffff:127.0.0.1")) {
		t.Error("IPv4-mapped loopback must be blocked")
	}
	if !isBlockedIP(net.ParseIP("::ffff:169.254.169.254")) {
		t.Error("IPv4-mapped AWS metadata must be blocked")
	}
}

func TestFetch_AllowPrivateIPsOptIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{"*"},
		AllowPrivateIPs: true,
	})
	if _, err := cache.Fetch(srv.URL); err != nil {
		t.Errorf("opt-in should allow loopback, got %v", err)
	}
}

func TestFetch_RedirectToBlockedHostRejected(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer target.Close()
	targetHost := strings.TrimPrefix(target.URL, "http://")

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, target.URL, http.StatusFound)
	}))
	defer redirector.Close()
	redirectorHost := strings.TrimPrefix(redirector.URL, "http://")

	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{redirectorHost}, // target NOT allowlisted
		AllowPrivateIPs: true,
	})

	_, err := cache.Fetch(redirector.URL)
	if err == nil || !strings.Contains(err.Error(), "redirect host not in allowlist") {
		t.Errorf("redirect to non-allowlisted host should fail, got %v (target host %s)", err, targetHost)
	}
}

func TestFetch_RedirectChainCapped(t *testing.T) {
	var srv *httptest.Server
	hops := 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{host},
		AllowPrivateIPs: true,
		MaxRedirects:    3,
	})

	_, err := cache.Fetch(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "redirect chain exceeded") {
		t.Errorf("expected redirect cap, got %v", err)
	}
}

func TestFetch_RedirectsDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/somewhere", http.StatusFound)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	follow := false
	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{host},
		AllowPrivateIPs: true,
		AllowRedirects:  &follow,
	})

	resp, err := cache.FetchResponse(srv.URL)
	if err != nil {
		t.Fatalf("got %v, expected redirect to be returned as-is", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302 to surface, got %d", resp.StatusCode)
	}
}

func TestFetch_BodySizeCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 1024))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cache := NewCache(config.ProxyCacheConfig{
		AllowedOrigins:  []string{host},
		AllowPrivateIPs: true,
		MaxSize:         "256B",
	})

	_, err := cache.Fetch(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "max-size") {
		t.Errorf("expected size cap error, got %v", err)
	}
}

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"", 0, false},
		{"512", 512, true},
		{"1k", 1024, true},
		{"1KB", 1000, true},
		{"2mb", 2_000_000, true},
		{"4MiB", 4 * 1024 * 1024, true},
		{"1G", 1 << 30, true},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := parseSize(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseSize(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",
		"10.1.2.3",
		"172.16.0.1",
		"192.168.0.1",
		"169.254.169.254",
		"100.64.0.1",
		"::1",
		"fe80::1",
		"fc00::1",
	}
	for _, s := range blocked {
		if ip := net.ParseIP(s); ip == nil || !isBlockedIP(ip) {
			t.Errorf("expected %s blocked", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"}
	for _, s := range allowed {
		if ip := net.ParseIP(s); ip == nil || isBlockedIP(ip) {
			t.Errorf("expected %s allowed", s)
		}
	}
}
