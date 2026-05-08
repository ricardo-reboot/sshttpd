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
	var gotMethod, gotPath, gotBody, gotIdentity string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		gotIdentity = r.Header.Get("X-SSHWeb-Identity")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	b, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	resp, status, err := b.APICall("POST", "/api/items", []byte(`{"x":1}`), "fp:abc")
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
	if gotIdentity != "fp:abc" {
		t.Errorf("identity header=%q want fp:abc", gotIdentity)
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
	body, status, err := b.APICall("GET", "/x", nil, "")
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

