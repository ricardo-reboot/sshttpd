# DevTools Extensions

## Overview

SSH-Web adds a new "Server" DevTools tab and extends the existing Network and Console tabs. The Server tab is only visible on `ssh-web://` pages.

## Server Tab

Five sub-tabs, backed by `Libraries/LibDevTools/SSHWebTab/`:

### Capabilities

Pretty-printed manifest with refresh button and raw-JSON toggle. Shows cache TTL and last-fetched time. Cached per origin (5 min default TTL).

### Commands (REPL)

Interactive command REPL with:
- Tab completion driven by the cached capabilities manifest
- History persisted per origin at `~/.config/sshweb/devtools-repl-history/<host>.json`
- Output rendered as JSON, hex, or text depending on response type
- `receive-pack` responses show packfile object listing rather than raw bytes

### MCP Tools

Table of tools from `capabilities.mcp.tools`. Each row has a "Run" button that opens an auto-generated form from the tool's parameter schema, executes via `sshweb.mcp.invoke`, and displays the result. Makes the browser a usable MCP client.

### Connection

Live SSH connection state:
- Cipher, MAC, keepalive
- Channel count, bytes in/out
- Rate limit headroom
- Current identity with "Switch identity" button (forces reconnect + page reload)

### Host Key

- Fingerprint, key type
- When first trusted
- SSHFP record verification status
- "Forget this host" button (removes from known_hosts, next visit re-triggers TOFU)

## Network Tab Extensions

SSH-Web operations appear as distinct row types alongside HTTP rows:

- SSH channel lifecycle (open, data, close) per resource
- Packfile transfer summaries (object count, bytes, delta vs full)
- `api-call` and `proxy-call` rows with method, path, duration, response size, cache-hit indicator
- Distinct icon + color for SSH-Web vs HTTP rows
- Filter toggle: All / HTTP / SSH-Web / Proxied
- Click detail pane: full command, response body (text/hex), channel lifecycle timestamps, identity used

## Console Tab

New "SSH-Web" event source filter alongside JavaScript/Network filters. Surfaces protocol events: connection open/close, command issued, errors.

## Permission Model

The Server tab respects the current page's auth tier:

- **Anonymous:** REPL can only run commands the manifest declares as `auth: anonymous`. Others grayed out.
- **Identified:** also gets `auth: identified` commands.
- **Trusted:** everything enabled.

The Connection sub-tab's "Switch identity" upgrades the tier and reloads the page.

## Architecture

Backend lives in `Libraries/LibDevTools/SSHWebTab/` — each sub-view is a small class wrapping `LibSSHWebClient` calls plus state management (caching, history). Frontend is HTML/CSS/TypeScript in the DevTools bundle, communicating with the backend over Ladybird's existing DevTools IPC.

```
Libraries/LibDevTools/SSHWebTab/
├── ServerTab.{h,cpp}           (tab coordinator)
├── CapabilitiesView.{h,cpp}    (manifest cache + refresh)
├── REPL.{h,cpp}                (history, completion)
├── MCPInvoker.{h,cpp}          (tool invocation)
├── ConnectionView.{h,cpp}      (live metrics)

Base/devtools/
├── server-tab.{html,css,ts}    (frontend)
├── network-tab.ts              (SSH channel row support)
```
