#!/usr/bin/env python3
"""Export one club's aggregate evidence from a read-only SQLite snapshot."""

import argparse
import csv
import json
import os
from pathlib import Path
import sqlite3


def export_data(connection, chat_id):
    connection.row_factory = sqlite3.Row
    connection.execute("BEGIN")
    statements = {
        "watches": "SELECT * FROM club_watches WHERE chat_id=? ORDER BY id",
        "sessions": "SELECT * FROM club_review_sessions WHERE chat_id=? ORDER BY id",
        "polls": "SELECT * FROM club_profile_polls WHERE chat_id=? ORDER BY id",
        "films": "SELECT * FROM club_films WHERE tmdb_id IN (SELECT tmdb_id FROM club_watches WHERE chat_id=?) ORDER BY tmdb_id",
        "watch_audit": "SELECT a.* FROM club_watch_audit a JOIN club_watches w ON w.id=a.watch_id WHERE w.chat_id=? ORDER BY a.watch_id,a.revision",
        "cleanup": "SELECT * FROM club_poll_cleanup WHERE chat_id=? ORDER BY poll_id",
    }
    data = {"schema": "club-profile-export-v1", "chat_id": chat_id}
    for key, query in statements.items():
        data[key] = [dict(row) for row in connection.execute(query, (chat_id,))]
    tables = {row[0] for row in connection.execute("SELECT name FROM sqlite_master WHERE type='table'")}
    research_queries = {
        "film_research_approvals": "SELECT * FROM film_research_approvals WHERE preparation_id IN (SELECT id FROM film_research_preparations WHERE chat_id=?) ORDER BY preparation_id",
        "film_research_preparations": "SELECT * FROM film_research_preparations WHERE chat_id=? ORDER BY id",
        "film_research_jobs": "SELECT * FROM film_research_jobs WHERE chat_id=? ORDER BY session_id",
        "film_research_dossiers": "SELECT * FROM film_research_dossiers WHERE id IN (SELECT dossier_id FROM film_research_preparations WHERE chat_id=?) ORDER BY id",
        "film_research_sources": "SELECT * FROM film_research_sources WHERE id IN (SELECT source_id FROM film_research_dossier_sources WHERE dossier_id IN (SELECT dossier_id FROM film_research_preparations WHERE chat_id=?)) ORDER BY id",
    }
    for table, query in research_queries.items():
        if table in tables:
            data[table] = [dict(row) for row in connection.execute(query, (chat_id,))]
    connection.rollback()
    return data


def csv_cell(value):
    # Do not let film names or question text execute spreadsheet formulas.
    if isinstance(value, str) and value.lstrip().startswith(("=", "+", "-", "@")):
        return "'" + value
    return value


def write_csv(data, stream):
    writer = csv.writer(stream)
    writer.writerow(["poll_id", "session_id", "topic", "intent", "aspect", "scale", "version", "question", "state", "total", "result_source", "option_key", "option_text", "substantive", "votes", "target", "measurement", "family", "registry", "dossier"])
    for poll in data["polls"]:
        question = json.loads(poll["question_json"])
        result = json.loads(poll["result_json"]) if poll["result_json"] else {}
        counts = result.get("counts") or []
        for index, option in enumerate(question.get("options", [])):
            row = [poll["id"], poll["session_id"], question.get("topic"), question.get("intent"), question.get("aspect"), question.get("scale"), question.get("version"), question.get("text"), poll["state"], result.get("total_voter_count"), result.get("source"), option.get("key"), option.get("text"), option.get("substantive"), counts[index] if index < len(counts) else None]
            descriptor = question.get("measurement") or {}
            row.extend(descriptor.get(key) for key in ("target_id", "measurement_id", "comparison_family_id", "comparison_registry_version", "dossier_id"))
            writer.writerow([csv_cell(value) for value in row])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--db", type=Path, required=True)
    parser.add_argument("--chat", type=int, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--format", choices=("json", "csv"), default="json")
    args = parser.parse_args()
    with sqlite3.connect(args.db.resolve().as_uri() + "?mode=ro", uri=True) as connection:
        data = export_data(connection, args.chat)
    descriptor = os.open(args.output, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8", newline="") as stream:
        if args.format == "csv":
            write_csv(data, stream)
        else:
            json.dump(data, stream, ensure_ascii=False, indent=2)
            stream.write("\n")
    print("Export saved. No Telegram messages were sent.")


if __name__ == "__main__":
    main()
