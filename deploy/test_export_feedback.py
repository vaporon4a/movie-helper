import io
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

from export_feedback import export, since_date


class FeedbackExportTests(unittest.TestCase):
    def test_snapshot_filters_history_and_no_reaction_posts(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "feedback #1.db"
            conn = sqlite3.connect(path)
            conn.execute("CREATE TABLE items(id INTEGER)")
            migration = Path(__file__).resolve().parents[1] / "internal/storage/migrations/00018_reaction_feedback.sql"
            conn.executescript(migration.read_text().split("-- +goose Down")[0])
            sql = """INSERT INTO feedback_posts(chat_id,message_id,publication,published_at,kind,
             content_key,text,source,image,source_evidence,ai_provider,ai_model,generation_policy)
             VALUES(?,?,?,100,'meme','key','Мем','https://redd.it/test','image','{}','gemini','prepared-model','meme-v4')"""
            conn.executemany(sql, [(-1, 99, "scheduled"), (-1, 100, "scheduled"), (-1, 101, "preview"), (-2, 99, "scheduled")])
            conn.execute("INSERT INTO feedback_events VALUES(123,1,'delta',110,112,'[]',?,1)", (json.dumps([{"type": "emoji", "value": "👍", "count": 1}]),))
            conn.execute("INSERT INTO feedback_counts VALUES(1,'emoji','👍',1)")
            conn.commit()
            conn.close()
            before = path.read_bytes()
            output = io.StringIO()
            export(path, output, chat_id=-1)
            data = [json.loads(line) for line in output.getvalue().splitlines()]
            self.assertEqual([99, 100], [row["post"]["message_id"] for row in data])
            self.assertEqual("prepared-model", data[0]["post"]["ai_model"])
            self.assertEqual("👍", data[0]["events"][0]["after"][0]["value"])
            self.assertEqual([], data[1]["events"])
            self.assertEqual([], data[1]["reactions"])
            self.assertEqual("observed_updates_only", data[0]["coverage"])
            self.assertEqual(before, path.read_bytes())
            output = io.StringIO()
            export(path, output, chat_id=-1, publication="preview")
            self.assertEqual(101, json.loads(output.getvalue())["post"]["message_id"])
            output = io.StringIO()
            export(path, output, since=101)
            self.assertEqual("", output.getvalue())

    def test_missing_database_is_not_created(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "missing.db"
            with self.assertRaises(sqlite3.OperationalError):
                export(path, io.StringIO())
            self.assertFalse(path.exists())

    def test_since_is_utc(self):
        self.assertEqual(86400, since_date("1970-01-02"))


if __name__ == "__main__":
    unittest.main()
