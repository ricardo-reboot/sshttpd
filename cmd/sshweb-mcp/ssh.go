package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
)

type sshwebConn struct {
	host         string
	port         int
	client       *ssh.Client
	tools        []siteTool
	capabilities *capabilitiesManifest
}

type siteTool struct {
	name        string
	description string
	params      map[string]toolParam
	auth        string
}

type toolParam struct {
	typ      string
	required bool
}

func (t *siteTool) mcpName(host string) string {
	return fmt.Sprintf("%s/%s", sanitizeHost(host), t.name)
}

func (t *siteTool) asMCPTool(host string) map[string]interface{} {
	properties := map[string]interface{}{}
	required := []string{}

	for name, p := range t.params {
		prop := map[string]interface{}{
			"type": "string",
		}
		if p.typ != "" && p.typ != "string" {
			prop["type"] = p.typ
		}
		properties[name] = prop
		if p.required {
			required = append(required, name)
		}
	}

	schema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	desc := t.description
	if desc == "" {
		desc = fmt.Sprintf("MCP tool '%s' on %s", t.name, host)
	}

	return map[string]interface{}{
		"name":        t.mcpName(host),
		"description": desc,
		"inputSchema": schema,
	}
}

func sanitizeHost(host string) string {
	return strings.ReplaceAll(strings.ReplaceAll(host, ".", "_"), ":", "_")
}

func dialSSHWeb(host string, port int) (*sshwebConn, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	log.Printf("connecting to %s", addr)

	config := &ssh.ClientConfig{
		User: "anonymous",
		Auth: []ssh.AuthMethod{
			ssh.Password(""),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		BannerCallback:  func(message string) error { return nil },
	}

	tcpConn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("TCP connect: %w", err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(tcpConn, addr, config)
	if err != nil {
		tcpConn.Close()
		return nil, fmt.Errorf("SSH handshake: %w", err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)

	return &sshwebConn{
		host:   host,
		port:   port,
		client: client,
	}, nil
}

func (c *sshwebConn) close() {
	if c.client != nil {
		c.client.Close()
	}
}

func (c *sshwebConn) execCommand(cmd string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(cmd); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%s: %s", err, stderr.String())
		}
		return "", err
	}

	return stdout.String(), nil
}

func (c *sshwebConn) fetchCapabilities() (*capabilitiesManifest, error) {
	out, err := c.execCommand("capabilities")
	if err != nil {
		return nil, err
	}

	var caps capabilitiesManifest
	if err := json.Unmarshal([]byte(out), &caps); err != nil {
		return nil, fmt.Errorf("parsing capabilities: %w", err)
	}

	return &caps, nil
}

func (c *sshwebConn) invokeMCP(toolName string, args map[string]interface{}) (string, error) {
	cmd := "mcp " + toolName
	for k, v := range args {
		cmd += fmt.Sprintf(" %s=%v", k, v)
	}

	return c.execCommand(cmd)
}

// capabilitiesManifest mirrors the JSON returned by the capabilities command.
type capabilitiesManifest struct {
	Protocol string                 `json:"protocol"`
	Site     siteInfo               `json:"site"`
	Commands map[string]interface{} `json:"commands"`
	Auth     authInfo               `json:"auth"`
	MCP      *mcpInfo               `json:"mcp,omitempty"`
	Feeds    map[string]interface{} `json:"feeds,omitempty"`
}

type siteInfo struct {
	Name string `json:"name"`
	Host string `json:"host"`
}

type authInfo struct {
	Modes    []string `json:"modes"`
	KeyTypes []string `json:"key_types"`
}

type mcpInfo struct {
	Version string                   `json:"version"`
	Tools   []map[string]interface{} `json:"tools"`
}

func parseToolsFromCapabilities(caps *capabilitiesManifest) ([]siteTool, error) {
	if caps.MCP == nil || len(caps.MCP.Tools) == 0 {
		return nil, nil
	}

	var tools []siteTool
	for _, raw := range caps.MCP.Tools {
		name, _ := raw["name"].(string)
		if name == "" {
			continue
		}

		t := siteTool{
			name:        name,
			description: strOrEmpty(raw["description"]),
			auth:        strOrEmpty(raw["auth"]),
			params:      map[string]toolParam{},
		}

		if params, ok := raw["params"].(map[string]interface{}); ok {
			for pName, pVal := range params {
				tp := toolParam{}
				if m, ok := pVal.(map[string]interface{}); ok {
					tp.typ, _ = m["type"].(string)
					tp.required, _ = m["required"].(bool)
				}
				t.params[pName] = tp
			}
		}

		tools = append(tools, t)
	}

	return tools, nil
}

func strOrEmpty(v interface{}) string {
	s, _ := v.(string)
	return s
}
