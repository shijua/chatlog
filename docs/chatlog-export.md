# Local chat export: all message tables

Requirements: Python 3.11+ and locally decrypted **macOS WeChat 3.x** databases. No Python dependencies are needed. Run from the repository:

```sh
python3 scripts/chatlog_export.py
```

The default now synchronizes **every `Chat_<32-character hash>` table in every `msg_*.db` database**, independently of the session list or whether a wxid can be identified. The number of tables is discovered each run, not hardcoded to 935. Empty tables and unmapped identities are included. Auxiliary `_dels` tables and non-message databases are outside this export's scope; records already absent from the local database cannot be recovered.

The decrypted work directory is read from `~/.chatlog/chatlog-server.json` (`work_dir` only is used; keys are never printed). To select it explicitly:

```sh
python3 scripts/chatlog_export.py --work-dir /absolute/path/to/decrypted/databases
```

## Files and migration

Message files are stored directly in `chatlog-export/`, alongside `index.json`. Older `tables/` layouts are automatically moved into the export root without copying message data. The index paths are updated, and the empty subdirectory is removed. Conflicting destination files are never overwritten. Interrupted moves resume after verifying already-moved file checksums.

- `chatlog-export/index.json`: schema version 2; contains `tables`, `table_count`, `total_messages`, `failures`, `database_failures`, `unresolved_tables`, `missing_tables`, and `last_run`.
- `chatlog-export/*.json` (excluding indexes): one JSON array per database/table pair. Same-named tables in different database shards get separate files. Rows contain **original SQLite fields**, including `mesLocalID`, `mesSvrID`, `msgCreateTime`, and `msgContent`. This is a different format from the API's normalized messages. BLOBs are losslessly encoded as `{"$sqlite_blob":"<base64>"}`. Media files are not downloaded.
- An existing session-based API index is copied to `chatlog-export/api-index.json` before the main index is migrated. Existing API message files and `chatlog-session.json` are retained. They are compatibility exports, not the all-table synchronization output, and their counts must not be added to the raw-table counts.

After verifying migration, the old API message files, `api-index.json`, and `chatlog-session.json` can be removed. Keep the raw-table JSON files in the export root and the main `index.json`; resolved identities are retained in that index and reused on later runs. Default database synchronization does not recreate the removed legacy exports.

The first database run writes all tables. Subsequent runs inspect the schema and database/WAL/journal file metadata (inode, size, and modification/change times). If the source signature and exported file size have not changed, the table is skipped. A changed database is read in a SQLite read transaction and its tables are regenerated as atomic snapshots. This catches newly imported old-timestamp messages, edits, and deletions without depending on API time windows or non-unique message IDs. It is incremental **at database-file granularity**, not a row-level change feed. A large changed database can take longer. Source changes during a snapshot invalidate its cached signature so the next run rereads it. For recovery or manual verification, force all snapshots:

```sh
python3 scripts/chatlog_export.py --full
```

The default skips same-size local export modifications when source metadata is unchanged; use `--full` to regenerate suspected damaged exports. Snapshots reflect current source rows: edits replace values and deleted rows disappear from that table's next snapshot. Entire tables missing from the source are reported under `missing_tables`; their export files remain on disk.

Identity matching uses local contact, group-member, group and session tables, plus identifiers from earlier exports. Unmatched hashes still have full raw exports and appear under `unresolved_tables`. A mapped non-group chat is labeled `non_group`, not automatically called a personal friend: service/official accounts also exist. Identity lookup warnings are recorded separately and do not prevent raw-table export.

All source connections use SQLite `mode=ro` and `query_only=ON`. There is no sending, source modification, server-binding change or upload. Export files are atomically replaced and index checkpoints saved after each database. Failures retain previous files and are recorded; concurrent runs against the same destination are rejected. SQLite transactions are per database; no globally consistent snapshot across all databases is promised. No scheduled job is installed. Exports remain ignored by Git.

## Legacy API mode

```sh
python3 scripts/chatlog_export.py --source api
python3 scripts/chatlog_export.py --source api --overlap-hours 168
python3 scripts/chatlog_export.py --source api --full
```

This compatibility mode uses `127.0.0.1:5030`, updates the original session-based files and (after migration) `api-index.json`, and refreshes `chatlog-session.json`. It discovers sessions through `/session`, so it **does not cover all message tables**. It queries every known session from its previous successful run boundary minus 24 hours through the current run's fixed start, and new sessions fetch full history. It paginates until an empty page. Overlap matching preserves duplicate occurrence counts; failures preserve the previous file/checkpoint. Because API 3.x responses lack a reliable unique message ID, API mode preserves old records and may retain additional versions of edited messages. Very old imported history can fall outside the overlap window.

Use `--output /absolute/path` to select a different destination.

## Local export exclusions

To keep a deliberately removed local conversation export from being recreated, add its 32-character `table_hash` to `index.json` → `excluded_table_hashes`. Default database mode (including `--full`) skips that hash across all database shards and retains the exclusion list. The index reports included `table_count`, `excluded_table_count`, and `discovered_table_count`. Remove the old JSON and index entry when applying the exclusion; synchronization does not delete files on its own. This only affects local exports, not WeChat databases or timeline API results. Removing a hash from the list re-enables export on the next run.

## Tests

```sh
python3 -m unittest discover -s scripts -p 'test_chatlog*.py'
```
