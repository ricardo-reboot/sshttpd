# SSH-Web: A Transport Protocol Proposal for the Authenticated Web

**Version:** 0.1.0-draft
**Author:** Ricardo (Bugscave)
**Date:** May 2026
**Status:** Concept Specification

---

## Abstract

SSH-Web proposes replacing HTTP as the browser-to-server transport layer with a constrained SSH-based protocol. Instead of layering authentication, encryption, session management, identity, and privacy onto a stateless plaintext protocol through decades of bolt-on standards (TLS, cookies, OAuth, CORS, CSP), SSH-Web starts from an encrypted, authenticated, command-driven transport and builds the web's semantics on top of it.

The result is a protocol where user identity is a local keypair, every connection is encrypted by default, third-party tracking is structurally impossible, and every server is simultaneously a website, an API, and a machine-readable service — discoverable from a single capabilities handshake.

---

## 1. Motivation

### 1.1 The Accretion Problem

HTTP was designed in 1991 for retrieving hypertext documents over trusted academic networks. Thirty-five years later, we've bolted onto it:

- TLS for encryption (because HTTP was plaintext)
- Cookies for session state (because HTTP was stateless)
- OAuth/OIDC for identity (because HTTP had no auth model for users)
- CORS for cross-origin security (because the browser trusts all origins equally)
- CSP for script injection prevention (because inline content is trusted by default)
- Certificate Authorities for server identity (because HTTP had no trust model)
- Cache-Control headers for caching (because intermediaries needed hints)
- WebSockets for bidirectional streams (because HTTP was request-response only)
- HTTP/2 multiplexing (because HTTP/1.1 couldn't share connections)
- HTTP/3 / QUIC (because TCP head-of-line blocking killed multiplexing)

Each layer solves a real problem, but the aggregate complexity is staggering. A modern HTTPS request involves a DNS lookup, a TCP handshake, a TLS handshake (with certificate chain validation against a trusted CA store), SNI negotiation, ALPN protocol selection, HTTP/2 stream multiplexing, HPACK header compression, cookie attachment, CORS preflight (possibly), CSP evaluation, and finally the actual content transfer — all to read a blog post.

### 1.2 The Identity Crisis

User identity on the modern web is controlled by a handful of corporations. "Sign in with Google" is the path of least resistance for both developers and users. Browser profiles, synced across devices by Google, Microsoft, or Apple, are the de facto identity layer. The user does not own their identity — they rent it.

SSH solved identity in 1995. A keypair on the user's machine. The user controls it, backs it up, revokes it. No third party required.

### 1.3 The Privacy Inversion

When a browser loads a modern webpage, it contacts 15-80 third-party domains: font providers, analytics services, tracking pixels, CDN-hosted libraries, ad networks. Each of those third parties sees the user's IP, browser fingerprint, and can correlate activity across sites. The user has no meaningful control over this.

SSH-Web inverts this model. The browser connects to one server. That server proxies all external dependencies. Third parties never see the user.

---

## 2. Protocol Overview

### 2.1 Connection Model

An SSH-Web connection follows the standard SSH transport (RFC 4253) with the following constraints:

```
Browser                          sshttpd (server)
   |                                  |
   |--- TCP connect (port 22443) ---->|
   |--- SSH handshake --------------->|
   |    (key exchange, host verify)   |
   |<-- Server capabilities ---------|
   |                                  |
   |--- command: receive-pack / ---->|
   |<-- packfile (site content) -----|
   |                                  |
   |--- command: api-call POST /x -->|
   |<-- JSON response ---------------|
   |                                  |
   |--- disconnect ----------------->|
```

Port `22443` is proposed as the default — a concatenation of SSH's port 22 and HTTPS's port 443, signaling the protocol's nature as "SSH for the web." This avoids conflicts with standard SSH (22) and its common alternative (22222). Servers MAY use any port. Discovery uses DNS SRV records:

```
_ssh-web._tcp.example.com. 300 IN SRV 10 1 22443 example.com.
```

### 2.2 Authentication Modes

SSH-Web supports three authentication tiers:

| Mode | Mechanism | Use Case |
|------|-----------|----------|
| **Anonymous** | No client key presented | Public content browsing |
| **Identified** | Client presents pubkey, server verifies | Personalized access, comments, voting |
| **Trusted** | Client key in server's authorized set | Admin, content management |

Anonymous mode is the default. The server MUST allow anonymous access to at least the `capabilities` and `receive-pack` commands unless the entire site is private. The SSH handshake still occurs (the channel is always encrypted), but the client authentication step is skipped.

Users MAY present a public key to identify themselves across sessions without passwords, OAuth tokens, or cookies. This key is the user's portable identity.

### 2.3 First Visit & Trust

On first connection to an unknown server, the browser uses Trust On First Use (TOFU), identical to SSH's `known_hosts` model:

```
The authenticity of host 'news.ycombinator.com (93.184.216.34)'
can't be established.

ED25519 key fingerprint is SHA256:xK3j9Df...

Connect and add to known hosts?
[Always] [Once] [Cancel]
```

This eliminates the entire Certificate Authority infrastructure. No more paying for certificates, no more Let's Encrypt renewal cron jobs, no more CA compromises affecting millions of sites. The trust relationship is direct: user <-> server.

For high-security contexts, servers MAY publish their host key fingerprints via DNS (SSHFP records, RFC 4255) or out-of-band verification channels.

---

## 3. Server Component: `sshttpd`

### 3.1 Overview

`sshttpd` is the server-side daemon that handles SSH-Web connections. It is NOT a general-purpose SSH server. It exposes a constrained set of commands per virtual host, handles resource proxying and caching, and enforces access control per public key.

Conceptually, it fills the same role as Caddy, Nginx, or Traefik — but for the SSH-Web protocol.

### 3.2 Configuration

```
# /etc/sshttpd/config

site news.ycombinator.com {
    port 22443
    host-key /etc/sshttpd/keys/hn_ed25519

    # Content commands
    commands {
        receive-pack /                      # full site packfile
        receive-pack /item/{id}             # single item page
        api-call GET  /api/items            # list items
        api-call POST /api/items            # submit (requires auth)
        api-call POST /api/vote             # vote (requires auth)
    }

    # Discovery & metadata
    meta {
        rss-feed    /feeds/front   format=atom
        rss-feed    /feeds/new     format=atom
        rss-feed    /feeds/ask     format=atom
        sitemap     /sitemap       dynamic=true
        robots      crawl-delay=10 allow=["/", "/item/*"]
        search      params=[q, page, sort]
    }

    # MCP-compatible tool interface
    mcp {
        tool submit_story   { params: [title, url, text] }
        tool upvote         { params: [item_id] }
        tool comment        { params: [item_id, text, parent_id?] }
        tool flag           { params: [item_id, reason] }
    }

    # External resource proxying
    proxy-cache {
        allow fonts.googleapis.com
        allow cdn.imgur.com
        allow cdnjs.cloudflare.com
        deny  *
        ttl 24h
        max-size 2GB
        storage /var/cache/sshttpd/hn/
    }

    # Access control
    auth {
        anonymous   [receive-pack, api-call GET, rss-feed, sitemap, robots, search]
        identified  [comment, upvote, submit_story]
        trusted     [flag, admin-*]
    }

    # Rate limiting
    limits {
        anonymous   60/min
        identified  300/min
        trusted     unlimited
    }
}
```

### 3.3 Virtual Hosting

A single `sshttpd` instance MAY serve multiple sites, differentiated by the hostname provided during connection. The client includes the target hostname in the SSH connection (analogous to SNI in TLS):

```
ssh -p 22443 news.ycombinator.com    # hostname determines which site config
```

### 3.4 Content Delivery via Packfiles

Static site content is served using Git's packfile format (documented in Git's technical docs). This provides:

