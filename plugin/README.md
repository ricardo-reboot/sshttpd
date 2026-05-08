# sshweb — Claude Code Plugin

SSH-Web protocol integration for Claude Code. Dynamically discovers and exposes MCP tools from any SSH-Web site.

## What it does

1. **Tool discovery** — Connect to any sshttpd site and its MCP tools become available to Claude. No per-site configuration needed.
2. **Auto-detection** — When you mention a domain in chat, the plugin probes port 22443. If an SSH-Web site is found, Claude is prompted to connect and discover tools.
3. **Dynamic tools** — Navigate between sites and the available tools change. One plugin, every SSH-Web site.

## Install

```bash
# From source (requires Go)
cd plugin && ./scripts/build.sh

# Test locally
claude --plugin-dir ./plugin
```

## Usage

Once installed, Claude has a `sshweb_connect` tool. Use it directly:

> "Connect to localhost:22443 and show me what tools are available"

Or just mention a domain — the auto-detection hook probes port 22443 and prompts Claude to connect.

After connecting, site tools appear with `{host}/` prefix:

> "List all posts" → Claude calls `localhost/list_posts`

## How it works

```
Claude Code ←MCP/stdio→ sshweb-mcp ←SSH→ sshttpd (any site)
                              ↓
                   capabilities → tools/list (dynamic)
                   mcp invoke  → tools/call (forwarded over SSH)
                   site change → notifications/tools/list_changed
```

The MCP server (`sshweb-mcp`) connects to sshttpd via SSH, fetches the capabilities manifest, and registers each MCP tool the site exposes. When Claude calls a tool, the server forwards it as `mcp <tool> key=value...` over the SSH connection.

## Plugin structure

```
plugin/
├── .claude-plugin/
│   └── plugin.json          # Plugin manifest + MCP server config
├── bin/
│   └── sshweb-mcp           # Go binary (built from cmd/sshweb-mcp/)
├── hooks/
│   └── hooks.json           # Auto-detect SSH-Web domains in prompts
├── skills/
│   └── sshweb-connect/
│       └── SKILL.md         # Skill for connecting to SSH-Web sites
├── scripts/
│   ├── build.sh             # Build sshweb-mcp binary
│   └── detect-sshweb.sh     # Domain detection hook script
└── README.md
```

## Building for distribution

```bash
# All platforms
./scripts/build.sh --all

# Output:
#   bin/sshweb-mcp-darwin-arm64
#   bin/sshweb-mcp-darwin-amd64
#   bin/sshweb-mcp-linux-amd64
#   bin/sshweb-mcp-linux-arm64
```

## Requirements

- sshttpd running on the target site (port 22443 by default)
- MCP tools must be allowed for the auth tier the plugin connects with (anonymous by default)
