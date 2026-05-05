# Changelog

All notable changes to the sshttpd reference implementation are documented here.

## [Unreleased]

### Added — reverse-proxy daemon feature pass

#### Authorization & identity
- **Tier authorization enforced on every command** (`capabilities` excepted). Wildcard
  entries (`admin-*`), tier inheritance (identified ⊇ anonymous, trusted ⊇ all),
  and qualified entries (`api-call GET`) all match correctly.
- **`authorized-keys` file** support: one OpenSSH-format public key per line,
  optionally prefixed with `tier=trusted` or `tier=identified`. Default for any
  presented key is `identified`.
- `key-fingerprint` now exposed as a permission extension on each connection,
  logged alongside the tier.

#### HTTP backend (reverse proxy)
- **New `internal/backend` package**. When a site declares `backend http://host:port`,
  `api-call` requests are forwarded as `<METHOD> <path>` and `mcp` invocations as
  `POST /mcp/<tool_name>` with parameters as JSON. Identity flows through as the
  `X-SSHWeb-Identity` header.
- **Route allowlist enforcement** on `api-call`: requests must match a `commands {}`
  declaration, with `{param}` placeholders supported (`/posts/{id}`).
- Upstream errors propagate with status code and body to the SSH client.

#### Real packfile binary
- `Handler.ExecuteBinary` writes raw `PACK\x00\x00\x00\x02 …` bytes to exec-mode
  SSH channels (browser path). Interactive shell sessions still get the human-
  readable summary via `Execute`.
- Path-traversal protection on `receive-pack` (refuses paths outside `root`).
- SSH `exit-status` requests now sent on every command completion (0 success,
  1 error, 2 empty command, 3 rate limited).

#### Multi-site daemon
- `Server` hosts a `siteListener` per configured `site` block. Each listener
  has its own port, host key, command handler, authorized-keys file, and
  rate limiter.
- Connection logs prefixed with the site name and include user, tier, and
  key fingerprint.

#### Rate limiting
- **New `internal/ratelimit` package**: token-bucket per tier. Rate spec format
  `<count>/<unit>` where unit is `sec`/`min`/`hour`/`day`. Empty or
  `unlimited` disables the limit. Bucket fills to capacity at startup; the
  `Allow` check refills since last call.

#### Discovery commands
- **`rss-feed <path>`** — returns Atom XML for the configured feed. Currently
  auto-generated from the site root; ready for backend-driven entries.
- **`sitemap`** — returns JSON listing configured `receive-pack` routes plus,
  when `dynamic=true`, a recursive walk of the site root.
- **`robots`** — returns the structured policy (crawl-delay, allowed paths,
  blocked paths) as JSON.
- **`mcp <tool> key=value...`** — validates required parameters against the
  configured tool schema and forwards to the backend (when configured) or
  echoes the validated invocation as a placeholder.

#### Capabilities manifest
- Now surfaces every configured surface: `api-call` routes (with auth tier
  annotations), `proxy-call` allowlist, `receive-pack` routes, `feeds`,
  `sitemap`, `robots`, and the full MCP tool schema (parameters and types).

#### Configuration
- New directives: `backend http://...`, `authorized-keys <path>`.
- Multi-site configs are now supported (one `site` block per port).

#### Tests
- 28 test functions across `internal/auth`, `internal/backend`,
  `internal/commands`, `internal/config`, `internal/proxy`, `internal/ratelimit`.
  Covers tier matching, authorized_keys parsing, HTTP forwarding,
  command authorization, route matching, packfile binary mode, config
  parsing, proxy allowlist + caching, and rate-limit token-bucket parsing.

### Earlier — initial scaffolding

#### Core Server
- SSH transport layer using `golang.org/x/crypto/ssh` on port 22443
- Connection handling with anonymous session support
- `exec` mode for single-command execution (e.g. `ssh -p 22443 localhost "capabilities"`)
- `shell` mode for interactive sessions with line-based REPL

#### Host Key Management
- Load existing ed25519 host keys from disk
- Auto-generate and persist a new ed25519 host key when none exists
- Log key fingerprint on startup for TOFU verification

#### Configuration parser
- Custom config format parser (recursive descent)
- `site` blocks with `port`, `host-key`, `root`, `commands`, `meta`, `mcp`, `proxy-cache`, `auth`, `limits`
- Bracket-list parsing for auth tiers and MCP params
- Comment support (`#`)

#### Commands (initial)
- `capabilities` — returns service manifest as JSON
- `receive-pack /` — scans the site root, packs files into Git-format packfile

#### Packfile engine
- Git-format packfile writer (PACK v2 header, object count, trailing SHA-1 checksum)
- Blob object encoding with proper Git object headers (`blob <size>\0`)
- Tree object encoding with mode/name/SHA entries
- Zlib compression per object
- Variable-length size encoding matching Git's pack format
- Delta computation helper (filters objects by SHA against a `--have` set)

#### Proxy cache
- In-memory cache with TTL-based expiration
- Origin allowlist enforcement (deny-by-default)
- HTTP fetch from upstream with configurable timeout

### Not yet implemented
- Incremental packfile updates (`--have` over the wire in `receive-pack`)
- On-disk `proxy-cache` storage (currently in-memory only)
- `search` command (mentioned in spec §5.1)
- SNI-style routing of multiple sites on a shared port (currently one port per site)
- Per-key revocation lists
- TLS/SSHFP DNS record validation
- Config hot-reload
