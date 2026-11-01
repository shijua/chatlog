import base64
from contextlib import closing
import hashlib
import json
from pathlib import Path
import sqlite3
from tempfile import TemporaryDirectory
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import chatlog_export as export
import chatlog_sqlite as local


class DatabaseSyncTests(unittest.TestCase):
    def setUp(self):
        self.tmp = TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.work = self.root / "work"
        self.work.mkdir()
        self.dest = self.root / "export"
        self.dest.mkdir()
        self.args = SimpleNamespace(output=self.dest, work_dir=self.work, full=False, quiet=True)
        self.first = "wxid_hidden"
        self.other = "wxid_unresolved"
        self.first_table = self.table(self.first)
        self.other_table = self.table(self.other)
        self.db_path = self.work / "msg_0.db"
        with closing(sqlite3.connect(self.db_path)) as db, db:
            self.create_table(db, self.first_table)
            self.create_table(db, self.other_table)
            self.add(db, self.first_table, "hello", 100, b"\x00\xff")
            self.add(db, self.other_table, "hidden too", 1)
        with closing(sqlite3.connect(self.work / "wccontact_new2.db")) as db, db:
            db.execute("CREATE TABLE WCContact (m_nsUsrName TEXT, nickname TEXT, m_nsRemark TEXT)")
            db.execute("INSERT INTO WCContact VALUES (?, ?, ?)", (self.first, "Hidden contact", ""))

    @staticmethod
    def table(user):
        return "Chat_" + hashlib.md5(user.encode()).hexdigest()

    @staticmethod
    def create_table(db, table):
        db.execute('CREATE TABLE "' + table + '" (mesLocalID INTEGER PRIMARY KEY, mesSvrID INTEGER, msgCreateTime INTEGER, msgContent TEXT, ConBlob BLOB)')

    @staticmethod
    def add(db, table, text, stamp, blob=None):
        db.execute('INSERT INTO "' + table + '" (msgCreateTime, msgContent, ConBlob) VALUES (?, ?, ?)', (stamp, text, blob))

    def run_sync(self):
        summary = local.sync_tables(self.args)
        index = json.loads((self.dest / "index.json").read_text())
        return summary, index

    def rows(self, entry):
        return json.loads((self.dest / entry["file"]).read_text())

    def test_all_tables_including_unknown_identities_and_blob(self):
        summary, index = self.run_sync()
        self.assertEqual(summary["table_count"], 2)
        self.assertEqual(summary["total_messages"], 2)
        self.assertEqual(summary["unresolved_tables"], 1)
        known = next(e for e in index["tables"] if e["talker"] == self.first)
        row = self.rows(known)[0]
        self.assertEqual(row["ConBlob"], {"$sqlite_blob": base64.b64encode(b"\x00\xff").decode()})
        self.assertEqual(known["kind"], "non_group")
        unknown = next(e for e in index["tables"] if e["talker"] is None)
        self.assertEqual(self.rows(unknown)[0]["msgContent"], "hidden too")
        self.assertEqual(unknown["kind"], "unresolved")

    def test_resolved_identity_survives_legacy_cleanup(self):
        listing = self.dest.parent / "chatlog-session.json"
        export.atomic_json(listing, {"items": [{"userName": self.other, "nickName": "Previously known"}]})
        summary, _ = self.run_sync()
        self.assertEqual(summary["unresolved_tables"], 0)
        listing.unlink()
        summary, index = self.run_sync()
        self.assertEqual(summary["unresolved_tables"], 0)
        retained = next(e for e in index["tables"] if e["talker"] == self.other)
        self.assertEqual(retained["nickname"], "Previously known")
        self.assertFalse(listing.exists())
        self.assertFalse((self.dest / "api-index.json").exists())

    def nest_exports(self):
        _, index = self.run_sync()
        nested = self.dest / "tables"
        nested.mkdir()
        for entry in index["tables"]:
            source = self.dest / entry["file"]
            source.rename(nested / source.name)
            entry["file"] = "tables/" + source.name
        export.atomic_json(self.dest / "index.json", index)
        return index

    def test_flat_layout_and_nested_migration_skip_reread(self):
        previous = self.nest_exports()
        with patch.object(local, "snapshot", side_effect=AssertionError("unnecessary reread")):
            summary, index = self.run_sync()
        self.assertEqual(summary["skipped_tables"], 2)
        self.assertFalse((self.dest / "tables").exists())
        for old, new in zip(previous["tables"], index["tables"]):
            self.assertEqual(new["file"], Path(old["file"]).name)
            self.assertEqual(new["sha256"], old["sha256"])
            self.assertTrue((self.dest / new["file"]).exists())

    def test_interrupted_flattening_recovers_from_verified_destination(self):
        index = self.nest_exports()
        entry = index["tables"][0]
        source = self.dest / entry["file"]
        source.rename(self.dest / source.name)
        summary, _ = self.run_sync()
        self.assertEqual(summary["skipped_tables"], 2)
        self.assertFalse((self.dest / "tables").exists())

    def test_flatten_collision_never_overwrites_files(self):
        index = self.nest_exports()
        entry = index["tables"][0]
        target = self.dest / Path(entry["file"]).name
        target.write_text("unrelated file")
        with self.assertRaisesRegex(ValueError, "already exists"):
            self.run_sync()
        self.assertEqual(target.read_text(), "unrelated file")
        self.assertTrue((self.dest / entry["file"]).exists())
        self.assertEqual(json.loads((self.dest / "index.json").read_text()), index)

    def test_no_change_skips_all_snapshots(self):
        self.run_sync()
        with patch.object(local, "snapshot", side_effect=AssertionError("unexpected reread")):
            summary, index = self.run_sync()
        self.assertEqual(summary["skipped_tables"], 2)
        self.assertEqual(summary["rewritten_tables"], 0)

    def test_old_timestamp_insert_edit_and_delete_are_reflected(self):
        self.run_sync()
        with closing(sqlite3.connect(self.db_path)) as db, db:
            db.execute('UPDATE "' + self.first_table + '" SET msgContent=?', ("edited",))
            self.add(db, self.first_table, "late history", 0)
            db.execute('DELETE FROM "' + self.other_table + '"')
        summary, index = self.run_sync()
        self.assertEqual(summary["rewritten_tables"], 2)
        known = next(e for e in index["tables"] if e["talker"] == self.first)
        self.assertEqual([m["msgContent"] for m in self.rows(known)], ["edited", "late history"])
        self.assertEqual(summary["total_messages"], 2)
        empty = next(e for e in index["tables"] if e["table"] == self.other_table)
        self.assertEqual(empty["message_count"], 0)

    def test_same_table_in_two_shards_does_not_overwrite(self):
        with closing(sqlite3.connect(self.work / "msg_1.db")) as db, db:
            self.create_table(db, self.first_table)
            self.add(db, self.first_table, "second shard", 200)
        summary, index = self.run_sync()
        self.assertEqual(summary["table_count"], 3)
        self.assertEqual(index["unique_table_hashes"], 2)
        self.assertEqual(len({e["file"] for e in index["tables"]}), 3)
        self.assertEqual(summary["total_messages"], 3)

    def test_migration_preserves_old_index_and_files(self):
        old = {"started_at": "2026-10-31T00:00:00Z", "sessions": [{"talker": "legacy"}]}
        export.atomic_json(self.dest / "index.json", old)
        (self.dest / "legacy.json").write_text('[{"content":"old"}]')
        self.run_sync()
        self.assertEqual(json.loads((self.dest / "api-index.json").read_text()), old)
        self.assertTrue((self.dest / "legacy.json").exists())

    def test_interrupted_migration_can_resume(self):
        old = {"started_at": "2026-10-31T00:00:00Z", "sessions": []}
        export.atomic_json(self.dest / "index.json", old)
        export.atomic_json(self.dest / "api-index.json", old)
        summary, _ = self.run_sync()
        self.assertEqual(summary["successful_tables"], 2)

    def test_failed_snapshot_preserves_previous_file_and_checkpoint(self):
        _, old_index = self.run_sync()
        old_entry = next(e for e in old_index["tables"] if e["table"] == self.first_table)
        before = (self.dest / old_entry["file"]).read_bytes()
        self.args.full = True
        with patch.object(local.os, "fsync", side_effect=OSError("disk failed")):
            with self.assertRaises(OSError):
                self.run_sync()
        self.assertEqual((self.dest / old_entry["file"]).read_bytes(), before)
        self.assertEqual(json.loads((self.dest / "index.json").read_text()), old_index)

    def test_one_table_failure_keeps_file_and_records_failure(self):
        _, old_index = self.run_sync()
        previous = next(e for e in old_index["tables"] if e["table"] == self.first_table)
        before = (self.dest / previous["file"]).read_bytes()
        self.args.full = True
        real_snapshot = local.snapshot

        def selective_failure(db, table, path):
            if table == self.first_table:
                raise OSError("disk failure")
            return real_snapshot(db, table, path)

        with patch.object(local, "snapshot", side_effect=selective_failure):
            summary, index = self.run_sync()
        self.assertEqual(summary["failed_tables"], 1)
        self.assertEqual(summary["successful_tables"], 1)
        failed = index["failures"][0]
        self.assertEqual(failed["last_success_at"], previous["last_success_at"])
        self.assertEqual((self.dest / previous["file"]).read_bytes(), before)
        self.assertEqual(index["status"], "completed_with_failures")

    def test_unreadable_database_reported_and_prior_entries_preserved(self):
        self.run_sync()
        self.db_path.write_bytes(b"broken sqlite")
        summary, index = self.run_sync()
        self.assertEqual(summary["database_failures"], 1)
        self.assertEqual(summary["failed_tables"], 2)
        self.assertEqual(len(index["tables"]), 2)
        self.assertEqual(index["status"], "completed_with_failures")

    def test_full_forces_reread(self):
        self.run_sync()
        self.args.full = True
        summary, _ = self.run_sync()
        self.assertEqual(summary["rewritten_tables"], 2)

    def test_deleted_table_is_reported_and_file_retained(self):
        _, old = self.run_sync()
        with closing(sqlite3.connect(self.db_path)) as db, db:
            db.execute('DROP TABLE "' + self.other_table + '"')
        _, index = self.run_sync()
        self.assertEqual(len(index["missing_tables"]), 1)
        entry = index["missing_tables"][0]
        self.assertTrue((self.dest / entry["file"]).exists())
        _, again = self.run_sync()
        self.assertEqual(len(again["missing_tables"]), 1)

    def test_read_only_connection_cannot_write(self):
        db = local.read_only(self.db_path)
        try:
            with self.assertRaises(sqlite3.OperationalError):
                db.execute('DELETE FROM "' + self.first_table + '"')
        finally:
            db.close()


if __name__ == "__main__":
    unittest.main()