- **Delta compression**: Resources that share common content (HTML templates, repeated CSS) are delta-compressed automatically
- **Single-stream transfer**: The entire site (or requested subset) arrives as one binary stream
- **Integrity verification**: Every object is SHA-addressed, content corruption is detected
- **Incremental updates**: On subsequent visits, the browser sends its current object hashes and the server sends only deltas (like `git pull`)

```
# First visit — full packfile
client: receive-pack /
server: PACK [header, objects...]    # entire site

# Return visit — incremental
client: receive-pack / --have abc123,def456,ghi789
server: PACK [delta objects only]    # only what changed
```

This is dramatically more efficient than HTTP for repeat visits. Instead of the browser checking `If-None-Match` / `ETag` for every individual resource, the server computes the minimal diff of the entire site in one pass.

---

## 4. Browser Architecture

### 4.1 Rendering Pipeline

```
SSH Connect
    |
Capabilities Discovery
    |
receive-pack (packfile)
    |
Unpack to Volatile Sandbox
    |
Parse & Render (HTML/CSS/JS)
    |
Dynamic interactions via api-call commands
    |
External resources via proxy-call
    |
Disconnect (or keep-alive for active sessions)
```

The browser unpacks received content into a sandboxed volatile filesystem (in-memory or tmpfs). JavaScript executes within this sandbox with no direct network access — all external communication goes through SSH commands to the connected server.

