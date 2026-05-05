package config


// Config holds the full sshttpd configuration.
type Config struct {
	Sites []SiteConfig
}

// SiteConfig holds configuration for a single virtual host.
type SiteConfig struct {
	Host    string
	Port    int
	HostKey string
	Root    string // content root directory
	Backend string // optional HTTP upstream for api-call / mcp (e.g. "http://localhost:8080")

	// Optional file containing one OpenSSH-format public key per line.
	// Lines may be prefixed with `trusted ` to put the key in the trusted tier
	// (default tier for any presented key is `identified`).
	AuthorizedKeys string

	Commands   []CommandConfig
	ProxyCache ProxyCacheConfig
	Auth       AuthConfig
	Limits     LimitsConfig
	MCP        []MCPTool
	Meta       MetaConfig
}

// CommandConfig defines an exposed command route.
type CommandConfig struct {
	Type   string // "receive-pack", "api-call", "proxy-call"
	Method string // GET, POST (for api-call)
	Route  string // URL pattern
}

// ProxyCacheConfig defines external resource proxying rules.
type ProxyCacheConfig struct {
	AllowedOrigins []string
	DenyAll        bool
	TTL            string
	MaxSize        string
	StoragePath    string

	// SSRF hardening knobs. Zero/empty values mean "use defaults".
	MaxRedirects     int  // default 10; 0 disables redirect-following
	AllowPrivateIPs  bool // default false; allowlist private/loopback/link-local IPs
	AllowRedirects   *bool // nil = follow up to MaxRedirects; pointer so explicit `false` differs from default
}

// AuthConfig maps authentication tiers to allowed commands.
type AuthConfig struct {
	Anonymous  []string
	Identified []string
	Trusted    []string
}

// LimitsConfig defines rate limits per authentication tier.
type LimitsConfig struct {
	Anonymous  string
	Identified string
	Trusted    string
}

// MCPTool defines an MCP-compatible tool exposed by the server.
type MCPTool struct {
	Name        string
	Description string
	Params      []MCPParam
	Auth        string
}

// MCPParam defines a parameter for an MCP tool.
type MCPParam struct {
	Name     string
	Type     string
	Required bool
}

// MetaConfig holds discovery metadata configuration.
type MetaConfig struct {
	Feeds   []FeedConfig
	Sitemap SitemapConfig
	Robots  RobotsConfig
}

type FeedConfig struct {
	Path   string
	Format string
}

type SitemapConfig struct {
	Path    string
	Dynamic bool
}

type RobotsConfig struct {
	CrawlDelay   int
	AllowedPaths []string
	BlockedPaths []string
}

// Load reads and parses a configuration file.
func Load(path string) (*Config, error) {
	return load(path)
}

// Default returns a minimal configuration for development/testing.
func Default() *Config {
	return &Config{
		Sites: []SiteConfig{
			{
				Host:    "localhost",
				Port:    22443,
				HostKey: "",
				Commands: []CommandConfig{
					{Type: "receive-pack", Route: "/"},
				},
				Auth: AuthConfig{
					Anonymous: []string{"receive-pack", "capabilities"},
				},
				Limits: LimitsConfig{
					Anonymous:  "60/min",
					Identified: "300/min",
					Trusted:    "unlimited",
				},
			},
		},
	}
}
