#!/usr/bin/env python3
"""Mock backend for sshttpd MCP tools and static content."""

from http.server import HTTPServer, BaseHTTPRequestHandler
import json

POSTS = [
    {"id": 1, "title": "Hello World", "body": "First post on SSH-Web."},
    {"id": 2, "title": "Why SSH-Web?", "body": "SSH-Web replaces HTTPS with SSH transport."},
    {"id": 3, "title": "Identity via Keypair", "body": "No passwords, no OAuth. Just ed25519."},
]

next_id = len(POSTS) + 1


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(b"<h1>Mock Backend</h1>")

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
    HTTPServer(("", port), Handler).serve_forever()