### 4.2 Navigation Model

Navigating to a link on the same site reuses the existing SSH connection:

```
# User clicks /item/12345
client: receive-pack /item/12345
server: PACK [page content]
```

Cross-origin navigation (links to a different host) opens a new SSH connection:

```
# User clicks link to docs.example.com
client: [new SSH connection to docs.example.com:22443]
client: capabilities
client: receive-pack /
```

### 4.3 External Resource Proxying

When the rendered page references external resources (fonts, images, scripts), the browser does NOT contact those origins directly. Instead, it requests them through the current server:

```
# Page references https://fonts.googleapis.com/css?family=Roboto
client: proxy-call GET https://fonts.googleapis.com/css?family=Roboto

# Page includes an image from imgur
client: proxy-call GET https://cdn.imgur.com/a/example.png

# Streaming support for large assets
client: proxy-call GET https://cdn.example.com/video.mp4 --stream
```

The server fetches, caches, and forwards these resources. The third-party origin never sees the client's identity.

**Proxy allowlisting is mandatory.** The server's `proxy-cache.allow` list defines which external origins it will proxy. This is a server-enforced Content Security Policy with no client-side bypasses.

### 4.4 DevTools: Server Tab

Because the transport is SSH, the browser naturally exposes a "Server" tab in developer tools — a constrained terminal connected to the server's exposed command set:

```
+--------------------------------------------------+
| DevTools — Server [news.ycombinator.com:22443]   |
+--------------------------------------------------+
| > capabilities                                   |
| { "commands": [...], "mcp": [...], ... }         |
|                                                  |
| > api-call GET /api/items?page=2                 |
| { "items": [...] }                               |
|                                                  |
| > search q="ssh protocol"                        |
| { "results": [...] }                             |
|                                                  |
| > rss-feed /feeds/front                          |
| <feed xmlns="http://www.w3.org/2005/Atom">...    |
|                                                  |
| [Tab-complete enabled for server commands]        |
+--------------------------------------------------+
```

Developers can inspect, debug, and interact with the server directly — no curl, no Postman, no separate API client needed.

---

## 5. Capabilities Discovery & Service Manifest

### 5.1 The Capabilities Command

The first command a client issues after connection is `capabilities`. The server responds with a structured JSON manifest describing everything the site offers:

