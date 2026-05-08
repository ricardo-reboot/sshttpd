# Examples

## Quick Start

Build sshttpd:

```bash
go build -o bin/sshttpd ./cmd/sshttpd
```

Start the server with the example config:

```bash
./bin/sshttpd -config examples/sshttpd.conf
```

The host key is auto-generated on first start. The server listens on port `22443`.

## Connecting

With any SSH client:

```bash
# Capabilities manifest (JSON)
ssh -p 22443 -o StrictHostKeyChecking=no localhost capabilities

# Static content as packfile
ssh -p 22443 localhost "receive-pack /"

# RSS feed (Atom format)
ssh -p 22443 localhost "rss-feed /feeds/posts"

# Sitemap (JSON, includes static files when dynamic=true)
ssh -p 22443 localhost sitemap

# Robots policy (JSON)
ssh -p 22443 localhost robots
```

## Serving the Example Site over HTTP (for development)

The example site in `site/` is plain HTML/CSS. You can preview it with any static file server:

```bash
# Python
python3 -m http.server 8080 --directory examples/site

# Node.js (npx)
npx serve examples/site -l 8080

# Then open http://localhost:8080
```

This is useful for iterating on the HTML/CSS before testing over SSH-Web.

## Backend Mode

`backend.conf` demonstrates sshttpd proxying to an HTTP backend instead of serving static files. Start a backend first:

```bash
# Simple backend
python3 -m http.server 3000 --directory examples/site

# Then sshttpd with backend config
./bin/sshttpd -config examples/backend.conf

# receive-pack now returns the HTTP response from the backend
ssh -p 22443 localhost "receive-pack /"
```

## Discovery Metadata

The `meta` block in the config controls three discovery commands. All return JSON (except rss-feed which returns Atom XML) — SSH-Web replaces HTTP's file-based conventions (`robots.txt`, XML sitemaps) with first-class commands that return structured data. JSON is the native interchange format across all SSH-Web commands (`capabilities`, `robots`, `sitemap`, `mcp`), so consumers are always programmatic and never need to parse ad-hoc text formats.

### rss-feed

```
meta {
    rss-feed /feeds/posts format=atom
}
```

Returns an Atom feed built from files in the content root. Query it:

```bash
ssh -p 22443 localhost "rss-feed /feeds/posts"
```

```xml
<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>localhost</title>
  <id>ssh-web://localhost/feeds/posts</id>
  <entry><title>index.html</title><id>ssh-web://localhost/index.html</id></entry>
  <entry><title>posts/hello-world.html</title><id>ssh-web://localhost/posts/hello-world.html</id></entry>
  <entry><title>posts/why-ssh-web.html</title><id>ssh-web://localhost/posts/why-ssh-web.html</id></entry>
</feed>
```

### sitemap

```
meta {
    sitemap /sitemap dynamic=true
}
```

Returns a JSON sitemap. The `dynamic` flag controls scope:

- `dynamic=false` (default) — only lists declared routes from the `commands {}` block (e.g. `receive-pack /{path*}`).
- `dynamic=true` — also walks the `root` directory and lists every file, giving crawlers and agents full content discovery.

Trade-off: dynamic walks the filesystem per request, which is fine for small sites but expensive for large ones with thousands of assets. For large sites, use `dynamic=false` and curate routes manually.

With `dynamic=true`:

```bash
ssh -p 22443 localhost sitemap
```

```json
{
  "site": "localhost",
  "entries": [
    { "path": "/{path*}", "type": "receive-pack" },
    { "path": "/index.html", "type": "static" },
    { "path": "/posts/hello-world.html", "type": "static" },
    { "path": "/posts/why-ssh-web.html", "type": "static" },
    { "path": "/style.css", "type": "static" }
  ]
}
```

### robots

```
meta {
    robots crawl-delay=5 allow=["/", "/posts/*"]
}
```

Returns a structured robots policy (replaces `robots.txt`):

```bash
ssh -p 22443 localhost robots
```

```json
{
  "allowed-paths": ["/", "/posts/*"],
  "blocked-paths": null,
  "crawl-delay": 5
}
```

## Example Site Structure

```
examples/
├── sshttpd.conf          Full config with all directives
├── backend.conf          Backend-proxy mode config
├── site/
│   ├── index.html        Home page with post listing
│   ├── style.css         Shared styles
│   ├── sandbox-allow.html   JS sandbox test (allowlisted fetch)
│   ├── sandbox-block.html   JS sandbox test (blocked fetch)
│   └── posts/
│       ├── hello-world.html
│       └── why-ssh-web.html
```
