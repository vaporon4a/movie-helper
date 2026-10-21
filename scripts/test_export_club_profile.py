import io
import json
import sqlite3
import unittest

from export_club_profile import export_data, write_csv


class ProfileExportTest(unittest.TestCase):
    def test_chat_isolation_unknown_total_and_csv_formula(self):
        db = sqlite3.connect(":memory:")
        self.addCleanup(db.close)
        db.executescript("""
            CREATE TABLE club_watches(id,chat_id,tmdb_id);
            CREATE TABLE club_review_sessions(id,chat_id);
            CREATE TABLE club_profile_polls(id,chat_id,session_id,question_json,result_json,state);
            CREATE TABLE club_films(tmdb_id);
            CREATE TABLE club_watch_audit(watch_id,revision);
            CREATE TABLE club_poll_cleanup(poll_id,chat_id);
            INSERT INTO club_watches VALUES(1,-1,603),(2,-2,604);
            INSERT INTO club_films VALUES(603),(604);
            INSERT INTO club_watch_audit VALUES(1,1),(2,1);
            INSERT INTO club_review_sessions VALUES(1,-1),(2,-2);
            INSERT INTO club_poll_cleanup VALUES(1,-1),(2,-2);
        """)
        question = {"text": "=unsafe", "options": [{"key": "x", "text": "Film", "substantive": True}]}
        db.execute("INSERT INTO club_profile_polls VALUES(1,-1,NULL,?,NULL,'open')", (json.dumps(question),))
        db.execute("INSERT INTO club_profile_polls VALUES(2,-2,NULL,?,NULL,'open')", (json.dumps(question),))
        db.commit()
        data = export_data(db, -1)
        self.assertEqual(data["films"], [{"tmdb_id": 603}])
        self.assertEqual(len(data["polls"]), 1)
        self.assertEqual(data["watch_audit"], [{"watch_id": 1, "revision": 1}])
        output = io.StringIO()
        write_csv(data, output)
        self.assertIn("'=unsafe", output.getvalue())
        self.assertIn("open,,,x,Film,True,", output.getvalue())


if __name__ == "__main__":
    unittest.main()