```json
{
  "protocol": "ssh-web/0.1",
  "site": {
    "name": "Hacker News",
    "host": "news.ycombinator.com",
    "description": "Social news for hackers"
  },
  "commands": {
    "receive-pack": {
      "description": "Fetch site content as packfile",
      "routes": ["/", "/item/{id}", "/user/{username}"],
      "supports": ["delta", "incremental", "subset"]
    },
    "api-call": {
      "description": "Dynamic API endpoints",
      "methods": ["GET", "POST"],
      "routes": {
        "GET /api/items": { "params": ["page", "sort"], "auth": "anonymous" },
        "POST /api/items": { "params": ["title", "url", "text"], "auth": "identified" },
        "POST /api/vote": { "params": ["item_id", "direction"], "auth": "identified" }
      }
    },
    "proxy-call": {
      "description": "Fetch external resources through server proxy",
      "allowed-origins": [
        "fonts.googleapis.com",
        "cdn.imgur.com",
        "cdnjs.cloudflare.com"
      ],
      "supports": ["streaming", "caching"]
    },
    "search": {
      "description": "Full-text search",
      "params": ["q", "page", "sort", "date_range"],
      "auth": "anonymous"
    }
  },
  "feeds": {
    "front": { "format": "atom", "path": "/feeds/front" },
    "new": { "format": "atom", "path": "/feeds/new" },
    "ask": { "format": "atom", "path": "/feeds/ask" }
  },
  "sitemap": {
    "dynamic": true,
    "path": "/sitemap"
  },
  "robots": {
    "crawl-delay": 10,
    "allowed-paths": ["/", "/item/*"],
    "blocked-paths": ["/admin/*"],
    "policy-per-key": true
  },
  "mcp": {
    "version": "1.0",
    "tools": [
      {
        "name": "submit_story",
        "description": "Submit a new story to Hacker News",
        "params": {
          "title": { "type": "string", "required": true },
          "url": { "type": "string", "required": false },
          "text": { "type": "string", "required": false }
        },
        "auth": "identified"
      },
      {
        "name": "upvote",
        "description": "Upvote an item",
        "params": {
          "item_id": { "type": "integer", "required": true }
        },
        "auth": "identified"
      },
      {
        "name": "comment",
        "description": "Post a comment on an item",
        "params": {
          "item_id": { "type": "integer", "required": true },
          "text": { "type": "string", "required": true },
          "parent_id": { "type": "integer", "required": false }
        },
        "auth": "identified"
      }
    ]
  },
  "auth": {
    "modes": ["anonymous", "identified", "trusted"],
    "key-types": ["ed25519", "ecdsa-sha2-nistp256"],
    "registration": "open"
  }
}
```

### 5.2 Implications

This single manifest replaces:

| HTTP-era artifact | SSH-Web equivalent |
|---|---|
| `robots.txt` | `capabilities -> robots` (structured, authenticated, per-key policies) |
| RSS `<link>` discovery | `capabilities -> feeds` (always present, always discoverable) |
| `sitemap.xml` | `capabilities -> sitemap` (structured, on-demand) |
| OpenAPI / Swagger spec | `capabilities -> commands` (always accurate, always current) |
| MCP server discovery | `capabilities -> mcp` (native, no separate endpoint) |
| `.well-known/*` files | Eliminated — everything is in the manifest |
| CORS headers | Eliminated — proxy allowlist is server-enforced |
| CSP headers | Eliminated — the sandbox + proxy model replaces it |
| `<meta>` tag soup | `capabilities -> site` metadata |

---

## 6. User-Side Customization

### 6.1 Client Commands

Users can define local command aliases and scripts in their SSH-Web client configuration:

```bash
# ~/.config/ssh-web/commands.conf

# Content preferences
alias readability   => receive-pack {path} --strip=js,css,tracking
alias offline       => receive-pack {path} --format=packfile --save=~/.cache/ssh-web/{host}/
alias dark          => receive-pack {path} --theme=dark
alias minimal       => receive-pack {path} --strip=images,fonts,js

# Feed management
alias subscribe     => rss-feed {path} | pipe-to ~/.config/ssh-web/feeds/{host}.atom
alias notify        => rss-feed {path} --watch --interval=5m | pipe-to ntfy

# Export & integration
alias export-pdf    => receive-pack {path} | render-to-pdf ~/.exports/{host}_{date}.pdf
alias bookmark      => api-call GET {path} --extract=title,url >> ~/.config/ssh-web/bookmarks.json
alias share         => api-call GET {path} --extract=title,url | pipe-to clipboard

# Search
alias deep-search   => search {q} --sort=relevance --date_range=all

# MCP bridge — pipe server MCP tools to local AI agent
alias ask-ai        => mcp {tool} | pipe-to claude-cli
```

### 6.2 Per-Site Overrides

```bash
# ~/.config/ssh-web/sites/news.ycombinator.com.conf

# Always use this key for HN
identity ~/.ssh/hn_ed25519

# Custom commands for this site
alias frontpage  => receive-pack / --subset=top30
alias my-threads => api-call GET /api/items?user=me&type=comment
alias karma      => api-call GET /api/user/me --extract=karma

# Auto-refresh
watch frontpage --interval=10m --notify-on-change
```

### 6.3 Portable Identity

The user's SSH keypair is their identity. It can be:

- **Backed up** to encrypted storage
- **Synced** across devices via any secure channel (USB, air-gapped transfer, encrypted cloud)
- **Revoked** by removing the pubkey from servers (or servers can check against a revocation list)
- **Scoped** — different keys for different contexts (one for news sites, one for work, one for anonymous browsing)
- **Hardware-bound** — stored on a YubiKey, Ledger, or TPM for maximum security

