import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import MagicMock, patch
from catalog_probe import Journal, choose, extract_html, identity, movie, specifications
from run import append, records


def claim(value):
    return {"rank": "normal", "mainsnak": {"datavalue": {"value": value}}}


class CatalogTests(unittest.TestCase):
    def test_seeded_selection_dedup_and_release_gate(self):
        rows = [{"id": i, "title": "Кино", "adult": False, "release_date": "1999-01-01", "genre_ids": [i % 5]} for i in range(1, 21)]
        self.assertEqual(choose(rows, "old", set()), choose(rows, "old", set()))
        used = {1, 2, 3}
        selected = choose(rows + rows, "old", used)
        self.assertEqual(len(selected), 10)
        self.assertEqual(len({m["id"] for m in selected}), 10)
        self.assertFalse({1, 2, 3} & {m["id"] for m in selected})
        for row in ({**rows[0], "release_date": "2027-01-01"}, {**rows[0], "adult": True}, {**rows[0], "id": True}):
            self.assertIsNone(movie(row))
        self.assertEqual(len(specifications()), 7)

    def test_ids_title_year_and_article_are_independent_gates(self):
        m = {"id": 603, "title": "Матрица", "original_title": "The Matrix", "year": 1999}
        link = {"qid": "Q1", "imdb": "tt123"}
        e = {"id": "Q1", "labels": {"ru": {"language": "ru", "value": "Матрица"}},
             "claims": {"P4947": [claim("603")], "P345": [claim("tt123")], "P577": [claim({"time": "+1999-03-01T00:00:00Z"})]},
             "sitelinks": {"ruwiki": {"title": "Матрица (фильм)"}}}
        self.assertEqual(identity(m, link, e)["status"], "matched")
        wrong = json.loads(json.dumps(e));wrong["claims"]["P4947"] = [claim("604")]
        self.assertEqual(identity(m, link, wrong)["status"], "id_mismatch_or_missing")
        self.assertEqual(identity({**m, "year": 2020}, link, e)["status"], "needs_identity_review")
        self.assertEqual(identity({**m, "title": "Другой", "original_title": "Another"}, link, e)["status"], "needs_identity_review")
        self.assertEqual(identity(m, link, {**e, "sitelinks": {}})["status"], "no_ru_article")

    def test_html_identity_metadata_and_hidden_text(self):
        body = '"wgWikibaseItemId":"Q1","wgRevisionId":123<h2>Съёмки</h2><p>' + "Слово " * 22 + '<style>CSS</style>конец.</p>'
        result = extract_html(body)
        self.assertEqual((result["qid"], result["revision"]), ("Q1", 123))
        self.assertEqual(result["eligible_paragraphs"], 1)
        self.assertNotIn("CSS", result["selected"][0]["text"])
        self.assertNotIn("html", result)

    def test_journal_reserves_errors_does_not_retry_and_hides_credentials(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp);j = Journal(root, "catalog")
            opener = MagicMock();opener.open.side_effect = OSError("private upstream detail")
            with patch("catalog_probe.urllib.request.build_opener", return_value=opener), patch("catalog_probe.time.sleep"):
                first = j.get("a", "https://api.themoviedb.org/3/movie/1", {"Authorization": "Bearer secret-value"})
                self.assertEqual(first, j.get("a", "https://api.themoviedb.org/3/movie/1"))
                self.assertEqual(opener.open.call_count, 1)
            saved = "".join(p.read_text() for p in (root / "catalog").glob("*.jsonl"))
            self.assertNotIn("secret-value", saved)
            self.assertNotIn("private upstream detail", saved)
            self.assertEqual(len(records(root / "catalog" / "attempts.jsonl")), 1)
            for i in range(6):append(root / "catalog" / "attempts.jsonl", {"id": str(i)})
            with self.assertRaises(ValueError):j.get("new", "https://api.themoviedb.org/3/movie/2")


if __name__ == "__main__":
    unittest.main()
