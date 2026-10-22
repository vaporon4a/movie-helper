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
        db.executescript("""
            CREATE TABLE film_research_preparations(id,chat_id,dossier_id);
            CREATE TABLE film_research_approvals(preparation_id,audit_json);
            CREATE TABLE film_research_jobs(session_id,chat_id);
            CREATE TABLE film_research_dossiers(id);
            CREATE TABLE film_research_sources(id);
            CREATE TABLE film_research_dossier_sources(dossier_id,source_id);
            INSERT INTO film_research_preparations VALUES('p1',-1,'d1'),('p2',-2,'d2');
            INSERT INTO film_research_approvals VALUES('p1','{}'),('p2','{}');
            INSERT INTO film_research_jobs VALUES(1,-1),(2,-2);
            INSERT INTO film_research_dossiers VALUES('d1'),('d2');
            INSERT INTO film_research_sources VALUES('s1'),('s2');
            INSERT INTO film_research_dossier_sources VALUES('d1','s1'),('d2','s2');
        """)
        question = {"text": "=unsafe", "options": [{"key": "x", "text": "Film", "substantive": True}]}
        db.execute("INSERT INTO club_profile_polls VALUES(1,-1,NULL,?,NULL,'open')", (json.dumps(question),))
        db.execute("INSERT INTO club_profile_polls VALUES(2,-2,NULL,?,NULL,'open')", (json.dumps(question),))
        db.commit()
        data = export_data(db, -1)
        self.assertEqual(data["films"], [{"tmdb_id": 603}])
        self.assertEqual(len(data["polls"]), 1)
        self.assertEqual(data["watch_audit"], [{"watch_id": 1, "revision": 1}])
        self.assertEqual(data["film_research_approvals"], [{"preparation_id": "p1", "audit_json": "{}"}])
        self.assertEqual(data["film_research_sources"], [{"id": "s1"}])
        self.assertEqual(data["film_research_dossiers"], [{"id": "d1"}])
        self.assertEqual(data["film_research_jobs"], [{"session_id": 1, "chat_id": -1}])
        output = io.StringIO()
        write_csv(data, output)
        self.assertIn("'=unsafe", output.getvalue())
        self.assertIn("open,,,x,Film,True,", output.getvalue())


if __name__ == "__main__":
    unittest.main()
