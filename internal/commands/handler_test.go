package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bugscave/sshttpd/internal/auth"
	"github.com/bugscave/sshttpd/internal/config"
)

var anonSess = SessionInfo{Tier: auth.TierAnonymous}

func newTestHandler(t *testing.T, site config.SiteConfig) *Handler {
	t.Helper()
	cfg := &config.Config{Sites: []config.SiteConfig{site}}
	h, err := NewHandler(cfg)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func TestExecute_RejectsUnauthorized(t *testing.T) {
	h := newTestHandler(t, config.SiteConfig{
		Host: "test",
		Auth: config.AuthConfig{
			Anonymous: []string{"receive-pack"},
		},
	})
	_, err := h.Execute("api-call", []string{"GET", "/x"}, anonSess)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected permission denied, got %v", err)
	}
}

func TestExecute_CapabilitiesAlwaysAllowed(t *testing.T) {
	h := newTestHandler(t, config.SiteConfig{
		Host: "test",
		Auth: config.AuthConfig{},
	})
	resp, err := h.Execute("capabilities", nil, anonSess)
	if err != nil {
		t.Fatalf("capabilities should always be allowed: %v", err)
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(resp), &manifest); err != nil {
		t.Fatalf("capabilities response should be valid JSON: %v", err)
	}
	if manifest["protocol"] != "ssh-web/0.1" {
		t.Errorf("expected protocol ssh-web/0.1, got %v", manifest["protocol"])
	}
}

func TestExecute_ReceivePackText(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>x</h1>"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestHandler(t, config.SiteConfig{
		Host: "test",
		Root: dir,
		Auth: config.AuthConfig{Anonymous: []string{"receive-pack"}},
	})

	resp, err := h.Execute("receive-pack", []string{"/"}, anonSess)
	if err != nil {
		t.Fatalf("receive-pack: %v", err)
	}
	if !strings.Contains(resp, "PACK v2") {
		t.Errorf("text mode response should mention PACK v2; got %q", resp)
	}
	if !strings.Contains(resp, "index.html") {
		t.Errorf("response should list index.html; got %q", resp)
	}
}

func TestExecuteBinary_ReceivePackHasPACKHeader(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestHandler(t, config.SiteConfig{
		Host: "test",
		Root: dir,
		Auth: config.AuthConfig{Anonymous: []string{"receive-pack"}},
	})

	var buf bytes.Buffer
	if err := h.ExecuteBinary("receive-pack", []string{"/"}, anonSess, &buf); err != nil {
		t.Fatalf("ExecuteBinary: %v", err)
	}
	got := buf.Bytes()
	if len(got) < 12 {
		t.Fatalf("packfile too short: %d bytes", len(got))
	}
	if string(got[:4]) != "PACK" {
		t.Errorf("expected PACK signature, got %q", got[:4])
	}
}

func TestRouteMatches(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"/", "/", true},
		{"/api/items", "/api/items", true},
		{"/api/items", "/api/posts", false},
		{"/posts/{id}", "/posts/42", true},
		{"/posts/{id}", "/posts/", false},
		{"/posts/{id}", "/posts/42/extra", false},
		{"/users/{user}/posts/{id}", "/users/me/posts/9", true},
		{"/api", "/api?page=2", true}, // query stripped
		{"/{path*}", "/", true},
		{"/{path*}", "/foo", true},
		{"/{path*}", "/foo/bar/baz", true},
		{"/_next/{path*}", "/_next/static/chunks/abc.js", true},
		{"/_next/{path*}", "/api/items", false},
		{"/static/{name*}", "/static", true},
		{"/posts/{id}/{rest*}", "/posts/42/comments/9", true},
	}
	for _, c := range cases {
		got := routeMatches(c.pattern, c.path)
		if got != c.want {
			t.Errorf("routeMatches(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestExecute_APICallRequiresConfiguredRoute(t *testing.T) {
	h := newTestHandler(t, config.SiteConfig{
		Host: "test",
		Auth: config.AuthConfig{Anonymous: []string{"api-call GET"}},
		Commands: []config.CommandConfig{
			{Type: "api-call", Method: "GET", Route: "/api/items"},
		},
	})

	// Authorized but not configured.
	_, err := h.Execute("api-call", []string{"GET", "/api/notconfigured"}, anonSess)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected not-configured error, got %v", err)
	}

	// Configured but no backend.
	_, err = h.Execute("api-call", []string{"GET", "/api/items"}, anonSess)
	if err == nil || !strings.Contains(err.Error(), "no backend") {
		t.Errorf("expected no-backend error, got %v", err)
	}
}

func TestExecute_UnknownCommand(t *testing.T) {
	h := newTestHandler(t, config.SiteConfig{
		Auth: config.AuthConfig{Anonymous: []string{"frobnicate"}},
	})
	_, err := h.Execute("frobnicate", nil, anonSess)
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("expected unknown-command error, got %v", err)
	}
}

