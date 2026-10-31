import json
from pathlib import Path
from tempfile import TemporaryDirectory
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import chatlog_export as export


def message(content="hello", when="2026-10-31T10:00:00+00:00"):
    return {"talker": "test-user", "time": when, "sender": "test-user", "isSelf": False,
            "type": 1, "subType": 0, "content": content, "seq": 0, "talkerName": "Name"}


class MergeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "chat.json"
        self.lower = export.timestamp("2026-10-30T00:00:00Z")

    def write(self, rows):
        self.path.write_text(json.dumps(rows, ensure_ascii=False, indent=2))

    def test_multiset_overlap_and_idempotence(self):
        self.write([message(), message()])
        renamed = dict(message(), talkerName="New name", senderName="New sender name")
        rows = [renamed, renamed, renamed, message("new")]
        first = export.merge_messages(self.path, rows, self.lower)
        self.assertEqual(first["message_count"], 4)
        self.assertEqual(first["added_messages"], 2)
        before = self.path.read_bytes()
        second = export.merge_messages(self.path, rows, self.lower)
        self.assertEqual(second["added_messages"], 0)
        self.assertEqual(self.path.read_bytes(), before)
        self.assertEqual(sum(m["content"] == "hello" for m in export.iter_messages(self.path)), 3)

    def test_late_message_inserted_and_old_messages_preserved(self):
        self.write([message("old", "2026-10-01T00:00:00Z"), message("latest")])
        result = export.merge_messages(self.path, [message("late", "2026-10-30T01:00:00Z")], self.lower)
        self.assertEqual(result["message_count"], 3)
        self.assertEqual([m["content"] for m in export.iter_messages(self.path)], ["old", "late", "latest"])

    def test_atomic_write_failure_keeps_old_file(self):
        self.write([message()])
        before = self.path.read_bytes()
        with patch.object(export.os, "fsync", side_effect=OSError("disk failure")):
            with self.assertRaises(OSError):
                export.merge_messages(self.path, [message(), message("new")], self.lower)
        self.assertEqual(self.path.read_bytes(), before)
        self.assertFalse(self.path.with_name("chat.json.tmp").exists())

    def test_reader_handles_large_unicode_content(self):
        rows = [message("消息" * 70000), message("second")]
        self.write(rows)
        self.assertEqual(list(export.iter_messages(self.path)), rows)

    def test_reader_rejects_truncated_or_invalid_arrays(self):
        for content in ('[{}', '[{},]', '[] garbage', '[1]', '{"items": []}'):
            with self.subTest(content=content):
                self.path.write_text(content)
                with self.assertRaises(ValueError):
                    list(export.iter_messages(self.path))


class PagingTests(unittest.TestCase):
    def test_paging_continues_past_short_page_until_empty(self):
        class Fake(export.API):
            def __init__(self):
                self.page_size = 2
                self.offsets = []

            def get(self, endpoint, params):
                self.offsets.append(params["offset"])
                return {0: [message("a")], 1: [message("b")], 2: []}[params["offset"]]

        api = Fake()
        rows, requests = api.pages("chatlog", {})
        self.assertEqual(len(rows), 2)
        self.assertEqual(requests, 3)
        self.assertEqual(api.offsets, [0, 1, 2])

    def test_repeated_page_fails_instead_of_looping(self):
        api = export.API(1)
        with patch.object(api, "get", return_value=[message()]):
            with self.assertRaisesRegex(ValueError, "same page"):
                api.pages("chatlog", {})


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.tmp = TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dest = Path(self.tmp.name) / "export"
        self.dest.mkdir()
        self.args = SimpleNamespace(output=self.dest, page_size=10000, overlap_hours=24, full=False, quiet=True)
        self.old_checkpoint = "2026-10-31T10:00:00+00:00"
        self.run_at = export.timestamp("2026-10-31T12:00:00Z")
        self.filename = export.export_file("test-user")
        (self.dest / self.filename).write_text(json.dumps([message()]))
        self.index = {"started_at": "2026-10-30T00:00:00.123456+00:00", "sessions": [
            {"talker": "test-user", "file": self.filename, "status": "success",
             "message_count": 1, "last_success_at": self.old_checkpoint}]}
        export.atomic_json(self.dest / "index.json", self.index)

    def fake_api(self, fail=False, new=False, wrong_talker=False):
        class Fake:
            def __init__(self):
                self.queries = []

            def pages(self, endpoint, params, items_key=None):
                if endpoint == "session":
                    rows = [{"userName": "test-user", "nickName": "Test"}]
                    if new:
                        rows.append({"userName": "new-user", "nickName": "New"})
                    return rows, 2
                self.queries.append(params)
                if params["talker"] == "new-user":
                    return [], 1
                if fail:
                    raise RuntimeError("API request failed: HTTP 503")
                row = message("new", "2026-10-31T11:00:00Z")
                if wrong_talker:
                    row["talker"] = "someone-else"
                return [message(), row], 2

        return Fake()

    def test_sync_discovers_sessions_and_advances_only_to_run_start(self):
        api = self.fake_api(new=True)
        summary = export.sync(self.args, api, self.run_at)
        self.assertEqual(summary["successful_sessions"], 2)
        self.assertEqual(summary["new_sessions"], 1)
        self.assertEqual(summary["added_messages"], 1)
        index = json.loads((self.dest / "index.json").read_text())
        self.assertEqual(index["total_messages"], 2)
        self.assertEqual(index["sessions"][0]["last_success_at"], self.run_at.isoformat())
        self.assertEqual(api.queries[0]["time"], "2026-10-30T10:00:00+00:00~2026-10-31T12:00:00+00:00")
        self.assertTrue(api.queries[1]["time"].startswith("1970-01-01"))
        again = export.sync(self.args, self.fake_api(new=True), self.run_at)
        self.assertEqual(again["added_messages"], 0)

    def test_failure_preserves_checkpoint_and_file_then_recovers(self):
        before = (self.dest / self.filename).read_bytes()
        summary = export.sync(self.args, self.fake_api(fail=True), self.run_at)
        self.assertEqual(summary["failed_sessions"], 1)
        index = json.loads((self.dest / "index.json").read_text())
        self.assertEqual(index["sessions"][0]["last_success_at"], self.old_checkpoint)
        self.assertEqual((self.dest / self.filename).read_bytes(), before)
        self.assertEqual(len(index["failures"]), 1)
        result = export.sync(self.args, self.fake_api(), self.run_at)
        self.assertEqual(result["failed_sessions"], 0)
        self.assertEqual(result["added_messages"], 1)

    def test_wrong_talker_never_written(self):
        before = (self.dest / self.filename).read_bytes()
        result = export.sync(self.args, self.fake_api(wrong_talker=True), self.run_at)
        self.assertEqual(result["failed_sessions"], 1)
        self.assertEqual((self.dest / self.filename).read_bytes(), before)

    def test_missing_existing_export_is_failure(self):
        (self.dest / self.filename).unlink()
        result = export.sync(self.args, self.fake_api(), self.run_at)
        self.assertEqual(result["failed_sessions"], 1)
        self.assertFalse((self.dest / self.filename).exists())

    def test_original_export_uses_start_time_not_completion(self):
        self.index["sessions"][0].pop("last_success_at")
        self.index["completed_at"] = "2026-10-31T11:00:00+00:00"
        export.atomic_json(self.dest / "index.json", self.index)
        api = self.fake_api()
        export.sync(self.args, api, self.run_at)
        self.assertTrue(api.queries[0]["time"].startswith("2026-10-29T00:00:00+00:00~"))


if __name__ == "__main__":
    unittest.main()
