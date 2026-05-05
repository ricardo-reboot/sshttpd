# SSH Transport Layer

## Overview

`SSHWebServer` wraps libssh2 to provide SSH connectivity. The transport layer handles TCP connections, SSH handshakes, TOFU host-key verification, and command execution over SSH channels.

## Connection (`SSHWeb::Connection`)

Wraps a libssh2 session. Opens an SSH session to a host, performs TOFU host-key verification, and exposes `execute_command(name, args)` returning the response bytes.

### Connection Flow

1. TCP connect via `getaddrinfo` + `connect()`
2. `libssh2_session_init` + `libssh2_session_handshake`
3. Extract host key: type + SHA-256 fingerprint (base64-encoded)
4. TOFU check against `KnownHosts` store
5. Authentication (anonymous or publickey)
6. Command execution via `libssh2_channel_exec`

### Host Key Types

Supported via libssh2: `ed25519`, `ecdsa-sha2-nistp256`, `ecdsa-sha2-nistp384`, `ecdsa-sha2-nistp521`, `rsa`.

## TOFU Host-Key Store (`SSHWeb::KnownHosts`)

Persistent store at `~/.config/sshweb/known_hosts/`. One JSON file per `(host, port)`, named `<host>_<port>.json`:

```json
{
  "type": "ed25519",
  "fingerprint_sha256": "base64-encoded-sha256",
  "first_seen": "1714934400000"
}
```

### Verification Logic

- **Known host, matching fingerprint:** proceed silently.
- **Known host, different fingerprint:** reject with "Host key mismatch" — potential MITM.
- **Unknown host:** invoke TOFU callback. If accepted, record and proceed. If rejected, disconnect.

### Operations

- `lookup(host, port)` — returns entry or empty
- `record(host, port, key)` — writes/overwrites
- `forget(host, port)` — removes entry (next connection re-triggers TOFU)

## Client Mode

One-shot CLI for testing: `SSHWebServer --connect ssh-web://host:port --command <cmd>`

Flags:
- `--connect <url>` — SSH-Web URL to connect to
- `--command <cmd>` — command to execute (`capabilities`, `receive-pack /`, etc.)
- `--accept-host-key` — auto-accept unknown host keys (scripted/test use only)
- `--version` / `-V` — print version and exit

For `capabilities` commands, the response is parsed and pretty-printed as a manifest summary. Other commands return raw output.

## Service Architecture

`SSHWebServer` has two modes:

1. **Client mode** (`--connect`/`--command`): one-shot execution, used for testing
2. **Service mode** (no flags): long-lived IPC service, accepts connections from `WebContent` processes

The core transport code (`Connection`, `KnownHosts`) is compiled into a static library `sshwebservice`, shared by both modes and by tests.

## libssh2 API Surface

Core APIs used:
- Session: `libssh2_session_init`, `libssh2_session_handshake`, `libssh2_session_disconnect`, `libssh2_session_free`
- Host key: `libssh2_session_hostkey`, `libssh2_hostkey_hash` (SHA-256)
- Channels: `libssh2_channel_open_session`, `libssh2_channel_exec`, `libssh2_channel_read`, `libssh2_channel_close`, `libssh2_channel_free`

## Smoke Testing Against sshttpd

```bash
# Build sshttpd
go build -o sshttpd ./cmd/sshttpd

# Configure a local fixture
mkdir -p /tmp/sshweb-fixture/keys /tmp/sshweb-fixture/site
cat > /tmp/sshweb-fixture/sshttpd.conf <<'EOF'
site localhost {
    port 32443
    host-key /tmp/sshweb-fixture/keys/host_ed25519
    root /tmp/sshweb-fixture/site
    commands { receive-pack / }
    auth { anonymous [receive-pack] }
}
EOF
echo '<h1>fixture</h1>' > /tmp/sshweb-fixture/site/index.html

# Start sshttpd
./sshttpd -config /tmp/sshweb-fixture/sshttpd.conf &

# Connect (first visit triggers TOFU, --accept-host-key auto-accepts)
./Build/release/bin/SSHWebServer \
    --connect ssh-web://localhost:32443 \
    --command capabilities \
    --accept-host-key
```

Subsequent connections reuse the host key in `~/.config/sshweb/known_hosts/`.