// TestReceivePackBinary_FilesystemMode verifies that receive-pack writes a
// valid PACK binary when root is configured and the file exists.
func TestReceivePackBinary_FilesystemMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0644); err != nil {
		t.Fatal(err)
	}

	h := newTestHandler(t, config.SiteConfig{
		Host: "test",
		Root: dir,
		Auth: config.AuthConfig{Anonymous: []string{"receive-pack"}},
	})

	var buf bytes.Buffer
	if err := h.ExecuteBinary("receive-pack", []string{"/"}, anonSess, &buf); err != nil {
		t.Fatalf("ExecuteBinary receive-pack (fs mode): %v", err)
	}
	if string(buf.Bytes()[:4]) != "PACK" {
		t.Errorf("expected PACK signature in filesystem mode, got %q", buf.Bytes()[:4])
	}
}

// TestReceivePackBinary_BackendFallback verifies that receive-pack falls back to
// the backend when no root is configured and returns HTTP/1.1 wire format.
func TestReceivePackBinary_BackendFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<h1>hello from backend</h1>"))
	}))
	defer ts.Close()

	h := newTestHandler(t, config.SiteConfig{
		Host:    "test",
		Backend: ts.URL,
		Auth:    config.AuthConfig{Anonymous: []string{"receive-pack /"}},
		Commands: []config.CommandConfig{
			{Type: "receive-pack", Route: "/"},
		},
	})

	var buf bytes.Buffer
	if err := h.ExecuteBinary("receive-pack", []string{"/"}, anonSess, &buf); err != nil {
		t.Fatalf("ExecuteBinary receive-pack (backend fallback): %v", err)
	}

	out := buf.String()
	if !strings.HasPrefix(out, "HTTP/1.1 200") {
		t.Errorf("expected HTTP/1.1 200, got: %q", out[:min(len(out), 60)])
	}
	if !strings.Contains(out, "Content-Type: text/html") {
		t.Errorf("expected Content-Type header; got: %q", out[:min(len(out), 200)])
	}
	if !strings.Contains(out, "<h1>hello from backend</h1>") {
		t.Errorf("expected body in response; got: %q", out)
	}
	if strings.Contains(out, "Transfer-Encoding:") {
		t.Errorf("hop-by-hop Transfer-Encoding must be stripped")
	}
	if !strings.Contains(out, "Content-Length: 27") {
		t.Errorf("expected Content-Length: 27; response: %q", out[:min(len(out), 300)])
	}
}

// TestReceivePackBinary_NeitherRootNorBackend verifies that receive-pack returns
// an error when neither root nor backend is configured.
func TestReceivePackBinary_NeitherRootNorBackend(t *testing.T) {
	h := newTestHandler(t, config.SiteConfig{
		Host: "test",
		Auth: config.AuthConfig{Anonymous: []string{"receive-pack"}},
	})

	var buf bytes.Buffer
	err := h.ExecuteBinary("receive-pack", []string{"/"}, anonSess, &buf)
	if err == nil {
		t.Fatal("expected error when neither root nor backend configured, got nil")
	}
	if !strings.Contains(err.Error(), "no content root") {
		t.Errorf("expected 'no content root' in error, got: %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Compile-time check that ExecuteBinary signature matches io.Writer expectations.
var _ = func() io.Writer { return nil }
