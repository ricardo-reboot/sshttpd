// Package backend implements the upstream HTTP proxy for api-call and
// receive-pack commands.
//
// When a site config specifies `backend http://host:port`, sshttpd forwards
// requests to <backend><path>. SSH session identity (tier and key fingerprint)
// is forwarded via X-SSHWeb-Tier and X-SSHWeb-Fingerprint headers so the
// backend can make authorization and content decisions.
package backend

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Backend forwards SSH-Web commands to an HTTP upstream.
type Backend struct {
	BaseURL string // e.g. "http://localhost:8080"
	Client  *http.Client
}

// New creates a Backend with the given base URL.
// Returns nil if baseURL is empty (no backend configured).
func New(baseURL string) (*Backend, error) {
	if baseURL == "" {
		return nil, nil
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("invalid backend URL %q: %w", baseURL, err)
	}
	return &Backend{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// APICall forwards an api-call to the backend as method+path with the given body.
// SSH session identity is forwarded via X-SSHWeb-Tier and X-SSHWeb-Fingerprint headers
// so the backend can make authorization and content decisions.
func (b *Backend) APICall(method, path string, body []byte, tier, fingerprint string) ([]byte, int, error) {
	target := b.BaseURL + path
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("building request: %w", err)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if tier != "" {
		req.Header.Set("X-SSHWeb-Tier", tier)
	}
	if fingerprint != "" {
		req.Header.Set("X-SSHWeb-Fingerprint", fingerprint)
	}
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("contacting backend: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return respBody, resp.StatusCode, fmt.Errorf("backend returned %d", resp.StatusCode)
	}
	return respBody, resp.StatusCode, nil
}

// FetchResource performs a GET to <BaseURL><path> and returns the live *http.Response.
// The caller is responsible for draining and closing the response body.
func (b *Backend) FetchResource(path, tier, fingerprint string) (*http.Response, error) {
	target := b.BaseURL + path
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	if tier != "" {
		req.Header.Set("X-SSHWeb-Tier", tier)
	}
	if fingerprint != "" {
		req.Header.Set("X-SSHWeb-Fingerprint", fingerprint)
	}
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting backend: %w", err)
	}
	return resp, nil
}

