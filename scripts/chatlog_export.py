#!/usr/bin/env python3
"""Incrementally export local chatlog API messages without changing the server."""
import argparse
from collections import Counter
from datetime import datetime, timedelta, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import time
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import ProxyHandler, build_opener

REPO = Path(__file__).resolve().parents[1]
BASE = "http://127.0.0.1:5030/api/v1/"
EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def timestamp(value):
    result = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if result.tzinfo is None:
        raise ValueError("timestamp must include a timezone")
    return result.astimezone(timezone.utc)


def atomic_json(path, value):
    tmp = path.with_name(path.name + ".tmp")
    try:
        with tmp.open("w", encoding="utf-8") as f:
            json.dump(value, f, ensure_ascii=False, indent=2)
            f.write("\n")
            f.flush()
            os.fsync(f.fileno())
        tmp.replace(path)
    finally:
        tmp.unlink(missing_ok=True)


def iter_messages(path):
    """Read JSON arrays incrementally, including pretty-printed existing files."""
    if not path.exists():
        return
    decoder = json.JSONDecoder()
    with path.open(encoding="utf-8") as f:
        buffer = ""
        eof = False

        def fill():
            nonlocal buffer, eof
            chunk = f.read(65536)
            buffer += chunk
            eof = not chunk

        def peek():
            nonlocal buffer
            while True:
                buffer = buffer.lstrip()
                if buffer or eof:
                    return buffer[:1]
                fill()

        if peek() != "[":
            raise ValueError("export is not a JSON array")
        buffer = buffer[1:]
        if peek() == "]":
            buffer = buffer[1:]
        else:
            while True:
                peek()
                while True:
                    try:
                        value, end = decoder.raw_decode(buffer)
                        break
                    except json.JSONDecodeError:
                        if eof:
                            raise ValueError("invalid or truncated message JSON") from None
                        fill()
                if not isinstance(value, dict):
                    raise ValueError("message must be a JSON object")
                buffer = buffer[end:]
                yield value
                separator = peek()
                if separator == "]":
                    buffer = buffer[1:]
                    break
                if separator != ",":
                    raise ValueError("invalid message separator")
                buffer = buffer[1:]
        if peek():
            raise ValueError("unexpected data after JSON array")


def message_key(message):
    # Display names can change; seq is not a unique ID on macOS WeChat 3.x.
    value = {k: v for k, v in message.items() if k not in ("seq", "talkerName", "senderName")}
    value["time"] = timestamp(message["time"]).isoformat()
    canonical = json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":"))
    return hashlib.sha256(canonical.encode()).digest()


def merge_messages(path, fetched, lower):
    old_counts = Counter()
    old_count = 0
    for message in iter_messages(path):
        old_count += 1
        if timestamp(message["time"]) >= lower:
            old_counts[message_key(message)] += 1
    seen = Counter()
    additions = []
    for message in fetched:
        key = message_key(message)
        seen[key] += 1
        if seen[key] > old_counts[key]:
            additions.append(message)
    additions.sort(key=lambda m: timestamp(m["time"]))
    if additions or not path.exists():
        tmp = path.with_name(path.name + ".tmp")
        try:
            with tmp.open("w", encoding="utf-8") as f:
                f.write("[")
                first = True

                def write(message):
                    nonlocal first
                    if not first:
                        f.write(",")
                    f.write("\n")
                    json.dump(message, f, ensure_ascii=False, separators=(",", ":"))
                    first = False

                pos = 0
                for message in iter_messages(path):
                    while pos < len(additions) and timestamp(additions[pos]["time"]) <= timestamp(message["time"]):
                        write(additions[pos])
                        pos += 1
                    write(message)
                for message in additions[pos:]:
                    write(message)
                f.write("\n]\n")
                f.flush()
                os.fsync(f.fileno())
            tmp.replace(path)
        finally:
            tmp.unlink(missing_ok=True)
    with path.open("rb") as f:
        digest = hashlib.file_digest(f, "sha256").hexdigest()
    return {"message_count": old_count + len(additions), "added_messages": len(additions),
            "sha256": digest, "bytes": path.stat().st_size}


class API:
    def __init__(self, page_size):
        self.page_size = page_size
        self.opener = build_opener(ProxyHandler({}))

    def get(self, endpoint, params):
        url = BASE + endpoint + "?" + urlencode(params)
        for attempt in range(3):
            try:
                with self.opener.open(url, timeout=120) as response:
                    return json.load(response)
            except Exception as error:
                if attempt == 2:
                    # Do not include response bodies or message data in errors.
                    detail = "HTTP " + str(error.code) if isinstance(error, HTTPError) else type(error).__name__
                    raise RuntimeError("API request failed: " + detail) from None
                time.sleep(attempt + 1)

    def pages(self, endpoint, params, items_key=None):
        result = []
        offset = 0
        previous = None
        requests = 0
        while True:
            obj = self.get(endpoint, dict(params, limit=self.page_size, offset=offset, format="json"))
            requests += 1
            page = obj.get(items_key) if items_key and isinstance(obj, dict) else obj
            if page is None and not items_key:
                page = []
            if not isinstance(page, list) or len(page) > self.page_size:
                raise ValueError("unexpected API page shape")
            if not page:
                return result, requests
            if any(not isinstance(item, dict) for item in page):
                raise ValueError("invalid API entry")
            digest = hashlib.sha256(json.dumps(page, sort_keys=True).encode()).digest()
            if digest == previous:
                raise ValueError("API returned the same page twice")
            previous = digest
            result.extend(page)
            offset += len(page)


def export_file(talker):
    safe = re.sub(r"[^\w.@-]", "_", talker)[:100] or "session"
    return safe + "_" + hashlib.sha256(talker.encode()).hexdigest()[:12] + ".json"


