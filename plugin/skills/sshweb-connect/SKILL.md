---
name: sshweb
description: Connect to SSH-Web sites and discover their MCP tools. Use when a user mentions an SSH-Web URL, asks to connect to an sshttpd site, or when the auto-detection hook finds an SSH-Web domain.
triggers:
  - "connect to ssh-web"
  - "sshweb connect"
  - "ssh-web://"
  - "check ssh-web"
  - "what tools does this site have"
---

# SSH-Web Site Connection

When a user wants to interact with an SSH-Web site, use the `sshweb_connect` MCP tool to connect and discover the site's available tools.

## Steps

1. Extract the hostname and port from the user's request. Default port is 22443.
2. Call `sshweb_connect` with `host` and `port`.
3. Once connected, the site's MCP tools appear automatically. List them to the user.
4. Use the discovered tools as the user requests. Tool names are prefixed with `{host}/`.
5. When done, call `sshweb_disconnect` to clean up.

## Examples

User: "connect to news.example.com over ssh-web"
→ Call `sshweb_connect(host: "news.example.com", port: 22443)`

User: "what tools does ssh-web://shop.example.com:2222 have?"
→ Call `sshweb_connect(host: "shop.example.com", port: 2222)`

User: "list the posts on this site"
→ If connected, call `{host}/list_posts` with appropriate params
