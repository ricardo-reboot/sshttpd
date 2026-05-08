#!/usr/bin/env python3
"""Mock backend for sshttpd MCP tools and static content."""

from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import os

POSTS = [
    {"id": 1, "title": "Hello World", "body": "First post on SSH-Web."},
    {"id": 2, "title": "Why SSH-Web?", "body": "SSH-Web replaces HTTPS with SSH transport."},
    {"id": 3, "title": "Identity via Keypair", "body": "No passwords, no OAuth. Just ed25519."},
    {"id": 4, "title": "Members Only: Roadmap", "body": "Upcoming: multi-site hosting, WebSocket tunneling, and end-to-end encrypted group channels.", "min_tier": "identified"},
]

TIER_RANK = {"anonymous": 0, "identified": 1, "trusted": 2}

next_id = len(POSTS) + 1

AUTHORIZED_KEYS_PATH = os.environ.get("AUTHORIZED_KEYS", "./keys/authorized_keys")


def load_authorized_keys():
    """Return dict mapping 'type base64' -> full line (with comment) from authorized_keys."""
    keys = {}
    try:
        with open(AUTHORIZED_KEYS_PATH) as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                # Strip tier= prefix if present
                keyline = line
                if keyline.startswith("tier="):
                    keyline = keyline.split(None, 1)[1] if " " in keyline else ""
                if keyline:
                    # Key identity = "type base64" (first two fields)
                    parts = keyline.split(None, 2)
                    if len(parts) >= 2:
                        key_id = f"{parts[0]} {parts[1]}"
                        keys[key_id] = keyline
    except FileNotFoundError:
        pass
    return keys


def append_authorized_key(pubkey):
    """Append a pubkey line to authorized_keys with tier=trusted."""
    os.makedirs(os.path.dirname(AUTHORIZED_KEYS_PATH), exist_ok=True)
    with open(AUTHORIZED_KEYS_PATH, "a") as f:
        f.write(f"tier=trusted {pubkey}\n")


def pubkey_id(pubkey):
    """Extract 'type base64' from a pubkey line (ignore comment)."""
    parts = pubkey.split(None, 2)
    if len(parts) >= 2:
        return f"{parts[0]} {parts[1]}"
    return pubkey


def is_key_registered(pubkey):
    """Check if a pubkey is already in authorized_keys."""
    keys = load_authorized_keys()
    return pubkey_id(pubkey) in keys