def save_index(path, index):
    entries = index["sessions"]
    index["updated_at"] = datetime.now(timezone.utc).isoformat()
    index["successful_sessions"] = sum(e["status"] == "success" for e in entries)
    index["total_messages"] = sum(e.get("message_count", 0) for e in entries)
    index["failures"] = [e for e in entries if e["status"] == "failed"]
    index.pop("verification", None)
    atomic_json(path, index)


def sync(args, api=None, run_at=None):
    dest = args.output.resolve()
    dest.mkdir(mode=0o700, parents=True, exist_ok=True)
    index_path = dest / "index.json"
    index = json.loads(index_path.read_text()) if index_path.exists() else {
        "started_at": datetime.now(timezone.utc).isoformat(), "sessions": []}
    # Freeze the upper boundary before any API reads. Each successful session advances
    # only to this boundary, so messages arriving during the run are picked up next time.
    run_at = (run_at or datetime.now(timezone.utc)).replace(microsecond=0)
    api = api or API(args.page_size)
    sessions, _ = api.pages("session", {}, "items")
    talkers = [s.get("userName") for s in sessions]
    if any(not isinstance(t, str) or not t for t in talkers) or len(set(talkers)) != len(talkers):
        raise ValueError("invalid or duplicate session identifiers")
    baseline = {e["talker"]: e for e in index["sessions"]}
    if len(baseline) != len(index["sessions"]):
        raise ValueError("duplicate talkers in index")
    new_count = 0
    for item in sessions:
        talker = item["userName"]
        if talker not in baseline:
            entry = {"talker": talker, "file": export_file(talker), "message_count": 0, "status": "pending"}
            index["sessions"].append(entry)
            baseline[talker] = entry
            new_count += 1
        baseline[talker]["nickname"] = item.get("nickName", "")
        baseline[talker]["session_nTime"] = item.get("nTime")
        baseline[talker]["session_nOrder"] = item.get("nOrder")
    source = dest.parent / "chatlog-session.json"
    atomic_json(source, {"items": sessions})
    summary = {"started_at": run_at.isoformat(), "successful_sessions": 0, "failed_sessions": 0,
               "updated_sessions": 0, "added_messages": 0, "new_sessions": new_count}
    index.update(source=str(source), page_size=args.page_size, expected_sessions=len(baseline),
                 status="in_progress", last_run=summary, time="all")
    save_index(index_path, index)
    for number, entry in enumerate(index["sessions"], 1):
        try:
            filename = entry["file"]
            if Path(filename).name != filename or filename in (".", "..", "index.json"):
                raise ValueError("invalid export filename")
            path = dest / filename
            if path.is_symlink():
                raise ValueError("symlink exports are not supported")
            if not path.exists() and entry.get("message_count", 0):
                raise ValueError("existing export file is missing")
            if args.full or not path.exists():
                lower = EPOCH
            else:
                checkpoint = entry.get("last_success_at", index["started_at"])
                lower = max(EPOCH, timestamp(checkpoint) - timedelta(hours=args.overlap_hours))
            lower = lower.replace(microsecond=0)
            if lower > run_at:
                raise ValueError("checkpoint is in the future")
            query_time = lower.isoformat(timespec="seconds") + "~" + run_at.isoformat(timespec="seconds")
            fetched, requests = api.pages("chatlog", {"talker": entry["talker"], "time": query_time})
            for message in fetched:
                if message.get("talker") != entry["talker"]:
                    raise ValueError("API returned a different talker")
                if not lower <= timestamp(message["time"]) <= run_at:
                    raise ValueError("API returned a message outside the requested interval")
            merged = merge_messages(path, fetched, lower)
            entry.update(merged, status="success", last_success_at=run_at.isoformat(),
                         last_query_start=lower.isoformat(), last_requests=requests, ended_on_empty_page=True)
            entry.pop("error", None)
            summary["successful_sessions"] += 1
            summary["added_messages"] += merged["added_messages"]
            summary["updated_sessions"] += merged["added_messages"] > 0
        except Exception as error:
            entry["status"] = "failed"
            entry["error"] = str(error) if isinstance(error, (ValueError, RuntimeError)) else type(error).__name__
            summary["failed_sessions"] += 1
        save_index(index_path, index)
        if not args.quiet and (number % 25 == 0 or entry["status"] == "failed"):
            print(json.dumps({"processed": number, **summary}), flush=True)
    summary["completed_at"] = datetime.now(timezone.utc).isoformat()
    index["status"] = "completed_with_failures" if summary["failed_sessions"] else "completed"
    index["completed_at"] = summary["completed_at"]
    save_index(index_path, index)
    return summary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=REPO / "chatlog-export")
    parser.add_argument("--overlap-hours", type=float, default=24)
    parser.add_argument("--page-size", type=int, default=10000)
    parser.add_argument("--full", action="store_true", help="reconcile all API-readable history, preserving existing records")
    parser.add_argument("--quiet", action="store_true")
    args = parser.parse_args()
    if not 1 <= args.page_size <= 10000 or not 0 < args.overlap_hours <= 876000:
        parser.error("page size must be 1..10000 and overlap hours must be positive (<=876000)")
    args.output.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (args.output / ".sync.lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            parser.exit(1, "Another export is already running.\n")
        try:
            summary = sync(args)
        except Exception as error:
            print("Export failed: " + type(error).__name__, file=sys.stderr)
            return 1
    print(json.dumps(summary, ensure_ascii=False), flush=True)
    return 1 if summary["failed_sessions"] else 0


if __name__ == "__main__":
    sys.exit(main())