No password manager needed. No "forgot password" flows. No email verification loops. No SMS 2FA (which is broken anyway). Your key is your identity.

---

## 7. Privacy Architecture

### 7.1 Structural Privacy

SSH-Web provides privacy as a structural property of the protocol, not as an opt-in feature:

| Threat | HTTP/S | SSH-Web |
|--------|--------|---------|
| Third-party tracking pixels | Client fetches directly, tracker sees user IP + fingerprint | Server proxies the resource, tracker sees server IP only |
| Cross-site identity correlation | Cookies, fingerprinting, "Sign in with Google" | Different keys per site context, no cross-site correlation |
| DNS-level surveillance | ISP sees every domain you visit | ISP sees only the IP of the server you connect to |
| Man-in-the-middle | Relies on CA trust chain (compromisable) | Direct key verification (TOFU or SSHFP) |
| Browser fingerprinting | Massive fingerprint surface (canvas, WebGL, fonts, plugins) | Minimal client identification — SSH version string only |
| Cookie-based tracking | Pervasive, hard to block completely | No cookies exist in the protocol |
| Ad network behavioral profiling | Trackers embedded in page JS | No direct third-party connections, JS sandboxed |

### 7.2 The Surveillance Capitalism Blocker

The current web's business model depends on a specific technical architecture: the browser must contact third-party origins directly so those origins can identify and track the user. SSH-Web makes this architecturally impossible.

A server operator who includes `tracking.facebook.com` in their proxy allowlist is making an explicit, auditable choice — and the tracker still only sees the server, not the user. The economic incentive for behavioral advertising collapses when the data pipeline is structurally broken.

---

## 8. MCP Integration: The Website Is the API Is the Agent Interface

### 8.1 Unified Interface

In the HTTP world, a service typically maintains:

1. A website (HTML for browsers)
2. A REST/GraphQL API (JSON for developers)
3. An OpenAPI spec (documentation for the API)
4. An MCP server (tool interface for AI agents)

These are four separate systems that must be kept in sync. In SSH-Web, they are one:

```
Browser   -> connects via SSH -> runs `receive-pack /`     -> gets HTML
Developer -> connects via SSH -> runs `api-call GET /data`  -> gets JSON
AI Agent  -> connects via SSH -> runs `capabilities`         -> discovers MCP tools
Crawler   -> connects via SSH -> runs `sitemap`              -> gets site structure
```

The `capabilities` manifest is the single source of truth. If a tool exists in the manifest, it works. If it's not there, it doesn't exist. No stale documentation, no versioning mismatches, no "the API docs say X but the server does Y."

### 8.2 Agent Authentication

AI agents authenticate the same way users do — with a keypair. A server can:

- Grant agents read-only access (anonymous tier)
- Require agent identification (identified tier) with rate limits
- Issue API keys as SSH keys with specific permissions (trusted tier)
- Revoke agent access instantly by removing their pubkey

The `robots` command provides per-key crawl policies:

```
# In the capabilities manifest
"robots": {
    "default": { "crawl-delay": 10, "allowed": ["/", "/item/*"] },
    "agents": {
        "key:SHA256:abc123...": { "crawl-delay": 0, "allowed": ["*"], "label": "Anthropic Claude" },
        "key:SHA256:def456...": { "crawl-delay": 60, "allowed": ["/"], "label": "Unknown Bot" }
    }
}
```

This replaces the honor-system `robots.txt` with cryptographically authenticated, enforceable access control.

---

## 9. Caching Architecture

### 9.1 Server-Side Proxy Cache

The `sshttpd` proxy cache operates as a local CDN:

```
First request for fonts.googleapis.com/roboto.woff2:
  -> sshttpd fetches from origin
  -> Stores in /var/cache/sshttpd/{site}/proxy/
  -> Serves to client through SSH pipe

Subsequent requests (any client):
  -> Served from local cache
  -> Origin never contacted
  -> Client never contacts origin
```

Cache eviction follows standard policies (TTL, LRU, max-size) configured per-site.

### 9.2 Client-Side Content Cache

The packfile model enables efficient client-side caching:

