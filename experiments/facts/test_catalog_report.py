import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from catalog_report import summarize
from run import digest


class ReportTests(unittest.TestCase):
    def test_denominators_include_missing_sources_and_tampering_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            catalog = {"movies": [{"id": i, "group": "old", "genres": [18]} for i in (1, 2, 3)], "coverage": [{"group": "old"}]}
            external = {"catalog_sha256": digest(catalog), "rows": [{"movie_id": i, "qid": "Q" + str(i)} for i in (1, 2, 3)]}
            mapping = {"catalog_sha256": digest(catalog), "external_sha256": digest(external), "rows": [{"movie_id": 1, "status": "matched"}, {"movie_id": 2, "status": "no_ru_article"}, {"movie_id": 3, "status": "needs_identity_review"}]}
            wiki = {"catalog_sha256": digest(catalog), "wikidata_sha256": digest(mapping), "rows": [{"movie_id": 1, "status": "captured", "production_paragraphs": 4, "eligible_paragraphs": 2}, {"movie_id": 2, "status": "not_resolved"}, {"movie_id": 3, "status": "not_resolved"}],
                    "passages": [{"id": "1-1", "movie_id": 1, "text": "Исходный факт.", "sha256": hashlib.sha256("Исходный факт.".encode()).hexdigest()}]}
            for name, data in (("catalog", catalog), ("external", external), ("wikidata", mapping), ("wikipedia", wiki)):
                (root / (name + ".json")).write_text(json.dumps(data))
            report, *_ = summarize(root)
            self.assertEqual(report["totals"], {"selected": 3, "qid": 3, "identity_verified": 2, "ru_link": 1, "captured": 1, "production": 1, "eligible": 1})
            self.assertEqual(report["groups"][0]["selected"], 3)
            wiki["passages"][0]["text"] = "Выдуманный новый текст."
            (root / "wikipedia.json").write_text(json.dumps(wiki))
            with self.assertRaises(ValueError):summarize(root)
            external["catalog_sha256"] = "wrong"
            (root / "external.json").write_text(json.dumps(external))
            with self.assertRaises(ValueError):summarize(root)


if __name__ == "__main__":
    unittest.main()
