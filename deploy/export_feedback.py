#!/usr/bin/env python3
"""Export observed reaction feedback from a read-only SQLite snapshot as JSONL."""

import argparse
from contextlib import closing
from datetime import date, datetime, timezone
import json
from pathlib import Path
import sqlite3
import sys


POST_COLUMNS = """id,chat_id,message_id,publication,published_at,kind,item_id,
content_key,text,source,image,source_evidence,ai_provider,ai_model,generation_policy,
counter_mode,partial,last_event_at,last_update_id"""


def export(db_path, output, *, chat_id=None, kind=None, publication="scheduled", since=0):
    """Include unreacted posts and event history; never include participant identities."""
    uri = Path(db_path).resolve().as_uri() + "?mode=ro"
    with closing(sqlite3.connect(uri, uri=True, timeout=5)) as conn:
        conn.row_factory = sqlite3.Row
        # Counts and events must be from the same snapshot even while the bot runs.
        conn.execute("BEGIN")
        predicates = ["published_at>=?"]
        params = [since]
        for column, value in (("chat_id", chat_id), ("kind", kind), ("publication", publication)):
            if value is not None:
                predicates.append(column + "=?")
                params.append(value)
        query = "SELECT " + POST_COLUMNS + " FROM feedback_posts WHERE " + " AND ".join(predicates) + " ORDER BY id"
        for row in conn.execute(query, params):
            post = dict(row)
            post["partial"] = bool(post["partial"])
            counts = [dict(r) for r in conn.execute(
                "SELECT reaction_type AS type,reaction_value AS value,count FROM feedback_counts WHERE post_id=? ORDER BY reaction_type,reaction_value",
                (post["id"],),
            )]
            events = []
            for r in conn.execute(
                "SELECT update_id,mode,occurred_at,received_at,before_json,after_json,applied FROM feedback_events WHERE post_id=? ORDER BY occurred_at,update_id",
                (post["id"],),
            ):
                event = dict(r)
                event["before"] = json.loads(event.pop("before_json"))
                event["after"] = json.loads(event.pop("after_json"))
                event["applied"] = bool(event["applied"])
                events.append(event)
            result = {"schema_version": 1, "coverage": "observed_updates_only", "post": post, "reactions": counts, "events": events}
            output.write(json.dumps(result, ensure_ascii=False) + "\n")


def since_date(value):
    day = date.fromisoformat(value)
    return int(datetime(day.year, day.month, day.day, tzinfo=timezone.utc).timestamp())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--db", required=True, help="Existing database or consistent backup; opened read-only")
    parser.add_argument("--chat-id", type=int)
    parser.add_argument("--kind", choices=("meme", "fact"))
    parser.add_argument("--publication", choices=("scheduled", "preview", "all"), default="scheduled")
    parser.add_argument("--since", type=since_date, default=0, metavar="YYYY-MM-DD", help="UTC publication date")
    args = parser.parse_args()
    publication = None if args.publication == "all" else args.publication
    try:
        export(args.db, sys.stdout, chat_id=args.chat_id, kind=args.kind, publication=publication, since=args.since)
    except (sqlite3.Error, OSError):
        parser.exit(1, "Не удалось прочитать статистику реакций. Проверьте путь, права и версию схемы БД.\n")


if __name__ == "__main__":
    main()