```
~/.cache/ssh-web/
  news.ycombinator.com/
    objects/          # Git-style object store
      ab/cd1234...   # HTML, CSS, JS as content-addressed objects
      ef/gh5678...
    refs/            # Latest known state
      HEAD           # Points to current site version
    host-key         # Server's public key (known_hosts equivalent)
  example.com/
    ...
```

On return visits, the client sends its known object hashes and receives only deltas. For a site that changed one article since the last visit, the transfer might be a few kilobytes instead of a full page load.

---

## 10. Migration Path

### 10.1 Dual-Stack Deployment

Adoption does not require a flag day. Sites can run both HTTP and SSH-Web simultaneously:

```
                    +---------------+
Browser (legacy) -->|  Caddy/Nginx  |--> Application
                    |  (port 443)   |
                    +---------------+

Browser (SSH-Web) ->|   sshttpd     |--> Application
                    |  (port 22443) |
                    +---------------+
```

The application backend doesn't change. `sshttpd` translates SSH-Web commands into the same backend calls that Caddy would make.

### 10.2 Discovery

SSH-Web-capable sites advertise support via DNS SRV records. Browsers that support the protocol check for SRV records and prefer SSH-Web when available:

```
_ssh-web._tcp.example.com. 300 IN SRV 10 1 22443 example.com.
```

Browsers without support simply ignore the record and use HTTPS as usual.

### 10.3 Fallback

If an SSH-Web connection fails (firewall blocking port 22443, server misconfiguration), the browser falls back to HTTPS transparently. The user experience is uninterrupted.

---

## 11. Security Considerations

### 11.1 Command Injection

`sshttpd` MUST NOT be a general-purpose shell. It is a command whitelist executor. Any input not matching a defined command pattern is rejected. There is no shell interpretation, no path traversal, no command chaining.

```
# VALID
receive-pack /item/12345
api-call GET /api/items?page=2

# REJECTED (not in command whitelist)
ls /etc/passwd
receive-pack /; rm -rf /
api-call GET /api/../../etc/shadow
```

### 11.2 Resource Exhaustion

Rate limiting is enforced per authentication tier. Anonymous connections get the lowest limits. Identified users get higher limits. Trusted users may have unlimited access.

Connection limits, bandwidth throttling, and concurrent session caps are configurable per-site.

### 11.3 Proxy Abuse

The `proxy-call` mechanism could be abused to turn the server into an open proxy. Mitigations:

- **Strict allowlisting**: Only explicitly configured origins are proxied
- **No user-specified origins**: The allowlist is server-side only, clients cannot request arbitrary URLs
- **Rate limiting**: Proxy requests count against the client's rate limit
- **Size limits**: Maximum response size for proxied resources

### 11.4 Key Management

Users are responsible for their own key security. The protocol supports:

- **Key rotation**: Servers can accept multiple keys per user during transition periods
- **Key revocation**: Via server-side removal or optional revocation list (OCSP-like, but simpler)
- **Hardware keys**: YubiKey, TPM, Secure Enclave storage recommended for high-security use

---

## 12. Comparison

| Aspect | HTTP/S (current web) | SSH-Web (proposed) |
|--------|---------------------|--------------------|
| Encryption | Opt-in (TLS), requires CA | Always-on, direct key exchange |
| Identity | Third-party (Google, Apple, OAuth) | Self-sovereign keypair |
| Session state | Cookies + server-side stores | Connection = session, key = identity |
| Content transfer | Per-resource requests | Packfile (delta-compressed, incremental) |
| Third-party resources | Client fetches directly | Server proxies and caches |
| API discovery | Separate OpenAPI/Swagger docs | Built into capabilities manifest |
| Feed discovery | Hunt for `<link rel="alternate">` | Always in manifest |
| Robots/crawl policy | Honor-system plaintext file | Authenticated, per-key, enforceable |
| Cross-origin policy | CORS headers (complex, error-prone) | Server-enforced proxy allowlist |
| Content Security | CSP headers (complex, bypassable) | Sandboxed execution, no direct network |
| Caching | CDN + Cache-Control headers | Server-side proxy cache + packfile deltas |
| DevTools | Network tab (passive observation) | Server tab (active command interface) |
| AI/Agent access | Separate MCP server or API | Native via capabilities manifest |
| Privacy | Requires extensions, VPNs, discipline | Structural — built into the protocol |

---

