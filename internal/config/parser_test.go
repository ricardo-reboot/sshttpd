package config

import (
	"strings"
	"testing"
)

func TestParseConfig_FullSite(t *testing.T) {
	input := `
site example.com {
    port 32443
    host-key /etc/sshttpd/host_ed25519
    root /var/www/example
    backend http://localhost:8080
    authorized-keys /etc/sshttpd/authorized_keys

    commands {
        receive-pack /
        receive-pack /posts/{id}
        api-call GET  /api/items
        api-call POST /api/items
    }

    proxy-cache {
        allow fonts.googleapis.com
        allow cdn.imgur.com
    }

    auth {
        anonymous   [receive-pack, api-call GET]
        identified  [api-call POST]
        trusted     [admin-*]
    }

    limits {
        anonymous   60/min
        identified  300/min
        trusted     unlimited
    }

    meta {
        rss-feed /feeds/posts format=atom
        sitemap  /sitemap     dynamic=true
        robots   crawl-delay=10 allow=["/"]
    }

    mcp {
        tool list_posts
        tool create_post
    }
}
`
	cfg, err := parseConfig(input)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if len(cfg.Sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(cfg.Sites))
	}
	site := cfg.Sites[0]
	if site.Host != "example.com" || site.Port != 32443 {
		t.Errorf("host/port: %s:%d", site.Host, site.Port)
	}
	if site.Backend != "http://localhost:8080" {
		t.Errorf("backend=%q", site.Backend)
	}
	if site.AuthorizedKeys != "/etc/sshttpd/authorized_keys" {
		t.Errorf("authorized-keys=%q", site.AuthorizedKeys)
	}
	if len(site.Commands) != 4 {
		t.Errorf("expected 4 commands, got %d", len(site.Commands))
	}
	if len(site.ProxyCache.AllowedOrigins) != 2 {
		t.Errorf("expected 2 proxy origins, got %d", len(site.ProxyCache.AllowedOrigins))
	}
	if len(site.Auth.Anonymous) != 2 || site.Auth.Anonymous[0] != "receive-pack" {
		t.Errorf("auth.anonymous=%v", site.Auth.Anonymous)
	}
	if site.Limits.Anonymous != "60/min" {
		t.Errorf("limits.anonymous=%q", site.Limits.Anonymous)
	}
	if len(site.Meta.Feeds) != 1 || site.Meta.Feeds[0].Format != "atom" {
		t.Errorf("feeds=%v", site.Meta.Feeds)
	}
	if !site.Meta.Sitemap.Dynamic || site.Meta.Sitemap.Path != "/sitemap" {
		t.Errorf("sitemap=%v", site.Meta.Sitemap)
	}
	if site.Meta.Robots.CrawlDelay != 10 {
		t.Errorf("robots.crawl-delay=%d", site.Meta.Robots.CrawlDelay)
	}
	if len(site.MCP) != 2 {
		t.Errorf("expected 2 mcp tools, got %d", len(site.MCP))
	}
}

func TestParseConfig_MultipleSites(t *testing.T) {
	input := `
site a.com {
    port 1
}
site b.com {
    port 2
}
`
	cfg, err := parseConfig(input)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if len(cfg.Sites) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(cfg.Sites))
	}
	if cfg.Sites[0].Port != 1 || cfg.Sites[1].Port != 2 {
		t.Errorf("ports=%d,%d", cfg.Sites[0].Port, cfg.Sites[1].Port)
	}
}

func TestParseConfig_RejectsUnknownDirective(t *testing.T) {
	_, err := parseConfig(`site x { invalid-keyword }`)
	if err == nil || !strings.Contains(err.Error(), "unknown directive") {
		t.Errorf("expected unknown-directive error, got %v", err)
	}
}

func TestParseConfig_DefaultPort(t *testing.T) {
	cfg, err := parseConfig(`site x { }`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.Sites[0].Port != 22443 {
		t.Errorf("default port should be 22443, got %d", cfg.Sites[0].Port)
	}
}
