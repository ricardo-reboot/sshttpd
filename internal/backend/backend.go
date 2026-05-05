// Package backend implements the upstream HTTP proxy for api-call and mcp commands.
//
// When a site config specifies `backend http://host:port`, sshttpd forwards
// api-call requests to <backend><path> and MCP invocations to
// <backend>/mcp/<tool> as POST requests with the validated parameters as JSON.
//
// This is what turns sshttpd into a reverse proxy for an existing HTTP service:
// the application backend doesn't change, sshttpd translates SSH-Web commands
// into the same HTTP calls the application already receives from a traditional
// frontend like Caddy or nginx.
package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bugscave/sshttpd/internal/config"
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
// Returns the response body on success, or an error including the upstream status.
func (b *Backend) APICall(method, path string, body []byte, identityHeader string) ([]byte, int, error) {
	target := b.BaseURL + path
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("building request: %w", err)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if identityHeader != "" {
		req.Header.Set("X-SSHWeb-Identity", identityHeader)
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
//
// The backend URL is configured server-side, so no allowlist check is needed here —
// the operator controls what host is contacted.
//
// TODO: forward X-SSHWeb-Identity from the SSH session to let the backend act
// on SSH auth tier (same pattern as APICall). Omitted for now; document as a trust
// boundary: today the backend receives anonymous HTTP requests.
func (b *Backend) FetchResource(path string) (*http.Response, error) {
	target := b.BaseURL + path
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting backend: %w", err)
	}
	return resp, nil
}

// InvokeMCP forwards an MCP tool invocation as POST /mcp/<tool> with params as JSON.
func (b *Backend) InvokeMCP(tool *config.MCPTool, params map[string]string, identityHeader string) (string, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("marshaling mcp params: %w", err)
	}
	resp, _, err := b.APICall("POST", "/mcp/"+tool.Name, body, identityHeader)
	if err != nil {
		return "", err
	}
	return string(resp), nil
}
