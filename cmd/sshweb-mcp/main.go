// sshweb-mcp is an MCP server that dynamically exposes SSH-Web site tools.
//
// It connects to any sshttpd instance, fetches the capabilities manifest,
// and registers the site's MCP tools as callable MCP tools. When the site
// changes, the tool list updates and Claude is notified.
//
// Usage in .claude/settings.json:
//
//	{
//	  "mcpServers": {
//	    "sshweb": {
//	      "command": "sshweb-mcp",
//	      "args": []
//	    }
//	  }
//	}
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lshortfile)

	s := newServer()
	if err := s.run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

type server struct {
	mu   sync.Mutex
	conn *sshwebConn // current SSH-Web connection, nil if disconnected

	writer io.Writer
	reader *bufio.Reader
}

func newServer() *server {
	return &server{
		writer: os.Stdout,
		reader: bufio.NewReader(os.Stdin),
	}
}

func (s *server) run() error {
	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("reading stdin: %w", err)
		}

		var msg jsonrpcRequest
		if err := json.Unmarshal(line, &msg); err != nil {
			s.sendError(nil, -32700, "parse error", nil)
			continue
		}

		s.handle(&msg)
	}
}

func (s *server) handle(msg *jsonrpcRequest) {
	switch msg.Method {
	case "initialize":
		s.handleInitialize(msg)
	case "initialized":
		// notification, no response needed
	case "ping":
		s.sendResult(msg.ID, map[string]interface{}{})
	case "tools/list":
		s.handleToolsList(msg)
	case "tools/call":
		s.handleToolsCall(msg)
	default:
		s.sendError(msg.ID, -32601, fmt.Sprintf("method not found: %s", msg.Method), nil)
	}
}

func (s *server) handleInitialize(msg *jsonrpcRequest) {
	s.sendResult(msg.ID, map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{
				"listChanged": true,
			},
		},
		"serverInfo": map[string]interface{}{
			"name":    "sshweb-mcp",
			"version": "0.1.0",
		},
	})
}

func (s *server) handleToolsList(msg *jsonrpcRequest) {
	tools := []interface{}{}

	// Always present: connect tool
	tools = append(tools, map[string]interface{}{
		"name":        "sshweb_connect",
		"description": "Connect to an SSH-Web site and discover its MCP tools. Tools from the site become available after connecting.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"host": map[string]interface{}{
					"type":        "string",
					"description": "Hostname of the SSH-Web site",
				},
				"port": map[string]interface{}{
					"type":        "integer",
					"description": "SSH-Web port (default 22443)",
					"default":     22443,
				},
			},
			"required": []string{"host"},
		},
	})

	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()

	if conn != nil {
		// Disconnect tool
		tools = append(tools, map[string]interface{}{
			"name":        "sshweb_disconnect",
			"description": fmt.Sprintf("Disconnect from %s:%d", conn.host, conn.port),
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		})

		// Status tool
		tools = append(tools, map[string]interface{}{
			"name":        "sshweb_status",
			"description": fmt.Sprintf("Show connection status and capabilities for %s:%d", conn.host, conn.port),
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		})

		// Site's MCP tools
		for _, tool := range conn.tools {
			tools = append(tools, tool.asMCPTool(conn.host))
		}
	}

	s.sendResult(msg.ID, map[string]interface{}{"tools": tools})
}

func (s *server) handleToolsCall(msg *jsonrpcRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.sendError(msg.ID, -32602, "invalid params", nil)
		return
	}

	switch params.Name {
	case "sshweb_connect":
		s.handleConnect(msg, params.Arguments)
	case "sshweb_disconnect":
		s.handleDisconnect(msg)
	case "sshweb_status":
		s.handleStatus(msg)
	default:
		s.handleSiteToolCall(msg, params.Name, params.Arguments)
	}
}

