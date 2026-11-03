# Chatlog repository notes

## Development

- Keep changes small and explain commands before running them. Use Chinese for user-facing explanations.
- Build the local Mac target with Go 1.24 and CGO enabled:
  `GOTOOLCHAIN=go1.24.0 CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -trimpath -o bin/chatlog main.go`.
- The module name and legacy imports are connected by the existing `replace` in `go.mod`; do not remove it incidentally.
- For gateway changes, run `GOTOOLCHAIN=go1.24.0 CGO_ENABLED=1 go test ./cmd/chatlog-readonly`.
- For timeline/backend HTTP changes, use the legacy import paths shown in [the timeline guide](docs/timeline-api.md#测试).

## Local data and services

- The local WeChat installation must stay on 3.8.9; do not upgrade it or change SIP settings automatically.
- Keep the backend on `127.0.0.1:5030`; remote reads go through the separate readonly gateway with its existing peer whitelist.
- See [the readonly API](docs/readonly-api.md) for current routes, media keys, fallback behavior and limits. Keep both MD5 and message-key media routes compatible unless the user asks otherwise.
- Do not print keys, credentials, private message content or account identifiers in diagnostics, commits or documentation.
- Do not send messages, upload exports or modify source WeChat databases without explicit authorization.
- Local synchronization is `python3 scripts/chatlog_export.py --quiet`; it defaults to all decrypted message tables, honors its file lock and preserves configured exclusions. See [the export guide](docs/chatlog-export.md).
- Keep `bin/` and `chatlog-export/` ignored and in place during Git maintenance. Do not reintroduce deleted legacy exports or excluded conversation files.

## Git alignment

- This repository's remote history was rewritten to remove private data. Do not merge, push or restore pre-cleanup history from backups or old clones.
- Use the current workspace for normal commits and pushes. An isolated clone is only a temporary work area, not a reason to leave this workspace on an old HEAD.
- After an authorized push, verify that local HEAD matches remote `main` and inspect `git status --short`. Report any intentional remaining user changes; do not clear them with a destructive reset.
- If an isolated clone was necessary, safely align this workspace to the pushed history before calling the work complete. First preserve unrelated changes and ignored runtime data.
- Keep project operational facts here and API usage in `docs/`; never hardcode personal account IDs or keys in either.
