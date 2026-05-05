# Browser UI

## Overview

The browser chrome surfaces SSH-Web state through address bar indicators, native dialogs, and an Identities settings pane. Both macOS (AppKit) and Linux (Qt) toolkits get parallel implementations backed by shared types in `LibWebView/SSHWebPolicy`.

## Shared Types (`LibWebView::SSHWebPolicy`)

```cpp
struct SSHWebTOFURequest {
    String host;
    u16 port;
    String key_type;
    String fingerprint_sha256;
};

enum class SSHWebTOFUDecision { AcceptAndRemember, AcceptOnce, Reject };

struct SSHWebFallbackRequest {
    String original_url;       // ssh-web://...
    String fallback_url;       // https://...
    String error_explanation;  // "Connection refused", etc.
};

enum class SSHWebFallbackDecision { TryHTTPS, Cancel };
```

## TOFU Dialog

On first connection to an unknown host, a native modal displays:

- Host and port
- Key type and SHA-256 fingerprint
- Three actions: **Always** (add to known_hosts), **Once** (this session only), **Cancel**

The dialog bridges the synchronous TOFU callback in `Connection::open` with the asynchronous IPC flow: `SSHWebServer` sends `tofu_prompt` → `LibSSHWebClient` forwards to UI → user responds → `tofu_decision` sent back.

## HTTPS Fallback Dialog

When an `ssh-web://` connection fails:

> "Could not connect to {host} over SSH-Web. The site may have moved, the server may be down, or this could be a downgrade attack. You can try HTTPS instead — but tracking, third-party requests, and CA-based trust will apply."
>
> [Try HTTPS] [Cancel]

No silent fallback under any circumstance.

## Address Bar Safety Indicator

| State | Display |
|---|---|
| `ssh-web://` connected, host trusted | Green keyed-lock; click shows fingerprint popover |
| `https://` (no SSH-Web tried) | Standard neutral lock |
| `https://` (downgraded from SSH-Web) | Amber warning; tooltip "Downgraded from SSH-Web" |
| `http://` plaintext | Red (existing Ladybird behavior) |

### Lock Popover (ssh-web pages)

- Host fingerprint and key type
- Date first trusted
- "Forget this host" button (removes from known_hosts, next visit re-triggers TOFU)
- Identity in use (if any)
- "Switch identity..." button

## Identities Settings Pane

A new tab in the Settings window:

- **Table view** listing identities: label, fingerprint, hosts where used
- **Generate** — creates new ed25519 keypair with a label
- **Import** — file picker for OpenSSH private key, copies to identities dir with 0600 perms, validates via `ssh-keygen -y -f` round-trip
- **Export public key** — file save dialog
- **Export private key** — with confirmation warning
- **Delete** — with confirmation listing affected sites
- **Assign to host** — dialog to write per-host assignment file

## Per-Host Identity Selection

Connection-time identity resolution:

1. Look up `~/.config/sshweb/sites/<host>.json`
2. If `identity` set and `auto-identify: true` → use automatically
3. If `identity` set and `auto-identify: false` → prompt: [Use it] / [Anonymous] / [Pick another]
4. If no per-host file → anonymous; lock popover offers "Identify yourself"

The IPC `execute_command` carries an `Optional<ByteString> identity_label` parameter to pass the resolved identity to `SSHWebServer`.

## File Layout

```
UI/AppKit/
├── Application/SSHWebDialogs.{h,mm}     (TOFU + fallback dialogs)
├── Tab/LocationToolbarItem.mm            (address bar indicator)
├── Settings/IdentitiesPane.{h,mm}        (identities settings)
├── Settings/SettingsWindowController.mm   (wire pane in)

UI/Qt/
├── SSHWebDialogs.{h,cpp}                 (TOFU + fallback dialogs)
├── LocationEdit.cpp                       (address bar indicator)
├── IdentitiesWidget.{h,cpp}              (identities settings)
├── Settings.cpp                           (wire widget in)

Libraries/LibWebView/
├── SSHWebPolicy.{h,cpp}                  (shared types)
```
