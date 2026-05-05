# Browser Architecture

Companion to: [SSH-WEB-SPEC.md](../../spec/SSH-WEB-SPEC.md)

## Overview

The SSH-Web browser is a hard fork of [Ladybird](https://ladybird.org) that natively speaks the SSH-Web protocol. It connects to `sshttpd` (and any future SSH-Web server) over SSH instead of HTTP, renders the returned content, and enforces the protocol's structural privacy guarantees in the renderer.

Ladybird is the right base because it shares the "start clean" philosophy, its multi-process architecture has a well-defined network boundary, and its pre-alpha state welcomes novel transport experiments. The fork is hard (we own `master`), rebased monthly against upstream Ladybird.

## Goals

- Prove SSH-Web works end-to-end with a real rendering engine.
- Enforce privacy guarantees structurally — pages on `ssh-web://` origins cannot reach third-party servers directly, regardless of what their JavaScript does.
- First-class developer ergonomics: a "Server" DevTools tab exposing the SSH command surface, an MCP tool runner, an identity keystore with per-site key selection.
- Coexist safely with HTTPS — no silent transitions, no downgrade attacks, clear address-bar indicators.

## Non-Goals

- Replacing Ladybird's HTTPS support.
- Production-grade Windows support in the first release.
- An in-tree SSH library (we use libssh2).
- OS keychain or hardware-key (YubiKey/TPM) integration in the first release.
- Fuzzing infrastructure, auto-update.

## Process Model

Ladybird runs: `Browser` (UI) → `WebContent` (renderer) → `RequestServer` (network) → helpers. We add one process:

```
┌──────────────┐
│   Browser    │  UI: address bar, tabs, dialogs, key manager, Server DevTools tab
└──────┬───────┘
       │ IPC
   ┌───┴───────────────┬──────────────┬──────────────────┐
   │                   │              │                  │
┌──┴────────┐  ┌───────┴──────┐  ┌────┴────────┐  ┌─────┴─────────┐
│ WebContent │  │ RequestServer│  │ ImageDecoder│  │ SSHWebServer  │  ← NEW
│ (renderer) │  │  (HTTP/S)    │  │             │  │  (SSH-Web)    │
└──┬─────────┘  └──────────────┘  └─────────────┘  └───────┬───────┘
   │                                                        │
   │              IPC for ssh-web:// resources              │
   └────────────────────────────────────────────────────────┘
```

### SSHWebServer (new process)

Lives at `Services/SSHWebServer/`. Owns all SSH-Web state:

- **Connection pool** keyed by `(host, port, identity)`. Connections are long-lived and reused across tabs to the same origin.
- **Host-key store** at `~/.config/sshweb/known_hosts/`. TOFU prompts proxied through Browser UI via IPC.
- **Identity keystore** at `~/.config/sshweb/identities/`. Stores `ed25519` keypairs and per-host identity assignments.
- **Capabilities cache** per `(host, identity)`, refreshed on TTL or explicit reload.
- **Proxy-call cache** for external resources fetched via `proxy-call`.

Separate process = separate security boundary. A `RequestServer` crash cannot leak SSH key material, and SSH-Web's stateful semantics don't contaminate `RequestServer`'s stateless model.

### WebContent Modifications

- URL scheme handler for `ssh-web://`. Resource loads on `ssh-web://` origins route to `SSHWebServer` via IPC instead of `RequestServer`.
- Standard network APIs intercepted and rewritten on `ssh-web://` origins (see [js-sandbox.md](js-sandbox.md)).
- `window.sshweb` global exposed on `ssh-web://` origins.

### Browser (UI) Modifications

- "Identities" settings pane backed by the `SSHWebServer` keystore.
- TOFU dialog, HTTPS fallback warning dialog, identity selector.
- "Server" DevTools tab.

### LibSSHWeb (shared library)

At `Libraries/LibSSHWeb/`. Shared types: URL parsing, manifest schema, IPC message definitions, error types, version constants.

Key types:
- `SSHWeb::URL` — parses `ssh-web://host[:port][/path]`
- `SSHWeb::CapabilitiesManifest` — the server's capabilities JSON schema
- `SSHWeb::HostKey` — key type + SHA-256 fingerprint
- `SSHWeb::KnownHostEntry` — host key + first-seen timestamp
- Error vocabulary enum (`SSHWebError`)

### LibSSHWebClient (client library)

At `Libraries/LibSSHWebClient/`. Used by `WebContent` to talk to `SSHWebServer` over IPC. Mirrors how `LibRequests` wraps `RequestServer`.

## Repository Layout

```
ladybird/                                (forked from LadybirdBrowser/ladybird)
├── Services/
│   ├── RequestServer/                   (upstream, unchanged)
│   ├── WebContent/                      (upstream + ssh-web URL handler patches)
│   └── SSHWebServer/                    ← NEW
│       ├── main.cpp
│       ├── Service.{h,cpp}              (entry: client mode or IPC service mode)
│       ├── Connection.{h,cpp}           (libssh2 wrapper, TOFU, command execution)
│       ├── KnownHosts.{h,cpp}           (TOFU store)
│       ├── ClientMode.{h,cpp}           (one-shot CLI client)
│       ├── ConnectionFromClient.{h,cpp} (per-WebContent IPC handler)
│       ├── ServiceMode.{h,cpp}          (long-lived IPC service)
│       └── *.ipc                        (IPC definitions)
├── Libraries/
│   ├── LibWeb/                          (upstream + sshweb bindings, fetch interception)
│   ├── LibSSHWeb/                       ← NEW (shared types)
│   └── LibSSHWebClient/                 ← NEW (IPC client for WebContent)
├── UI/                                  (upstream + Identities pane, TOFU dialog, Server DevTools tab)
├── Meta/                                (upstream + LADYBIRD_ENABLE_SSHWEB CMake option)
└── Tests/                               (upstream + LibSSHWeb, SSHWebServer tests)
```

## Build

**libssh2** added to `vcpkg.json`, matching Ladybird's dependency pattern.

**Build flag:** `LADYBIRD_ENABLE_SSHWEB=ON` (default ON in our fork). Disabling reverts to vanilla Ladybird — useful for bisecting upstream issues.

**Platforms:** macOS and Linux. Windows is a follow-up.

**Versioning:** Tracks SSH-Web protocol version, not Ladybird's. Initial release `0.1.0` matching `ssh-web/0.1`.

**Upstream rebase:** Monthly. CI runs upstream's test suite on every rebase.

## URL Scheme

**Scheme:** `ssh-web://host[:port][/path]`. Default port `22443`.

**Bare host resolution:** A host typed without a scheme triggers DNS SRV lookup for `_ssh-web._tcp.<host>`. If reachable, resolves to `ssh-web://`. Otherwise falls to `https://`.

**Fallback flow:** When an `ssh-web://` connection fails, a modal dialog appears — never silent fallback:

> "Could not connect to example.com over SSH-Web. The site may have moved, the server may be down, or this could be a downgrade attack. You can try HTTPS instead — but tracking, third-party requests, and CA-based trust will apply."
>
> [Try HTTPS] [Cancel]

## Identity Management

### Storage

`~/.config/sshweb/identities/` — dedicated keystore, owned by the browser:

```
~/.config/sshweb/identities/
  default/
    ed25519             (private key, mode 0600)
    ed25519.pub
    meta.json           (label, created date, hosts where used)
  work/
    ed25519
    ed25519.pub
    meta.json
```

### Per-Host Assignment

`~/.config/sshweb/sites/<host>.json`:

```json
{ "host": "news.ycombinator.com", "identity": "hn", "auto-identify": true }
```

`auto-identify: true` presents the identity automatically. `false` (default) prompts before identifying.

### Operations

- Generate new identity (ed25519, with label)
- Import existing OpenSSH-format private key
- Export public/private key
- Revoke/delete identity
- Assign identity to host
- Toggle auto-identify per host

## Testing Strategy

### Unit Tests (LibSSHWeb)

In-process, fast. URL parsing round-trips, manifest schema validation, IPC message serialization.

### Integration Tests (SSHWebServer)

Process-level with `sshttpd` as fixture on a random port:
- TOFU first-connection → persistence → reuse → rotation → mismatch detection
- Identity flows: anonymous → identified → trusted, with rate-limit assertions
- `receive-pack`, `api-call`, `proxy-call` with auth tier enforcement
- Connection pooling: two tabs to same origin share one SSH connection

### End-to-End Browser Tests

Headless browser against `ssh-web://localhost:NNNN` backed by `sshttpd` fixture:
- Render static site, assert DOM
- `fetch('/api/x')` routes through `api-call`
- `window.sshweb.mcp.invoke(...)` executes
- Cross-origin block, disallowed external fetch block
- HTTPS fallback dialog

## Open Questions

1. **WebSocket framing semantics.** `subsystem ws /path` opens a long-lived SSH channel, but the on-wire framing format needs definition.
2. **Service Worker design.** Disabled in MVP. Re-enabling requires designing mediation without breaking the sandbox.
3. **Multiplexed channels.** A page with many concurrent channels on one SSH connection — stress-test libssh2's channel multiplexing.
4. **Subresource integrity.** Packfile objects are SHA-addressed by Git's model, but integrating with HTML's `integrity="sha256-..."` needs a bridging spec.

## Follow-Up Work

1. WebSocket / streaming framing format on SSH channels
2. Hardware-key support (YubiKey, TPM, Secure Enclave)
3. OS keychain integration
4. Fuzzing infrastructure
5. Service Workers on `ssh-web://` origins
6. Windows platform support
7. Auto-update mechanism
8. In-tree `LibSSH` to remove libssh2 dependency
