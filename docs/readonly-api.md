# Remote read-only API

Keep chatlog bound to `127.0.0.1:5030`. Expose the separate gateway on the Mac's assigned Tailscale IPv4 address:

```sh
GOTOOLCHAIN=go1.24.0 go build -o bin/chatlog-readonly ./cmd/chatlog-readonly
./bin/chatlog-readonly --addr <Mac-Tailscale-IP>:5030 --allow-peer <VM-Tailscale-IP>
```

The gateway only forwards GET requests for `/api/v1/session`, `/api/v1/chatlog`, `/api/v1/contact`, `/api/v1/chatroom`, and `/api/v1/timeline` to the fixed local backend. Contacts and chatrooms accept `keyword`, `limit`, `offset`, and `format`. Timeline accepts `start`, `end`, `limit`, `cursor`, `format`, `exclude_talker`, and `exclude_sender`; see [the timeline API](timeline-api.md). It accepts the existing query parameters for those routes and returns JSON. `/api/v1/search` and `/api/v1/diary` are not implemented in this version; message search uses the chatlog endpoint's `keyword` parameter. The gateway also supports `GET /image/<md5>` (one 32-character hexadecimal hash, no query parameters). It resolves the hash through the backend media index and returns image bytes directly, with no redirect. Only JPEG, PNG, GIF, WebP and BMP signatures are accepted, up to 32 MiB. Missing local media or an unavailable media index cannot be recovered by this endpoint. It does not accept local file paths, comma-separated keys, or direct `/data/` access.

All other paths and methods, request bodies, duplicate or unknown query parameters, and encoded paths are rejected. Client headers are not forwarded, and backend redirects are not followed. It has no message-sending or file-writing endpoint.

Remote binding requires `--allow-peer` with exactly one Tailscale IPv4 source. Connections from all other addresses receive 403 before reaching chatlog. The check uses the actual connection source and ignores forwarded headers.

The default listener is `127.0.0.1:5031` for local verification. Wildcard and LAN listen addresses are rejected. The gateway does not configure Tailscale: use a tailnet policy that only permits the intended VM to reach the gateway's TCP port, and check for broader existing allow rules. Neither process provides application authentication. An allowed reader can retrieve all API-accessible conversations and messages.

This restriction applies to the gateway. Never expose the original chatlog listener remotely, because it also provides media, file and MCP routes. It does not restrict unrelated remote access such as SSH or file sharing.
