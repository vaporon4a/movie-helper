import unittest
import json
from unittest.mock import MagicMock, patch
from extractive import id_schema, materialize
from holdout_capture import RussianParagraphs
from run import request


class ExtractiveTests(unittest.TestCase):
    def test_id_cannot_add_or_rewrite_text(self):
        rows = [{"id": "a", "excerpt": "Неизменный текст источника."}]
        self.assertEqual(materialize({"selected_id": "a"}, rows)["hook"], rows[0]["excerpt"])
        for selection in ({"selected_id": "b"}, {"selected_id": "a", "hook": "Выдумка"},
                          {"selected_id": None}, {"selected_id": []}, ["a"]):
            with self.assertRaises(ValueError):
                materialize(selection, rows)

    def test_none_contains_no_fact(self):
        result = materialize({"selected_id": "NONE"}, [{"id": "a", "excerpt": "Факт"}])
        self.assertEqual((result["hook"], result["evidence"]), ("", ""))
        self.assertEqual(id_schema([{"id": "a"}])["properties"]["selected_id"]["enum"], ["a", "NONE"])
        self.assertFalse(id_schema([])["additionalProperties"])

    def test_russian_production_and_citation_boundaries(self):
        parser = RussianParagraphs()
        parser.feed('<h2>Сюжет</h2><p>Спойлер.</p><h2>Работа над фильмом</h2>'
                    '<p>История<sup>[4]</sup><style>.bad{display:none}</style> съёмок.</p><h3>Актёры</h3><p>Продолжение.</p>'
                    '<h2>Критика</h2><p>Рецензия.</p>')
        self.assertEqual([p for _, p in parser.paragraphs], ["История съёмок.", "Продолжение."])

    def test_transport_uses_id_only_schema_for_both_providers(self):
        rows = [{"id": "a", "excerpt": "Исходник"}]
        for provider in ("gemini", "groq"):
            raw = {"candidates": [{"finishReason": "STOP", "content": {"parts": [{"text": '{"selected_id":"a"}'}]}}]} if provider == "gemini" else {"choices": [{"finish_reason": "stop", "message": {"content": '{"selected_id":"a"}'}}]}
            opener = MagicMock()
            opener.open.return_value.__enter__.return_value.read.return_value = json.dumps(raw).encode()
            with patch("run.urllib.request.build_opener", return_value=opener):
                selection, _ = request(provider, "test-model", "test-key", "select", rows, 5,
                                       reasoning_effort="low", response_schema=id_schema(rows))
            payload = json.loads(opener.open.call_args.args[0].data)
            actual = payload["generationConfig"]["responseJsonSchema"] if provider == "gemini" else payload["response_format"]["json_schema"]["schema"]
            self.assertEqual(actual, id_schema(rows))
            self.assertEqual(selection, {"selected_id": "a"})


if __name__ == "__main__":
    unittest.main()
