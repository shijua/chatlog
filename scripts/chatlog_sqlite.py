"""Read-only snapshots of every macOS WeChat 3.x Chat_<hash> table."""
import base64
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3

from chatlog_export import atomic_json

TABLE = re.compile(r"Chat_([0-9a-fA-F]{32})\Z")
DATABASE = re.compile(r"msg_(?:[0-9]{1,2})?\.db\Z")


def read_only(path):
    db = sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True, timeout=30)
    try:
        db.execute("PRAGMA query_only=ON")
        db.execute("BEGIN")
        return db
    except Exception:
        db.close()
        raise


def signature(path):
    result = {}
    for suffix in ("", "-wal", "-journal"):
        source = Path(str(path) + suffix)
        try:
            stat = source.stat()
            result[suffix or "db"] = [stat.st_dev, stat.st_ino, stat.st_size, stat.st_mtime_ns, stat.st_ctime_ns]
        except FileNotFoundError:
            result[suffix or "db"] = None
    return result


def quote(name):
    return '"' + name.replace('"', '""') + '"'


def identities(root):
    users = {}
    warnings = []
    specs = [
        ("wccontact_new2.db", "WCContact", "m_nsUsrName", "nickname", "m_nsRemark"),
        ("group_new.db", "GroupMember", "m_nsUsrName", "nickname", None),
        ("group_new.db", "GroupContact", "m_nsUsrName", "nickname", "m_nsRemark"),
        ("session_new.db", "SessionAbstract", "m_nsUsrName", None, None),
    ]
    for filename, table, column, nickname, remark in specs:
        for path in sorted(root.rglob(filename)):
            try:
                db = read_only(path)
                try:
                    columns = {row[1] for row in db.execute("PRAGMA table_info(" + quote(table) + ")")}
                    if column not in columns:
                        continue
                    selected = [column] + [n for n in (nickname, remark) if n and n in columns]
                    rows = db.execute("SELECT " + ",".join(map(quote, selected)) + " FROM " + quote(table))
                    for row in rows:
                        value = dict(zip(selected, row))
                        user = value[column]
                        if not isinstance(user, str) or not user:
                            continue
                        key = hashlib.md5(user.encode()).hexdigest()
                        candidate = users.setdefault(key, {"talker": user, "nickname": "", "remark": ""})
                        if candidate["talker"] != user:
                            raise ValueError("ambiguous identity hash")
                        if nickname and value.get(nickname):
                            candidate["nickname"] = value[nickname]
                        if remark and value.get(remark):
                            candidate["remark"] = value[remark]
                finally:
                    db.close()
            except (sqlite3.Error, OSError):
                warnings.append({"database": str(path.relative_to(root)), "table": table, "error": "identity lookup failed"})
    return users, warnings


def encoded(value):
    if isinstance(value, bytes):
        return {"$sqlite_blob": base64.b64encode(value).decode("ascii")}
    return value


def snapshot(db, table, path):
    columns = [row[1] for row in db.execute("PRAGMA table_info(" + quote(table) + ")")]
    if "mesLocalID" not in columns:
        raise ValueError("message table lacks mesLocalID")
    tmp = path.with_name(path.name + ".tmp")
    count = 0
    lower = upper = None
    digest = hashlib.sha256()
    try:
        with tmp.open("wb") as output:
            def write(text):
                data = text.encode("utf-8")
                output.write(data)
                digest.update(data)

            write("[")
            for row in db.execute("SELECT * FROM " + quote(table) + " ORDER BY mesLocalID"):
                value = {key: encoded(item) for key, item in zip(columns, row)}
                write(("," if count else "") + "\n")
                write(json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(",", ":")))
                stamp = value.get("msgCreateTime")
                if isinstance(stamp, (int, float)):
                    lower = stamp if lower is None else min(lower, stamp)
                    upper = stamp if upper is None else max(upper, stamp)
                count += 1
            write("\n]\n")
            output.flush()
            os.fsync(output.fileno())
        tmp.replace(path)
    finally:
        tmp.unlink(missing_ok=True)
    return {"message_count": count, "sha256": digest.hexdigest(), "bytes": path.stat().st_size,
            "columns": columns, "first_timestamp": lower, "last_timestamp": upper}


