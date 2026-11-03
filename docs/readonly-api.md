# Remote read-only API

Keep the chatlog backend on `127.0.0.1:5030`; expose only the gateway on the Mac's Tailscale IPv4 address:

```sh
GOTOOLCHAIN=go1.24.0 go build -o bin/chatlog-readonly ./cmd/chatlog-readonly
./bin/chatlog-readonly --addr <Mac-Tailscale-IP>:5030 --allow-peer <VM-Tailscale-IP>
```

## JSON endpoints

All endpoints use GET and return JSON. `format=json` is optional; other formats are rejected.

| Endpoint | Accepted query parameters |
| --- | --- |
| `/api/v1/session` | `keyword`, `limit`, `offset`, `format` |
| `/api/v1/contact` | `keyword`, `limit`, `offset`, `format` |
| `/api/v1/chatroom` | `keyword`, `limit`, `offset`, `format` |
| `/api/v1/chatlog` | `talker`, `time`, `sender`, `keyword`, `limit`, `offset`, `format` |
| `/api/v1/timeline` | `start`, `end`, `limit`, `cursor`, `exclude_talker`, `exclude_sender`, `format` |

Use chatlog's `keyword` for message search. See [timeline parameters and pagination](timeline-api.md) for account-wide time ranges.

## Media endpoints

These GET endpoints return file bytes directly and accept no query parameters. `<md5>` is one 32-character hexadecimal media hash; it must resolve in the local media index.

| Endpoint | Response | Size limit |
| --- | --- | --- |
| `/image/v3_…` (see below) | JPEG, PNG, GIF, WebP or BMP | 32 MiB |
| `/file/<md5>` | Download attachment (`application/octet-stream`) | 64 MiB |
| `/voice/<md5>` | Original SILK, AMR, MP3, WAV or Ogg audio | 32 MiB |
| `/video/<md5>` | MP4 or WebM | 128 MiB |

Image, audio and video content signatures are checked. Attachments use `Content-Disposition: attachment` and a sandbox CSP. No transcription, automatic audio conversion, CDN download or Range forwarding is performed.

### Mac WeChat 3.x message keys

For images, voice and video, prefer these routes when a message MD5 is empty or absent from the media index:

| Endpoint | Fields from a timeline item |
| --- | --- |
| `/image/v3_<tableHash>_<localId>_<timestamp>` | `type=3`; convert `time` to Unix **seconds** |
| `/voice/v3_<tableHash>_<localId>_<serverId>` | `type=34`; keep `serverId` as a decimal string to avoid JavaScript rounding |
| `/video/v3_<tableHash>_<localId>_<timestamp>` | `type=43`; convert `time` to Unix **seconds**, not milliseconds |

The gateway derives a fixed Image, Audio or Video path from the message fields. Table hashes must be hexadecimal; IDs must be canonical positive decimals (`localId` and image/video timestamps: uint32; voice server ID: uint64). The same media size and format limits apply.

Only `/image/v3_<tableHash>_<localId>_<timestamp>` tries HD, original, thumbnail, then thumbnail preview, advancing only on 404. Successful responses include `X-Chatlog-Media-Variant: hd`, `original`, or `thumbnail`. `/image/<md5>` is rejected with 404; only the image message-key format is exposed.

Missing images return 404 after all candidates fail; missing audio/video originals also return 404. Video thumbnails are not substituted. These rules cover the verified Mac 3.x naming schemes, not all historical files. Invalid media or oversized responses return 502. Neither route accepts arbitrary file paths.

## Access restrictions

- Only GET from the configured `--allow-peer` IP is accepted; other sources receive 403. Forwarded IP headers are ignored.
- Unknown routes, encoded paths, request bodies, and duplicate or unknown query parameters are rejected. Client headers are not forwarded and backend redirects are not followed.
- Direct `/data/`, MCP, message sending and file writing are not exposed. Search, diary and Moments/SNS APIs are not implemented.
- Responses use `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`.

The default gateway listener is `127.0.0.1:5031` for local checks. Only loopback and Tailscale IPv4 listeners are allowed. Configure tailnet access separately; the gateway does not change Tailscale policy or provide application authentication. The allowed VM can read all API-accessible messages and media. Keep the original backend private; these restrictions apply only to the gateway.