def get_display_name(pubkey):
    """Look up the display name (comment) for a pubkey from authorized_keys."""
    if not pubkey:
        return ""
    keys = load_authorized_keys()
    kid = pubkey_id(pubkey)
    if kid in keys:
        parts = keys[kid].split(None, 2)
        if len(parts) >= 3:
            return parts[2]
    # Fall back to comment in the pubkey header itself
    parts = pubkey.split(None, 2)
    if len(parts) >= 3:
        return parts[2]
    return ""


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        tier = self.headers.get("X-SSHWeb-Tier", "anonymous")
        fingerprint = self.headers.get("X-SSHWeb-Fingerprint", "")
        pubkey = self.headers.get("X-SSHWeb-PubKey", "")

        if self.path == "/register":
            self.handle_register(tier, fingerprint, pubkey)
            return

        if self.path.startswith("/posts/"):
            self.handle_post_page(self.path, tier, fingerprint, pubkey)
            return

        # Extract display name from pubkey comment field (third space-separated part)
        display_name = ""
        if pubkey:
            parts = pubkey.split(None, 2)
            if len(parts) >= 3:
                display_name = parts[2]

        if tier == "trusted":
            who = display_name or "trusted user"
            greeting = f"Welcome back, {who}!"
            badge_color = "#2e7d32"
            badge_label = "TRUSTED"
        elif tier == "identified":
            who = display_name or "identified visitor"
            greeting = f"Hello, {who}."
            badge_color = "#1565c0"
            badge_label = "IDENTIFIED"
        else:
            greeting = "Browsing anonymously."
            badge_color = "#757575"
            badge_label = "ANONYMOUS"

        fp_html = ""
        if fingerprint:
            fp_html = f'<p style="font-family:monospace;font-size:13px;opacity:0.7">Fingerprint: {fingerprint}</p>'

        register_html = ""
        if tier == "trusted":
            register_html = '<p style="color:#2e7d32;font-size:14px;margin-top:12px">&#10003; Registered and trusted.</p>'
        elif tier == "identified" and pubkey and is_key_registered(pubkey):
            register_html = '<p style="color:#2e7d32;font-size:14px;margin-top:12px">&#10003; Your key is registered. Reconnect to get trusted tier.</p>'
        else:
            register_html = '''
    <div style="margin-top:12px; padding-top:12px; border-top:1px solid #eee">
      <a href="/register" style="
        display:inline-block; padding:8px 20px; background:#1565c0; color:white;
        border-radius:6px; text-decoration:none; font-size:14px; font-weight:600;
      ">Register My Key</a>
    </div>'''

        post_items = []
        for p in POSTS:
            pid = p["id"]
            title = p["title"]
            min_tier = p.get("min_tier", "anonymous")
            if TIER_RANK.get(tier, 0) >= TIER_RANK.get(min_tier, 0):
                lock = " &#128274;" if p.get("min_tier") else ""
                post_items.append(f'<li style="padding:6px 0;border-bottom:1px solid #eee"><a href="/posts/{pid}" style="color:#1565c0;text-decoration:none;font-weight:500">{title}</a>{lock}</li>')
            else:
                post_items.append(f'<li style="padding:6px 0;border-bottom:1px solid #eee;opacity:0.5">&#128274; {title} <span style="font-size:12px;color:#999">({min_tier}+ only)</span></li>')
        posts_html = "".join(post_items)

        html = f"""<!doctype html>
<html>
<head><title>Mock Backend</title>
<style>
  body {{ font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px; }}
  .badge {{ display: inline-block; padding: 3px 10px; border-radius: 4px;
            color: white; font-size: 12px; font-weight: 600; background: {badge_color}; }}
  .card {{ border: 1px solid #ddd; border-radius: 8px; padding: 16px 20px; margin-top: 16px; }}
</style></head>
<body>
  <h1>Mock Backend</h1>
  <span class="badge">{badge_label}</span>
  <div class="card">
    <h2>{greeting}</h2>
    {fp_html}
    <p>Tier: <code>{tier}</code></p>
    {register_html}
  </div>
  <div class="card">
    <h3>Posts</h3>
    <ul style="list-style:none;padding:0;margin:0">
      {posts_html}
    </ul>
  </div>
</body>
</html>"""

        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(html.encode())

    def handle_register(self, tier, fingerprint, pubkey):
        if not pubkey:
            html = """<!doctype html>
<html>
<head><title>Identity Required</title>
<style>
  body { font-family: system-ui, sans-serif; max-width: 480px; margin: 60px auto; padding: 0 20px; text-align: center; }
  .icon { font-size: 48px; margin-bottom: 12px; }
  a { color: #1565c0; }
</style></head>
<body>
  <div class="icon">&#128273;</div>
  <h2>No Identity Selected</h2>
  <p>Open the identity popover in the toolbar and select a keypair, then try again.</p>
  <p><a href="/">Back to home</a></p>
</body>
</html>"""
            self.send_response(200)
            self.send_header("Content-Type", "text/html")
            self.end_headers()
            self.wfile.write(html.encode())
            return

        if not is_key_registered(pubkey):
            append_authorized_key(pubkey)
            print(f"[mock-backend] registered key: {fingerprint} -> {pubkey[:40]}...")

        self.send_response(302)
        self.send_header("Location", "/")
        self.send_header("Content-Length", "0")
        self.end_headers()

    def handle_post_page(self, path, tier, fingerprint, pubkey):
        try:
            post_id = int(path.split("/")[-1])
        except ValueError:
            self.send_response(404)
            self.send_header("Content-Type", "text/html")
            self.end_headers()
            self.wfile.write(b"<h1>404</h1><p>Not found.</p>")
            return

        post = next((p for p in POSTS if p["id"] == post_id), None)
        if not post:
            self.send_response(404)
            self.send_header("Content-Type", "text/html")
            self.end_headers()
            self.wfile.write(b"<h1>404</h1><p>Post not found.</p>")
            return

        min_tier = post.get("min_tier", "anonymous")
        if TIER_RANK.get(tier, 0) < TIER_RANK.get(min_tier, 0):
            html = f"""<!doctype html>
<html>
<head><title>Access Denied</title>
<style>
  body {{ font-family: system-ui, sans-serif; max-width: 480px; margin: 60px auto; padding: 0 20px; text-align: center; }}
  a {{ color: #1565c0; }}
</style></head>
<body>
  <h2>&#128274; Access Denied</h2>
  <p>This post requires <strong>{min_tier}</strong> tier or above.</p>
  <p>Your current tier: <code>{tier}</code></p>
  <p><a href="/">Back to home</a></p>
</body>
</html>"""
            self.send_response(403)
            self.send_header("Content-Type", "text/html")
            self.end_headers()
            self.wfile.write(html.encode())
            return

        html = f"""<!doctype html>
<html>
<head><title>{post["title"]}</title>
<style>
  body {{ font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px; }}
  .back {{ color: #1565c0; text-decoration: none; font-size: 14px; }}
  .card {{ border: 1px solid #ddd; border-radius: 8px; padding: 20px 24px; margin-top: 16px; }}
</style></head>
<body>
  <a class="back" href="/">&larr; Home</a>
  <div class="card">
    <h1>{post["title"]}</h1>
    <p>{post["body"]}</p>
  </div>
</body>
</html>"""

        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(html.encode())

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(length)) if length else {}

        if self.path == "/mcp/list_posts":
            page = int(body.get("page", 1))
            limit = int(body.get("limit", 10))
            start = (page - 1) * limit
            result = {"posts": POSTS[start:start + limit], "total": len(POSTS)}

        elif self.path == "/mcp/get_post":
            post_id = int(body.get("id", 0))
            post = next((p for p in POSTS if p["id"] == post_id), None)
            if post:
                result = post
            else:
                self.send_response(404)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"error": f"post {post_id} not found"}).encode())
                return

        elif self.path == "/mcp/create_post":
            global next_id
            post = {"id": next_id, "title": body.get("title", ""), "body": body.get("body", "")}
            POSTS.append(post)
            next_id += 1
            result = post

        else:
            self.send_response(404)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"error": f"unknown endpoint: {self.path}"}).encode())
            return

        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(result, indent=2).encode())

    def log_message(self, format, *args):
        print(f"[mock-backend] {args[0]}")


if __name__ == "__main__":
    port = 8080
    print(f"Mock backend listening on http://localhost:{port}")
    print(f"Authorized keys: {AUTHORIZED_KEYS_PATH}")
    HTTPServer(("", port), Handler).serve_forever()
