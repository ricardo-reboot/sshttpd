# IPC & URL Scheme Integration

## Overview

`WebContent` loads `ssh-web://` URLs through `SSHWebServer` via Ladybird's IPC system. The architecture mirrors how `WebContent` talks to `RequestServer` for HTTP — a pair of IPC endpoints, a long-lived service process, and a thin client library.

## IPC Endpoints

### SSHWebServer.ipc (client → service)

```
fetch_capabilities(u64 request_id, URL url)
execute_command(u64 request_id, URL url, ByteString command)
stop_request(u64 request_id) => (bool success)
tofu_decision(u64 prompt_id, bool accept)
```

### SSHWebClient.ipc (service → client)

```
tofu_prompt(u64 prompt_id, ByteString host, u16 port, ByteString key_type, ByteString fingerprint_sha256)
command_chunk(u64 request_id, ByteBuffer data, bool is_final)
command_completed(u64 request_id, Optional<ByteString> error)
```

## Service Mode

When invoked without `--connect`, `SSHWebServer` runs as a long-lived multi-client IPC service. Each connected `WebContent` process gets one `ConnectionFromClient` instance (mirroring `RequestServer`'s pattern).

For each request, `ConnectionFromClient`:
1. Parses the URL via `SSHWeb::URL::parse`
2. Looks up or creates an `SSHWeb::Connection` for `(host, port)`
3. For TOFU prompts, sends `tofu_prompt` to the client and waits for `tofu_decision`
4. Runs the command, streams bytes back as `command_chunk`, then `command_completed`

### TOFU Synchronization

The TOFU callback in `Connection::open` is synchronous, but the IPC TOFU exchange is asynchronous. Resolution: synchronous spin — pump the event loop until the TOFU response arrives. This blocks the service on user input but is simpler than refactoring `Connection::open` into continuation-passing style.

## LibSSHWebClient

Client library used by `WebContent`. Mirrors how `LibRequests` wraps `RequestServer`.

```cpp
class Client : public IPC::ConnectionToServer<SSHWebServerEndpoint, SSHWebClientEndpoint> {
    void execute_command(URL const& url, ByteString command, OnComplete);
    // Accumulates command_chunk events per request_id
    // Calls OnComplete when command_completed arrives
};
```

TOFU prompt handling at the client side eventually routes to the UI process for a real dialog. During development, the client defaults to auto-accept with logging.

## ResourceLoader Integration

`LibWeb/Loader/ResourceLoader.cpp` gains scheme-conditional dispatch: if `url.scheme() == "ssh-web"`, route through a process-singleton `LibSSHWebClient::Client` instead of `LibRequests::RequestClient`.

For `receive-pack` responses, the loader synthesizes HTTP-like headers (`Content-Type: text/html`, `Content-Length`, status 200) so LibWeb treats the response correctly.

The integration is conditional on `LADYBIRD_ENABLE_SSHWEB` — when disabled, no ssh-web code paths exist in LibWeb.

## Design Decisions

- **One connection per request (initially).** No connection pooling in the first cut. A pool keyed by `(host, port, identity)` is a follow-up.
- **Option A dispatch** (scheme check in ResourceLoader) rather than a registered handler pattern. Simpler diff, smaller blast radius.
- **Synchronous TOFU spin** rather than continuation-based `Connection::open`. Trades elegance for simplicity. Can be refactored once UI integration stabilizes.
