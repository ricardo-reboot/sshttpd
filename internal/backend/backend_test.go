package backend

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNew_EmptyURLReturnsNil(t *testing.T) {
	b, err := New("")
	if err != nil {
		t.Fatalf("New(\"\"): %v", err)
	}
	if b != nil {
		t.Errorf("expected nil backend for empty URL, got %v", b)
	}
}

func TestAPICall_ForwardsMethodPathAndBody(t *testing.T) {
	var gotMethod, gotPath, gotBody, gotTier, gotFP string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		gotTier = r.Header.Get("X-SSHWeb-Tier")
		gotFP = r.Header.Get("X-SSHWeb-Fingerprint")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	b, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	resp, status, err := b.APICall("POST", "/api/items", []byte(`{"x":1}`), "identified", "SHA256:abc123")
	if err != nil {
		t.Fatalf("APICall: %v", err)
	}
	if status != 200 {
		t.Errorf("status=%d want 200", status)
	}
	if gotMethod != "POST" {
		t.Errorf("method=%q want POST", gotMethod)
	}
	if gotPath != "/api/items" {
		t.Errorf("path=%q want /api/items", gotPath)
	}
	if gotBody != `{"x":1}` {
		t.Errorf("body=%q want %q", gotBody, `{"x":1}`)
	}
	if gotTier != "identified" {
		t.Errorf("tier header=%q want identified", gotTier)
	}
	if gotFP != "SHA256:abc123" {
		t.Errorf("fingerprint header=%q want SHA256:abc123", gotFP)
	}
	if !strings.Contains(string(resp), "ok") {
		t.Errorf("response=%q expected to contain 'ok'", resp)
	}
}

func TestAPICall_PropagatesUpstreamErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "kaboom", http.StatusBadGateway)
	}))
	defer srv.Close()

	b, _ := New(srv.URL)
	body, status, err := b.APICall("GET", "/x", nil, "", "")
	if err == nil {
		t.Fatalf("expected error for upstream 502")
	}
	if status != http.StatusBadGateway {
		t.Errorf("status=%d want 502", status)
	}
	if !strings.Contains(string(body), "kaboom") {
		t.Errorf("body should propagate upstream message, got %q", body)
	}
}

