# JavaScript API Sandbox

## Overview

When a page is served from an `ssh-web://` origin, every JavaScript network API is intercepted and routed through `LibSSHWebClient` instead of the open internet. APIs that would leak the user's identity are disabled entirely. A `window.sshweb` global provides native SSH-Web semantics for opt-in pages.

## Network API Interception

| Standard API | Behavior on `ssh-web://` origin |
|---|---|
| `fetch('/path')` (same-origin) | → `api-call <METHOD> /path` on the origin's SSH connection |
| `fetch('https://allowed.cdn/x')` | → `proxy-call GET https://allowed.cdn/x` if origin is in server's `proxy-cache.allow` |
| `fetch('https://other.com/x')` (not allowlisted) | Rejected with `TypeError: blocked by SSH-Web proxy policy` |
| `XMLHttpRequest` | Same translation as `fetch` |
| `WebSocket` | → long-lived SSH channel via `subsystem ws /path` |
| `EventSource` | → SSH channel streaming `text/event-stream` |
| `navigator.sendBeacon` | → fire-and-forget `api-call POST /path` |
| `Image.src`, `<link>`, `<script src>`, `<iframe src>` | Same loader path: same-origin via `receive-pack`/`api-call`, allowlisted external via `proxy-call`, others blocked |

### Implementation

Origin-conditional dispatch in `LibWeb/Fetch/Fetching/Fetching.cpp`: if the document's origin scheme is `ssh-web://`, route through `sshweb_fetch()` instead of the normal HTTP path.

`sshweb_fetch()` logic:
1. Same-origin URL → dispatch as `api-call <METHOD> <path>` via `LibSSHWebClient`
2. External URL → check manifest's `proxy-cache.allow` list; allowed → `proxy-call GET <url>`; denied → reject with TypeError

Each API entry point (WebSocket, EventSource, sendBeacon, image/script/iframe loaders) gets the same pattern: origin check → SSH-Web dispatch or fallthrough to existing behavior.

## Disabled APIs

These APIs would leak the user's IP or identity outside the SSH tunnel:

| API | Behavior on `ssh-web://` |
|---|---|
| `WebRTC` (`RTCPeerConnection`) | Constructor throws `SecurityError` |
| `navigator.geolocation.*` | Methods reject with `SecurityError` |
| `navigator.mediaDevices.*` | Methods reject with `SecurityError` |
| `Service Workers` | `navigator.serviceWorker.register()` rejects |

Each is a 1-3 line origin check at the binding entry point.

## Cookie & Storage Semantics

- `document.cookie` getter: returns `""` on `ssh-web://` origins
- `document.cookie` setter: silent no-op (no throw — page code shouldn't break)
- `localStorage`, `sessionStorage`, `IndexedDB`: work normally, partitioned per origin (no third-party iframes possible, so storage can't be used for cross-site tracking)

## `window.sshweb` Native API

Always present on `ssh-web://` origins:

```javascript
window.sshweb.capabilities()                       // → Promise<CapabilitiesManifest>
window.sshweb.apiCall(method, path, body?, opts?)   // → Promise<Response>
window.sshweb.proxyCall(url, opts?)                 // → Promise<Response>
window.sshweb.openChannel(name, args?)              // → Promise<SSHChannel> (full duplex)
window.sshweb.mcp.list()                            // → Promise<Tool[]>
window.sshweb.mcp.invoke(toolName, params)          // → Promise<any>
window.sshweb.identity                              // → { fingerprint, label } | null
window.sshweb.requestIdentity()                     // → Promise<Identity>
```

Implemented via `LibWeb/SSHWeb/SSHWeb.idl` using Ladybird's IDL bindings generator. Methods route through `LibSSHWebClient::Client`. Promises resolve via the standard `JS::Promise` + `LibCore` deferred call pattern.

## Cross-Origin Policy

- `ssh-web://a.com` cannot load resources from `ssh-web://b.com`. Cross-origin navigation opens a fresh SSH connection per spec.
- `ssh-web://` pages cannot load `https://` resources directly — everything goes through `proxy-call`.
- `https://` pages cannot access `ssh-web://` resources (no fetch, no iframe).