## 13. Open Questions

1. **WebSocket equivalent**: Long-lived bidirectional streams (chat, real-time updates) map naturally to SSH channels, but the framing semantics need definition.

2. **Media streaming**: Large media files (video, audio) need a streaming `proxy-call` variant with range request support and adaptive bitrate semantics.

3. **Offline-first**: The packfile model naturally supports offline use (the entire site is cached locally), but sync conflict resolution for dynamic content needs design.

4. **DNS independence**: Could SSH-Web use an alternative discovery mechanism to avoid DNS dependency? Tor-style .onion addresses? Distributed hash tables?

5. **Key UX**: Making SSH key management accessible to non-technical users is critical. Hardware key support, biometric unlocking, and platform keychain integration need design attention.

6. **Server-side rendering vs. client-side**: Should `sshttpd` support server-side rendering natively, sending pre-rendered content in the packfile? This could further reduce client complexity.

7. **Multiplayer / collaborative**: Real-time collaborative features (Google Docs-style) over SSH channels need multiplexing and conflict resolution semantics.

---

## 14. Reference Implementation Roadmap

### Phase 1: Proof of Concept
- `sshttpd` daemon (Go or Rust) serving static sites via packfile
- Modified terminal client that can render HTML from packfiles
- Basic `capabilities` manifest support

### Phase 2: Browser Extension
- Chrome/Firefox extension that intercepts SSH-Web URLs (`ssh-web://`)
- Translates SSH-Web commands to rendered content in the browser
- DevTools "Server" tab integration

### Phase 3: Standalone Browser
- Minimal browser built on a rendering engine (Servo, WebKit) with native SSH-Web transport
- Full capabilities discovery, proxy-call, MCP integration
- User key management UI

### Phase 4: Ecosystem
- `sshttpd` plugins for popular frameworks (Next.js, Rails, Django)
- WordPress / Ghost / Hugo adapters
- Package registries serving packages via SSH-Web

---

## Appendix A: Wire Protocol Examples

### A.1 Anonymous Static Site Visit

```
C: [TCP connect to example.com:22443]
C: [SSH handshake — client skips authentication]
S: [SSH handshake accepted, anonymous session]
C: capabilities
S: {"protocol":"ssh-web/0.1","commands":{"receive-pack":{"routes":["/"]}}, ...}
C: receive-pack /
S: PACK\x00\x00\x00\x02 [packfile header] [compressed objects...]
C: [unpack, render HTML/CSS/JS in sandbox]
C: [disconnect]
```

### A.2 Authenticated API Interaction

```
C: [TCP connect to api.example.com:22443]
C: [SSH handshake — client presents ed25519 pubkey]
S: [SSH handshake accepted, identified session]
C: capabilities
S: {"commands":{"api-call":{...},"mcp":{...}}, "auth":{"current":"identified"}}
C: api-call POST /api/comment {"item_id": 42, "text": "Great post!"}
S: {"status": "created", "comment_id": 1337}
C: [disconnect]
```

### A.3 Proxied External Resource

```
C: proxy-call GET https://fonts.googleapis.com/css2?family=Inter
S: [sshttpd checks allowlist — fonts.googleapis.com is allowed]
S: [sshttpd checks cache — miss, fetches from origin]
S: [sshttpd caches response, returns to client]
S: @font-face { font-family: 'Inter'; ... }
```

---

## Appendix B: Relationship to Existing Protocols

| Protocol | Relationship to SSH-Web |
|----------|------------------------|
| SSH (RFC 4253) | Transport layer — SSH-Web is built on SSH |
| Git protocol | Inspiration for packfile content delivery |
| HTTP/3 + QUIC | Solves similar problems (multiplexing, connection migration) but retains HTTP semantics |
| Gemini | Similar philosophy (simplicity, privacy) but lacks SSH-Web's dynamic capabilities |
| MCP | Directly integrated — SSH-Web servers are native MCP servers |
| Tor | Complementary — SSH-Web could run over Tor for additional anonymity |
| IPFS | Alternative content-addressing approach — SSH-Web uses Git's model instead |
| Nostr | Shares the keypair identity model — potential interoperability |

---

## License

This specification is released under Apache-2.0 — see [LICENSE](../LICENSE).

---

*"The best protocol is the one that makes the right thing the default thing."*
