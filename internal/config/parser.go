package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// parser is a simple recursive descent parser for the sshttpd config format.
type parser struct {
	input  string
	pos    int
	line   int
	col    int
}

func newParser(input string) *parser {
	return &parser{input: input, line: 1, col: 1}
}

func (p *parser) peek() byte {
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *parser) advance() byte {
	ch := p.input[p.pos]
	p.pos++
	if ch == '\n' {
		p.line++
		p.col = 1
	} else {
		p.col++
	}
	return ch
}

func (p *parser) skipWhitespace() {
	for p.pos < len(p.input) {
		ch := p.peek()
		if ch == '#' {
			// Skip comment to end of line
			for p.pos < len(p.input) && p.peek() != '\n' {
				p.advance()
			}
		} else if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			p.advance()
		} else {
			break
		}
	}
}

func (p *parser) readWord() string {
	p.skipWhitespace()
	start := p.pos
	for p.pos < len(p.input) {
		ch := p.peek()
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '{' || ch == '}' || ch == '#' {
			break
		}
		p.advance()
	}
	return p.input[start:p.pos]
}

func (p *parser) readUntilNewline() string {
	start := p.pos
	for p.pos < len(p.input) && p.peek() != '\n' && p.peek() != '#' {
		p.advance()
	}
	return strings.TrimSpace(p.input[start:p.pos])
}

func (p *parser) expectByte(expected byte) error {
	p.skipWhitespace()
	if p.pos >= len(p.input) {
		return fmt.Errorf("line %d: expected '%c', got EOF", p.line, expected)
	}
	if p.peek() != expected {
		return fmt.Errorf("line %d: expected '%c', got '%c'", p.line, expected, p.peek())
	}
	p.advance()
	return nil
}

// readBracketList reads a [...] list of comma-separated items.
func (p *parser) readBracketList() ([]string, error) {
	p.skipWhitespace()
	if p.peek() != '[' {
		return nil, fmt.Errorf("line %d: expected '[', got '%c'", p.line, p.peek())
	}
	p.advance()

	var items []string
	for {
		p.skipWhitespace()
		if p.peek() == ']' {
			p.advance()
			return items, nil
		}
		// Read item until comma or ]
		start := p.pos
		for p.pos < len(p.input) && p.peek() != ',' && p.peek() != ']' {
			p.advance()
		}
		item := strings.TrimSpace(p.input[start:p.pos])
		if item != "" {
			items = append(items, item)
		}
		if p.peek() == ',' {
			p.advance()
		}
	}
}

// parseBool accepts true/false/yes/no/on/off (case-insensitive). Anything else
// is treated as false so a typo doesn't silently relax security defaults.
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

func parseConfig(input string) (*Config, error) {
	p := newParser(input)
	cfg := &Config{}

	for {
		p.skipWhitespace()
		if p.pos >= len(p.input) {
			break
		}

		keyword := p.readWord()
		switch keyword {
		case "site":
			site, err := p.parseSite()
			if err != nil {
				return nil, err
			}
			cfg.Sites = append(cfg.Sites, *site)
		default:
			return nil, fmt.Errorf("line %d: unexpected top-level keyword: %q", p.line, keyword)
		}
	}

	return cfg, nil
}

func (p *parser) parseSite() (*SiteConfig, error) {
	host := p.readWord()
	if host == "" {
		return nil, fmt.Errorf("line %d: site requires a hostname", p.line)
	}

	if err := p.expectByte('{'); err != nil {
		return nil, err
	}

	site := &SiteConfig{
		Host: host,
		Port: 22443, // default
	}

	for {
		p.skipWhitespace()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("unexpected EOF in site block")
		}
		if p.peek() == '}' {
			p.advance()
			return site, nil
		}

		keyword := p.readWord()
		switch keyword {
		case "port":
			val := p.readWord()
			port, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid port: %s", p.line, val)
			}
			site.Port = port
		case "host-key":
			site.HostKey = p.readWord()
		case "root":
			site.Root = p.readWord()
		case "backend":
			site.Backend = p.readWord()
		case "authorized-keys":
			site.AuthorizedKeys = p.readWord()
		case "commands":
			cmds, err := p.parseCommands()
			if err != nil {
				return nil, err
			}
			site.Commands = cmds
		case "meta":
			meta, err := p.parseMeta()
			if err != nil {
				return nil, err
			}
			site.Meta = *meta
		case "mcp":
			tools, err := p.parseMCP()
			if err != nil {
				return nil, err
			}
			site.MCP = tools
		case "proxy-cache":
			pc, err := p.parseProxyCache()
			if err != nil {
				return nil, err
			}
			site.ProxyCache = *pc
		case "auth":
			auth, err := p.parseAuth()
			if err != nil {
				return nil, err
			}
			site.Auth = *auth
		case "limits":
			limits, err := p.parseLimits()
			if err != nil {
				return nil, err
			}
			site.Limits = *limits
		default:
			return nil, fmt.Errorf("line %d: unknown directive in site block: %q", p.line, keyword)
		}
	}
}

func (p *parser) parseCommands() ([]CommandConfig, error) {
	if err := p.expectByte('{'); err != nil {
		return nil, err
	}

	var cmds []CommandConfig
	for {
		p.skipWhitespace()
		if p.peek() == '}' {
			p.advance()
			return cmds, nil
		}

		cmdType := p.readWord()
		rest := p.readUntilNewline()

		switch cmdType {
		case "receive-pack":
			cmds = append(cmds, CommandConfig{Type: "receive-pack", Route: rest})
		case "api-call":
			parts := strings.Fields(rest)
			if len(parts) < 2 {
				return nil, fmt.Errorf("line %d: api-call requires METHOD and route", p.line)
			}
			cmds = append(cmds, CommandConfig{Type: "api-call", Method: parts[0], Route: parts[1]})
		default:
			cmds = append(cmds, CommandConfig{Type: cmdType, Route: rest})
		}
	}
}

