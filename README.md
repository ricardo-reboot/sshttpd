# sshttpd

A reference implementation of the [SSH-Web protocol](spec/SSH-WEB-SPEC.md) — an authenticated, encrypted transport layer for the web built on SSH.

## What is SSH-Web?

SSH-Web replaces HTTP as the browser-to-server transport with a constrained SSH-based protocol. Instead of bolting encryption, authentication, and privacy onto a stateless plaintext protocol, SSH-Web starts from SSH's encrypted, authenticated foundation and builds web semantics on top.

Key properties:
- **Always encrypted** — no opt-in TLS, no certificate authorities
- **Identity via keypair** — no passwords, no OAuth, no third-party identity providers
- **Structural privacy** — third parties never see the client; external resources are server-proxied
- **Unified interface** — website, API, and agent interface are one thing, discoverable from a single `capabilities` handshake
- **Efficient delivery** — Git packfile format with delta compression and incremental updates

## What sshttpd does

`sshttpd` is the role of Caddy/nginx for the SSH-Web protocol: a deployable daemon you put in front of an existing application backend.

- **Static content** is served directly from a configured `root` as Git packfiles.
- **Dynamic API requests** are forwarded as plain HTTP to a configured `backend` (`api-call POST /api/items` ↔ `POST http://localhost:8080/api/items`).
- **MCP tool invocations** are forwarded the same way (`mcp submit_story title=... url=...` ↔ `POST http://localhost:8080/mcp/submit_story`).
- **External resource fetches** are proxied through an allowlisted, TTL-cached fetcher so third parties never see the client.
- **Identity** is supplied by the user's SSH keypair; tiers (anonymous/identified/trusted) are enforced per command.
- **Discovery** (RSS feeds, sitemap, robots policy) is structured JSON/Atom served by sshttpd directly, surfaced in the `capabilities` manifest.
- **Multi-site** hosting: one `sshttpd` instance can serve any number of `site { … }` blocks, each on its own port, with its own host key, content root, and auth.

## Project Status

**Phase 1: Proof of Concept** — substantially complete.

- [x] SSH transport with constrained command execution
- [x] `capabilities` manifest discovery (now surfaces all configured surfaces)
- [x] `receive-pack` static site delivery via real PACK v2 binary
- [x] `api-call` dynamic request handling — forwarded to a configured HTTP backend
- [x] `proxy-call` external resource proxying with allowlist + cache
- [x] Configuration file parsing (with `backend`, `authorized-keys`, multi-site)
- [x] Authentication tiers (anonymous, identified, trusted) — enforced
- [x] `authorized-keys` file with `tier=trusted` / `tier=identified` overrides
- [x] Rate limiting per tier (token-bucket)
- [x] `mcp` tool dispatch with parameter validation + backend forwarding
- [x] `rss-feed`, `sitemap`, `robots` discovery commands
- [x] Multi-site listeners on distinct ports
- [x] Identity wiring: browser identity selection → SSH publickey auth end-to-end
- [x] Key registration flow with dynamic `authorized-keys` reload
- [x] Server-side redirects (302) with URL bar update in browser
- [x] Pubkey + display name forwarding to backend via `X-SSHWeb-PubKey`
- [x] Tier-gated content (per-post access control in demo backend)
- [x] SSH connection pool clear on page reload (fresh auth on navigate)
- [ ] `stdio` command handlers — spawn processes with stdin/stdout wired to SSH channel, bypassing HTTP framing entirely (CGI-over-SSH)
- [ ] File downloads — `Content-Disposition: attachment` handling in browser, save dialog
- [ ] Incremental `receive-pack` (`--have` over the wire)
- [ ] On-disk `proxy-cache` storage (currently in-memory)
- [ ] SNI-style routing of multiple sites on a shared port

See [CHANGELOG.md](CHANGELOG.md) for a complete change log.

## Architecture

```
cmd/sshttpd/          Entry point for the daemon
internal/
  server/             Multi-site listener, ssh handshake, exec/shell sessions
  config/             Configuration parser
  commands/           Command handlers (capabilities, receive-pack, api-call,
                      proxy-call, rss-feed, sitemap, robots, mcp)
  packfile/           Git PACK v2 generation + delta computation
  auth/               Tier classification, authorized-keys parser,
                      command-allowed matching (wildcards, qualifiers)
  proxy/              External-resource fetcher with allowlist + TTL cache
  backend/            HTTP forwarder for api-call and mcp
  ratelimit/          Token-bucket per auth tier
spec/                 Protocol specification
docs/browser/         SSH-Web browser (Ladybird fork) architecture and design
docs/server/          sshttpd server documentation
examples/             Example site configuration
```

## Building

```bash
go build -o sshttpd ./cmd/sshttpd

# With version stamp:
go build -ldflags "-X main.version=0.1.0" -o sshttpd ./cmd/sshttpd
```

Tests:

```bash
go test ./internal/...
```

## Running

```bash
# (Optional) generate a host key — sshttpd will auto-generate one if missing.
ssh-keygen -t ed25519 -f /etc/sshttpd/keys/host_ed25519 -N ""

# Start the daemon.
./sshttpd -config examples/sshttpd.conf
```

By default sshttpd listens on the port from each `site` block (22443 unless overridden). Connect with any SSH client to verify:

```bash
ssh -p 22443 localhost capabilities
ssh -p 22443 localhost "receive-pack /" | xxd | head     # raw PACK bytes
ssh -p 22443 localhost sitemap
ssh -p 22443 localhost robots
ssh -p 22443 localhost "rss-feed /feeds/posts"
```

## Configuration

The full annotated example lives at [examples/sshttpd.conf](examples/sshttpd.conf). Minimal example:

```
site example.com {
    port      22443
    host-key  /path/to/host_ed25519
    root      /var/www/example
    backend   http://localhost:8080      # forwards api-call + mcp here

    commands {
        receive-pack /
        api-call GET  /api/items
        api-call POST /api/items
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
}
```

### Tier classification with `authorized-keys`

```
site example.com {
    authorized-keys /etc/sshttpd/authorized_keys
    ...
}
```

`authorized_keys` format (one entry per line, optional `tier=` prefix):

```
# Default tier for any presented key is `identified`.
ssh-ed25519 AAAAC3Nz... regular-user@example

# Override to put a key in the trusted tier.
tier=trusted ssh-ed25519 AAAAC3Nz... admin@example
```

### Reverse-proxy mode

When `backend http://...` is set, sshttpd forwards SSH-Web commands to the upstream as plain HTTP. The application doesn't need to know anything about SSH-Web.

```
api-call POST /api/items {"title":"hi"}
   ↓
POST http://backend/api/items
Content-Type: application/json
X-SSHWeb-Identity: <key fingerprint or "">
{"title":"hi"}
```

```
mcp submit_story title=hi url=https://x
   ↓
POST http://backend/mcp/submit_story
Content-Type: application/json
{"title":"hi","url":"https://x"}
```

This is the path that lets you wrap an existing Rails/Express/Django/etc. app and serve it over SSH-Web with no changes to the application code.

## Companion browser

A reference SSH-Web browser (a fork of [Ladybird](https://ladybird.org)) is included as a [git submodule](ladybird/). Architecture and design docs live in [docs/browser/](docs/browser/).

## Specification

The full protocol specification is at [spec/SSH-WEB-SPEC.md](spec/SSH-WEB-SPEC.md).

## License

Apache-2.0 — see [LICENSE](LICENSE).