def checkpoint(dest, index):
    entries = index["tables"]
    index["updated_at"] = datetime.now(timezone.utc).isoformat()
    index["successful_tables"] = sum(e["status"] == "success" for e in entries)
    index["total_messages"] = sum(e.get("message_count", 0) for e in entries)
    index["failures"] = [e for e in entries if e["status"] == "failed"]
    index["unresolved_tables"] = [e["id"] for e in entries if not e.get("talker")]
    atomic_json(dest / "index.json", index)


def flatten_exports(dest, index):
    entries = index.get("tables", []) + index.get("missing_tables", [])
    nested = dest / "tables"
    if nested.is_symlink():
        raise ValueError("symlink export directories are not supported")
    moves = []
    for entry in entries:
        relative = Path(entry["file"])
        if len(relative.parts) == 1:
            continue
        if len(relative.parts) != 2 or relative.parts[0] != "tables":
            raise ValueError("invalid nested export path")
        source = dest / relative
        target = dest / relative.name
        if source.is_symlink() or target.is_symlink():
            raise ValueError("symlink exports are not supported")
        if source.exists() and target.exists():
            raise ValueError("flat export destination already exists")
        if not source.exists() and target.exists():
            # A prior process may have moved the file before saving the index.
            with target.open("rb") as f:
                digest = hashlib.file_digest(f, "sha256").hexdigest()
            if digest != entry.get("sha256"):
                raise ValueError("interrupted migration destination checksum mismatch")
        moves.append((entry, source, target))
    for entry, source, target in moves:
        if source.exists():
            source.rename(target)
        entry["file"] = target.name
    if moves:
        atomic_json(dest / "index.json", index)
    if nested.is_dir() and not any(nested.iterdir()):
        nested.rmdir()


