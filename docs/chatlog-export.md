# Incremental local chat export

Requirements: Python 3.11+, and chatlog running at `127.0.0.1:5030` with automatic decryption enabled. No Python dependencies are needed. Run from the repository:

```sh
python3 scripts/chatlog_export.py
```

The default destination is `chatlog-export/` in this repository. The script refreshes `chatlog-session.json`, discovers new sessions, checks every known session (including sessions no longer listed), and updates one JSON array per talker plus `chatlog-export/index.json`. It does not send messages, change server configuration, or download media attachments. Both export paths are ignored by Git.

Existing sessions query from their last successful run boundary minus 24 hours up to this run's fixed start time. Original exports without per-session checkpoints use the original export's start time, not its completion time. New sessions fetch all API-readable history up to this run's start. Every query paginates until an empty response, including after short pages. Each completed conversation advances its checkpoint independently; failed conversations retain their files and previous checkpoint for retry. Concurrent runs against the same export directory are rejected.

WeChat 3.x API responses lack a reliable unique message ID. Overlap matching uses timestamps and message fields, excluding display names and `seq`, and compares occurrence counts. This preserves identical messages sent multiple times and avoids re-appending an unchanged overlap on repeated runs. Existing records are preserved; edits can appear as additional versions, and deletions are not propagated. The script cannot guarantee discovery of history imported with timestamps earlier than the overlap window. It also cannot recover records absent from the local API, and the live API does not provide a transactional snapshot.

To cover a week of delayed history, or reconcile all available history:

```sh
python3 scripts/chatlog_export.py --overlap-hours 168
python3 scripts/chatlog_export.py --full
```

`--full` merges the entire API-readable history into existing files; it does not discard existing records. Large histories take longer and require more memory. `--page-size` (default 10000) can reduce per-request load. `--output /absolute/path` selects another export directory; its sibling `chatlog-session.json` is also refreshed.

The final console summary and `index.json` → `last_run` report added messages, updated/new/successful/failed sessions. Each conversation records `last_success_at`, message count, file size and SHA-256. Failures appear under `failures`; a nonzero exit code signals an incomplete run. Export files are atomically replaced, and index checkpoints are saved per conversation. If interrupted between file replacement and checkpoint saving, overlap matching makes retry safe. The output remains local; nothing is uploaded. No scheduled job is installed.

Run tests:

```sh
python3 -m unittest discover -s scripts -p 'test_chatlog_export.py'
```
