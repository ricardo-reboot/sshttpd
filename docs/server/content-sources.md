# Content Source Modes

## Problem

When a site block sets `backend http://localhost:8000` without a `root` directive, `receive-pack` called `buildPackfile()` which immediately returned an error for missing content root. The browser received an error string instead of binary data and failed with "missing PACK signature".

The `backend` field was only wired for `api-call` and `mcp` — `receive-pack` had no backend branch.

## Design

`receive-pack` is smart about its content source. No new commands needed.

```
site localhost {
    port 22443
    host-key ./keys/host_ed25519

    # Filesystem mode: non-empty root → PACK binary response
    root ./examples/site

    # HTTP-backend mode: root absent, backend set → HTTP/1.1 wire response
    # backend http://localhost:8000

    commands { receive-pack / }
    auth { anonymous [receive-pack] }
}
```

### Mode Selection (`receivePackBinary`)

1. **`root` configured** → call `buildPackfile`. On success: write PACK bytes.
2. **`root` absent (or file not found) AND `backend` configured** → `GET <backend><path>`, write full HTTP/1.1 wire response (status line + CRLF headers + blank line + body). Hop-by-hop headers stripped; `Content-Length` and `Connection: close` set explicitly. Same wire shape as `proxy-call`.
3. **Neither** → error.

The text-mode `receivePack` wrapper (CLI tests) still requires `root`; backend fallback is binary-path only since that's what the browser hits.

## Changes

### `internal/commands/handler.go`
- Removed `httpFetch`/`httpFetchBinary` and `case "http-fetch"` branches
- Rewrote `receivePackBinary` with three-mode logic
- `capabilities()` no longer advertises `http-fetch`; `receive-pack` entry includes `"backend-fallback"` in its `supports` list

### `examples/backend.conf`
- `http-fetch /` replaced with `receive-pack /`
- Auth block references `receive-pack` instead of `http-fetch`

## Risks

| Risk | Severity | Notes |
|------|----------|-------|
| Large response bodies | Medium | Backend fallback reads full body into memory. Follow-up: use `io.Copy` streaming. |
| Backend unavailable/slow | Medium | 30s `http.Client` timeout holds the SSH channel open. |
| Auth header forwarding | Low now | Backend receives anonymous HTTP requests. `X-SSHWeb-Identity` forwarding is future work. |
