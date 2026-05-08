package mcpproxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type Proxy struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	nextID atomic.Int64
	tools  []Tool
}

// Start spawns an MCP server over stdio, performs the initialize handshake,
// and fetches the tool list. The command string is split on whitespace and
// executed directly (no shell expansion).
func Start(command string) (*Proxy, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("mcpproxy: empty command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcpproxy: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcpproxy: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcpproxy: start %q: %w", command, err)
	}

	p := &Proxy{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}

	if err := p.initialize(); err != nil {
		p.Close()
		return nil, fmt.Errorf("mcpproxy: initialize: %w", err)
	}

	if err := p.refreshTools(); err != nil {
		p.Close()
		return nil, fmt.Errorf("mcpproxy: tools/list: %w", err)
	}

	return p, nil
}

func (p *Proxy) initialize() error {
	result, err := p.call("initialize", json.RawMessage(`{
		"protocolVersion": "2024-11-05",
		"capabilities": {},
		"clientInfo": {"name": "sshttpd", "version": "0.1.0"}
	}`))
	if err != nil {
		return err
	}
	_ = result

	return p.notify("notifications/initialized", nil)
}

func (p *Proxy) refreshTools() error {
	result, err := p.call("tools/list", nil)
	if err != nil {
		return err
	}

	var listResult struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(result, &listResult); err != nil {
		return fmt.Errorf("parsing tools/list: %w", err)
	}
	p.tools = listResult.Tools
	return nil
}

// Tools returns the cached tool list from the MCP server.
func (p *Proxy) Tools() []Tool {
	return p.tools
}

// CallTool invokes a tool on the MCP server and returns the text result.
func (p *Proxy) CallTool(name string, arguments map[string]interface{}) (string, error) {
	params := map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return "", err
	}

	result, err := p.call("tools/call", raw)
	if err != nil {
		return "", err
	}

	var callResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError,omitempty"`
	}
	if err := json.Unmarshal(result, &callResult); err != nil {
		return "", fmt.Errorf("parsing tools/call result: %w", err)
	}

	var texts []string
	for _, c := range callResult.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	out := strings.Join(texts, "\n")

	if callResult.IsError {
		return "", fmt.Errorf("mcp tool error: %s", out)
	}
	return out, nil
}

// Close shuts down the MCP server process.
func (p *Proxy) Close() error {
	p.stdin.Close()
	return p.cmd.Wait()
}

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (p *Proxy) call(method string, params json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := p.nextID.Add(1)
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	if _, err := p.stdin.Write(data); err != nil {
		return nil, fmt.Errorf("writing request: %w", err)
	}

	for {
		line, err := p.stdout.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("reading response: %w", err)
		}
		line = trimCRLF(line)
		if len(line) == 0 {
			continue
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		// Skip notifications (no id)
		if resp.ID == nil {
			continue
		}
		if *resp.ID != id {
			continue
		}

		if resp.Error != nil {
			return nil, fmt.Errorf("jsonrpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (p *Proxy) notify(method string, params json.RawMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = p.stdin.Write(data)
	return err
}

func trimCRLF(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