def sync_tables(args, run_at=None):
    dest = args.output.resolve()
    config_path = Path.home() / ".chatlog/chatlog-server.json"
    work_dir = args.work_dir
    if work_dir is None:
        config = json.loads(config_path.read_text())
        if config.get("platform") != "darwin" or config.get("version") != 3:
            raise ValueError("database export currently supports macOS WeChat 3.x only")
        work_dir = Path(config["work_dir"])
    root = work_dir.resolve()
    if not root.is_dir():
        raise ValueError("decrypted work directory does not exist")
    paths = sorted(p for p in root.rglob("msg_*.db") if DATABASE.fullmatch(p.name))
    if not paths:
        raise ValueError("no decrypted message databases found")
    dest.mkdir(mode=0o700, parents=True, exist_ok=True)
    index_path = dest / "index.json"
    previous = json.loads(index_path.read_text()) if index_path.exists() else {}
    if previous and previous.get("source_type") != "sqlite_tables":
        legacy = dest / "api-index.json"
        if legacy.exists() and json.loads(legacy.read_text()) != previous:
            raise ValueError("API index already exists; refusing to overwrite migration")
        atomic_json(legacy, previous)
        previous = {}
    if previous and previous.get("work_dir") != str(root):
        raise ValueError("work directory changed; choose a separate output directory")
    if previous:
        flatten_exports(dest, previous)
    old = {e["id"]: e for e in previous.get("missing_tables", []) + previous.get("tables", [])}
    users, warnings = identities(root)
    # Keep resolved identities even after optional legacy exports are removed.
    for entry in old.values():
        user = entry.get("talker")
        if user:
            users.setdefault(hashlib.md5(user.encode()).hexdigest(), {
                "talker": user, "nickname": entry.get("nickname", ""), "remark": entry.get("remark", "")})
    # Also use previously exported identifiers; identity lookup never reads message content.
    for listing in (dest.parent / "chatlog-session.json", dest / "api-index.json"):
        if not listing.exists():
            continue
        obj = json.loads(listing.read_text())
        for entry in obj.get("items", obj.get("sessions", [])):
            user = entry.get("userName", entry.get("talker"))
            if user:
                users.setdefault(hashlib.md5(user.encode()).hexdigest(), {
                    "talker": user, "nickname": entry.get("nickName", entry.get("nickname", "")), "remark": ""})
    summary = {"started_at": (run_at or datetime.now(timezone.utc)).isoformat(),
               "successful_tables": 0, "failed_tables": 0, "rewritten_tables": 0, "skipped_tables": 0}
    index = {"schema_version": 2, "source_type": "sqlite_tables", "work_dir": str(root),
             "status": "in_progress", "started_at": previous.get("started_at", summary["started_at"]),
             "tables": [], "database_failures": [], "identity_warnings": warnings, "last_run": summary,
             "legacy_api_index": "api-index.json" if (dest / "api-index.json").exists() else None}
    seen = set()
    failed_databases = set()
    for source in paths:
        relative = source.relative_to(root).as_posix()
        try:
            before = signature(source)
            db = read_only(source)
            try:
                tables = sorted(row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'") if TABLE.fullmatch(row[0]))
                for table in tables:
                    table_hash = TABLE.fullmatch(table).group(1).lower()
                    identity = relative + ":" + table
                    seen.add(identity)
                    filename = hashlib.sha256(relative.encode()).hexdigest()[:16] + "_" + table_hash + ".json"
                    entry = dict(old.get(identity, {}))
                    entry.update(id=identity, database=relative, table=table, table_hash=table_hash, file=filename,
                                 talker=None, nickname="", remark="", kind="unresolved")
                    if table_hash in users:
                        entry.update(users[table_hash])
                        entry["kind"] = "group" if entry["talker"].endswith("@chatroom") else "non_group"
                    file = dest / filename
                    try:
                        if file.is_symlink():
                            raise ValueError("symlink exports are not supported")
                        can_skip = (not args.full and entry.get("status") == "success"
                                    and entry.get("database_signature") == before and file.exists()
                                    and file.stat().st_size == entry.get("bytes"))
                        if can_skip:
                            summary["skipped_tables"] += 1
                            entry["last_action"] = "unchanged"
                        else:
                            entry.update(snapshot(db, table, file))
                            entry["database_signature"] = before if signature(source) == before else None
                            entry["last_action"] = "snapshot"
                            summary["rewritten_tables"] += 1
                        entry.update(status="success", last_success_at=summary["started_at"])
                        entry.pop("error", None)
                        summary["successful_tables"] += 1
                    except Exception as error:
                        entry.update(status="failed", error=type(error).__name__)
                        summary["failed_tables"] += 1
                    index["tables"].append(entry)
                checkpoint(dest, index)
            finally:
                db.close()
        except (sqlite3.Error, OSError) as error:
            failed_databases.add(relative)
            index["database_failures"].append({"database": relative, "error": type(error).__name__})
        if not args.quiet:
            print(json.dumps({"databases_processed": paths.index(source) + 1, **summary}), flush=True)
    for identity, entry in old.items():
        if identity in seen:
            continue
        if entry["database"] in failed_databases:
            retained = dict(entry, status="failed", error="database discovery failed")
            index["tables"].append(retained)
            summary["failed_tables"] += 1
    index["missing_tables"] = [entry for key, entry in old.items() if key not in seen and entry["database"] not in failed_databases]
    index["table_count"] = len(seen)
    index["unique_table_hashes"] = len({e["table_hash"] for e in index["tables"]})
    summary["completed_at"] = datetime.now(timezone.utc).isoformat()
    index["status"] = "completed_with_failures" if summary["failed_tables"] or index["database_failures"] else "completed"
    index["completed_at"] = summary["completed_at"]
    checkpoint(dest, index)
    summary.update(total_messages=index["total_messages"], table_count=index["table_count"],
                   unresolved_tables=len(index["unresolved_tables"]), database_failures=len(index["database_failures"]))
    checkpoint(dest, index)
    return summary