func (p *parser) parseMeta() (*MetaConfig, error) {
	if err := p.expectByte('{'); err != nil {
		return nil, err
	}

	meta := &MetaConfig{}
	for {
		p.skipWhitespace()
		if p.peek() == '}' {
			p.advance()
			return meta, nil
		}

		keyword := p.readWord()
		rest := p.readUntilNewline()

		switch keyword {
		case "rss-feed":
			parts := strings.Fields(rest)
			feed := FeedConfig{Path: parts[0]}
			for _, part := range parts[1:] {
				if strings.HasPrefix(part, "format=") {
					feed.Format = strings.TrimPrefix(part, "format=")
				}
			}
			meta.Feeds = append(meta.Feeds, feed)
		case "sitemap":
			parts := strings.Fields(rest)
			meta.Sitemap.Path = parts[0]
			for _, part := range parts[1:] {
				if part == "dynamic=true" {
					meta.Sitemap.Dynamic = true
				}
			}
		case "robots":
			parts := strings.Fields(rest)
			for _, part := range parts {
				if strings.HasPrefix(part, "crawl-delay=") {
					val := strings.TrimPrefix(part, "crawl-delay=")
					meta.Robots.CrawlDelay, _ = strconv.Atoi(val)
				}
				if strings.HasPrefix(part, "allow=") {
					listStr := strings.TrimPrefix(part, "allow=")
					listStr = strings.Trim(listStr, "[]")
					for _, item := range strings.Split(listStr, ",") {
						item = strings.Trim(strings.TrimSpace(item), "\"")
						if item != "" {
							meta.Robots.AllowedPaths = append(meta.Robots.AllowedPaths, item)
						}
					}
				}
			}
		}
	}
}

func (p *parser) parseMCP() ([]MCPTool, error) {
	if err := p.expectByte('{'); err != nil {
		return nil, err
	}

	var tools []MCPTool
	for {
		p.skipWhitespace()
		if p.peek() == '}' {
			p.advance()
			return tools, nil
		}

		keyword := p.readWord()
		if keyword != "tool" {
			return nil, fmt.Errorf("line %d: expected 'tool' in mcp block, got %q", p.line, keyword)
		}

		name := p.readWord()
		tool := MCPTool{Name: name}

		// Read the { params: [...] } block
		p.skipWhitespace()
		if p.peek() == '{' {
			p.advance()
			// Read until closing }
			for {
				p.skipWhitespace()
				if p.peek() == '}' {
					p.advance()
					break
				}
				key := p.readWord()
				if key == "params:" {
					list, err := p.readBracketList()
					if err != nil {
						return nil, err
					}
					for _, paramName := range list {
						tool.Params = append(tool.Params, MCPParam{Name: paramName, Type: "string", Required: true})
					}
				} else {
					// Skip unknown key
					p.readUntilNewline()
				}
			}
		}

		tools = append(tools, tool)
	}
}

func (p *parser) parseProxyCache() (*ProxyCacheConfig, error) {
	if err := p.expectByte('{'); err != nil {
		return nil, err
	}

	pc := &ProxyCacheConfig{}
	for {
		p.skipWhitespace()
		if p.peek() == '}' {
			p.advance()
			return pc, nil
		}

		keyword := p.readWord()
		switch keyword {
		case "allow":
			origin := p.readWord()
			pc.AllowedOrigins = append(pc.AllowedOrigins, origin)
		case "deny":
			val := p.readWord()
			if val == "*" {
				pc.DenyAll = true
			}
		case "ttl":
			pc.TTL = p.readWord()
		case "max-size":
			pc.MaxSize = p.readWord()
		case "storage":
			pc.StoragePath = p.readWord()
		case "max-redirects":
			n, err := strconv.Atoi(p.readWord())
			if err != nil {
				return nil, fmt.Errorf("line %d: max-redirects: %w", p.line, err)
			}
			pc.MaxRedirects = n
		case "allow-private-ips":
			pc.AllowPrivateIPs = parseBool(p.readWord())
		case "allow-redirects":
			b := parseBool(p.readWord())
			pc.AllowRedirects = &b
		default:
			p.readUntilNewline()
		}
	}
}

func (p *parser) parseAuth() (*AuthConfig, error) {
	if err := p.expectByte('{'); err != nil {
		return nil, err
	}

	auth := &AuthConfig{}
	for {
		p.skipWhitespace()
		if p.peek() == '}' {
			p.advance()
			return auth, nil
		}

		tier := p.readWord()
		p.skipWhitespace()
		list, err := p.readBracketList()
		if err != nil {
			return nil, fmt.Errorf("line %d: parsing auth list for %s: %w", p.line, tier, err)
		}

		switch tier {
		case "anonymous":
			auth.Anonymous = list
		case "identified":
			auth.Identified = list
		case "trusted":
			auth.Trusted = list
		}
	}
}

func (p *parser) parseLimits() (*LimitsConfig, error) {
	if err := p.expectByte('{'); err != nil {
		return nil, err
	}

	limits := &LimitsConfig{}
	for {
		p.skipWhitespace()
		if p.peek() == '}' {
			p.advance()
			return limits, nil
		}

		tier := p.readWord()
		val := p.readWord()

		switch tier {
		case "anonymous":
			limits.Anonymous = val
		case "identified":
			limits.Identified = val
		case "trusted":
			limits.Trusted = val
		}
	}
}

// Load reads and parses a configuration file.
func load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return parseConfig(string(data))
}