func (s *server) handleConnect(msg *jsonrpcRequest, args map[string]interface{}) {
	host, _ := args["host"].(string)
	if host == "" {
		s.sendError(msg.ID, -32602, "host is required", nil)
		return
	}
	port := 22443
	if p, ok := args["port"].(float64); ok {
		port = int(p)
	}

	// Disconnect existing connection
	s.mu.Lock()
	if s.conn != nil {
		s.conn.close()
		s.conn = nil
	}
	s.mu.Unlock()

	conn, err := dialSSHWeb(host, port)
	if err != nil {
		s.sendToolResult(msg.ID, fmt.Sprintf("Failed to connect to %s:%d: %v", host, port, err), true)
		return
	}

	caps, err := conn.fetchCapabilities()
	if err != nil {
		conn.close()
		s.sendToolResult(msg.ID, fmt.Sprintf("Connected but failed to fetch capabilities: %v", err), true)
		return
	}

	tools, err := parseToolsFromCapabilities(caps)
	if err != nil {
		log.Printf("warning: parsing tools: %v", err)
	}
	conn.tools = tools
	conn.capabilities = caps

	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	// Notify tools changed
	s.sendNotification("notifications/tools/list_changed", nil)

	summary := fmt.Sprintf("Connected to %s:%d\nProtocol: %s\nSite: %s\nTools discovered: %d",
		host, port,
		caps.Protocol,
		caps.Site.Name,
		len(tools))

	if len(tools) > 0 {
		summary += "\n\nAvailable tools:"
		for _, t := range tools {
			summary += fmt.Sprintf("\n  - %s", t.name)
			if t.description != "" {
				summary += fmt.Sprintf(": %s", t.description)
			}
		}
	}

	s.sendToolResult(msg.ID, summary, false)
}

func (s *server) handleDisconnect(msg *jsonrpcRequest) {
	s.mu.Lock()
	if s.conn != nil {
		s.conn.close()
		s.conn = nil
	}
	s.mu.Unlock()

	s.sendNotification("notifications/tools/list_changed", nil)
	s.sendToolResult(msg.ID, "Disconnected", false)
}

func (s *server) handleStatus(msg *jsonrpcRequest) {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()

	if conn == nil {
		s.sendToolResult(msg.ID, "Not connected to any SSH-Web site", false)
		return
	}

	caps, _ := json.MarshalIndent(conn.capabilities, "", "  ")
	s.sendToolResult(msg.ID, fmt.Sprintf("Connected to %s:%d\n\nCapabilities:\n%s", conn.host, conn.port, string(caps)), false)
}

func (s *server) handleSiteToolCall(msg *jsonrpcRequest, name string, args map[string]interface{}) {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()

	if conn == nil {
		s.sendToolResult(msg.ID, "Not connected to any SSH-Web site. Use sshweb_connect first.", true)
		return
	}

	// Find the tool
	var tool *siteTool
	for i := range conn.tools {
		if conn.tools[i].mcpName(conn.host) == name {
			tool = &conn.tools[i]
			break
		}
	}
	if tool == nil {
		s.sendError(msg.ID, -32602, fmt.Sprintf("unknown tool: %s", name), nil)
		return
	}

	// Build the SSH command: mcp <tool_name> key=value...
	result, err := conn.invokeMCP(tool.name, args)
	if err != nil {
		s.sendToolResult(msg.ID, fmt.Sprintf("Error: %v", err), true)
		return
	}

	s.sendToolResult(msg.ID, result, false)
}

// JSON-RPC helpers

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (s *server) sendResult(id interface{}, result interface{}) {
	s.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func (s *server) sendError(id interface{}, code int, message string, data interface{}) {
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	if data != nil {
		resp["error"].(map[string]interface{})["data"] = data
	}
	s.send(resp)
}

func (s *server) sendToolResult(id interface{}, text string, isError bool) {
	content := []map[string]interface{}{
		{"type": "text", "text": text},
	}
	result := map[string]interface{}{"content": content}
	if isError {
		result["isError"] = true
	}
	s.sendResult(id, result)
}

func (s *server) sendNotification(method string, params interface{}) {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	s.send(msg)
}

func (s *server) send(msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("marshal error: %v", err)
		return
	}
	data = append(data, '\n')
	s.writer.Write(data)
}
