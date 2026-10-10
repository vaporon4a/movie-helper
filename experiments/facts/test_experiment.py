import hashlib
import copy
import json
import tempfile
import unittest
import urllib.error
from email.message import Message
from pathlib import Path

from capture import Paragraphs, window
from analyze import analyze
from editor_cycle import stage_run
from run import digest
from run import candidates, load_dataset, rate_limit_headers, records, report, run, validate_result


def fixture():
    paragraph = "The director built a rotating room. Actors walked on its walls. The camera followed them."
    return {"version": "test", "sources": [{"id": "film", "display_title": "Film <unsafe>", "url": "https://en.wikipedia.org/w/index.php?oldid=1"}],
            "passages": [{"id": "film-1", "source_id": "film", "paragraph": paragraph, "excerpt": paragraph, "paragraph_sha256": hashlib.sha256(paragraph.encode()).hexdigest()}],
            "batches": [{"id": "batch-1", "passage_ids": ["film-1"]}]}


def success(provider, model, key, prompt, rows, timeout):
    return {"selected_id": rows[0]["id"], "hook": "<script>alert(1)</script>", "evidence": rows[0]["excerpt"], "rationale": "test"}, {"prompt_tokens": 30}


class ExperimentTests(unittest.TestCase):
    def test_source_profiles_are_explicit_and_external_sources_not_cc_wikipedia(self):
        data = fixture()
        source = data["sources"][0]
        source.update(url="https://www.bfi.org.uk/interviews/test", publisher="BFI", license=None)
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            path = out / "dataset.json"
            path.write_text(json.dumps(data))
            with self.assertRaises(ValueError):
                load_dataset(path)
            load_dataset(path, allowed_source_hosts=("www.bfi.org.uk",))
            run(data, out, "groq", "test", "key", limit=1, call=success)
            report(data, out)
            page = (out / "compare.html").read_text()
            self.assertIn("BFI", page)
            self.assertNotIn("CC BY-SA", page)
            self.assertNotIn(" · Wikipedia", page)
            source["url"] = "https://ru.wikipedia.org/wiki/Movie"
            path.write_text(json.dumps(data))
            with self.assertRaises(ValueError):
                load_dataset(path, allowed_source_hosts=("ru.wikipedia.org",))

    def test_editor_cycle_rejects_repeated_episode_before_network(self):
        data = fixture()
        data["batches"].append({"id": "another", "passage_ids": ["film-1"]})
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(ValueError):
                stage_run(data, Path(directory), "draft", "test", "key", call=lambda *args, **kwargs: self.fail("Network must not be called"))

    def test_editor_repairs_keep_sources_and_actual_draft_frozen(self):
        def draft_call(*args, **kwargs):
            self.assertEqual(kwargs, {"reasoning_effort": "low"})
            return success(*args)
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            data = fixture()
            stage_run(data, out, "draft", "test", "key", call=draft_call)
            repaired = copy.deepcopy(data)
            repaired["parent_draft_dataset_sha256"] = digest(data)
            repaired["passages"][0]["editorial_context"] = {"previous_draft": "<script>alert(1)</script>", "editor_feedback": "Make readable"}
            stage_run(repaired, out, "repair", "test", "key", call=draft_call)
            before = len(records(out / "repair" / "attempts.jsonl"))
            repaired["passages"][0]["editorial_context"]["previous_draft"] = "Invented old draft"
            with self.assertRaises(ValueError):
                stage_run(repaired, out, "repair", "test", "key", call=draft_call)
            repaired["passages"][0]["editorial_context"]["previous_draft"] = "<script>alert(1)</script>"
            repaired["passages"][0]["excerpt"] += " Fabrication."
            with self.assertRaises(ValueError):
                stage_run(repaired, out, "repair", "test", "key", call=draft_call)
            self.assertEqual(len(records(out / "repair" / "attempts.jsonl")), before)

    def test_rate_limit_headers_exclude_unrelated_sensitive_values(self):
        headers = Message()
        headers["X-RateLimit-Reset-Tokens"] = "7.66s"
        headers["Retry-After"] = "120"
        headers["Set-Cookie"] = "secret"
        headers["Authorization"] = "secret"
        self.assertEqual(rate_limit_headers(headers), {"retry-after": "120", "x-ratelimit-reset-tokens": "7.66s"})

    def test_source_parser_excludes_plot_and_citations(self):
        parser = Paragraphs()
        parser.feed('<h2>Plot</h2><p>fiction</p><h2>Production</h2><p>Real <b>event</b><sup>[1]</sup>.</p><h2>Reception</h2><p>ratings</p>')
        self.assertEqual(parser.paragraphs, ["Real event."])

    def test_window_is_contiguous(self):
        text = "One event. A special set. Actors move it. A real tide rises. Another episode."
        quote = window(text, "tide")
        self.assertIn(quote, text)
        self.assertIn("tide", quote)

    def test_fixed_dataset_sources_are_verifiable(self):
        data = load_dataset(Path(__file__).with_name("dataset.json"))
        self.assertEqual(len(data["passages"]), 24)
        self.assertEqual(len(data["batches"]), 4)
        self.assertTrue(all(len(b["passage_ids"]) == 6 for b in data["batches"]))

    def test_tampered_quote_is_rejected(self):
        data = fixture()
        data["passages"][0]["excerpt"] = "a fabricated quote"
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "dataset.json"
            path.write_text(json.dumps(data))
            with self.assertRaises(ValueError):
                load_dataset(path)

    def test_source_link_cannot_be_executable_html(self):
        data = fixture()
        data["sources"][0]["url"] = "javascript:alert(1)"
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "dataset.json"
            path.write_text(json.dumps(data))
            with self.assertRaises(ValueError):
                load_dataset(path)

    def test_selection_must_quote_selected_source(self):
        rows = candidates(fixture(), fixture()["batches"][0])
        result, _ = success("", "", "", "", rows, 1)
        result["evidence"] = "This does not occur in the source."
        with self.assertRaises(ValueError):
            validate_result(result, rows)
        result["selected_id"] = "NONE"
        with self.assertRaises(ValueError):
            validate_result(result, rows)

    def test_budget_includes_failure_and_resume_does_not_repeat(self):
        calls = []
        def fail(*args):
            calls.append(args)
            raise urllib.error.HTTPError("https://example.org", 429, "secret upstream body", {"Retry-After": "120"}, None)
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            run(fixture(), out, "gemini", "test", "secret-key", max_requests=2, call=fail)
            self.assertEqual(len(calls), 1)
            run(fixture(), out, "gemini", "test", "secret-key", max_requests=2, call=success)
            self.assertEqual(len(records(out / "attempts.jsonl")), 2)
            self.assertNotIn("secret", (out / "results.jsonl").read_text())
            run(fixture(), out, "gemini", "test", "secret-key", max_requests=2, call=success)
            self.assertEqual(len(records(out / "attempts.jsonl")), 2)

    def test_crashed_attempt_is_reserved(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            (out / "attempts.jsonl").write_text('{"batch":"batch-1","mode":"A"}\n')
            run(fixture(), out, "gemini", "test", "key", max_requests=1, call=success)
            self.assertFalse((out / "results.jsonl").exists())

    def test_manifest_cannot_change_provider_or_budget(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            run(fixture(), out, "gemini", "test", "key", limit=1, call=success)
            for settings in (("groq", 12), ("gemini", 11)):
                with self.assertRaises(ValueError):
                    run(fixture(), out, settings[0], "test", "key", max_requests=settings[1], call=success)

    def test_custom_prompt_is_frozen_and_failed_jobs_not_repeated(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            modes = {"compact": "short", "story": "long"}
            run(fixture(), out, "groq", "test", "key", modes=modes, common="context", call=success)
            self.assertEqual([r["mode"] for r in records(out / "attempts.jsonl")], list(modes))
            self.assertEqual(len(report(fixture(), out, modes=modes, introduction="<new>")), 2)
            self.assertIn("&lt;new&gt;", (out / "compare.html").read_text())
            with self.assertRaises(ValueError):
                run(fixture(), out, "groq", "test", "key", modes=modes, common="changed", call=success)

    def test_ratings_join_rejects_duplicates_mismatches_and_boolean_scores(self):
        deck = [{"label": "1", "batch": "first"}, {"label": "2", "batch": "first"}]
        key = {"1": {"batch": "first", "mode": "A", "selected_id": "movie"},
               "2": {"batch": "first", "mode": "B", "selected_id": "NONE"}}
        rows = [{"label": i, "batch": "first", "interest": None, "clarity": None, "share": None, "supported": None, "comment": ""} for i in ("1", "2")]
        rows[0]["interest"] = 2
        result = analyze(rows, deck, key)
        self.assertEqual(result["summary"]["A"]["fields"]["interest"]["mean"], 2)
        self.assertIsNone(result["summary"]["A"]["fields"]["clarity"]["mean"])
        for invalid in ([rows[0], rows[0]], [dict(rows[0], batch="wrong"), rows[1]], [dict(rows[0], interest=True), rows[1]], [rows[0]]):
            with self.assertRaises(ValueError):
                analyze(invalid, deck, key)

    def test_rewrite_comparison_only_pairs_the_same_episode(self):
        deck = [{"label": "1", "batch": "first"}, {"label": "2", "batch": "first"}, {"label": "3", "batch": "second"}]
        key = {"1": {"batch": "first", "mode": "compact", "selected_id": "movie"},
               "2": {"batch": "first", "mode": "story", "selected_id": "movie"},
               "3": {"batch": "second", "mode": "compact", "selected_id": "another"}}
        rows = [{"label": r["label"], "batch": r["batch"], "interest": 1, "clarity": None, "share": 1, "supported": None, "comment": ""} for r in deck]
        rows[1]["interest"] = 2
        result = analyze(rows, deck, key, phase="rewrite")
        self.assertEqual(len(result["pair_comparisons"]), 1)
        self.assertEqual(result["pair_comparisons"][0]["story_minus_compact"], {"interest": 1, "clarity": None, "share": 0})
        key["2"]["selected_id"] = "different"
        self.assertEqual(analyze(rows, deck, key, phase="rewrite")["pair_comparisons"], [])

    def test_blind_output_escapes_text_and_labels_survive_resume(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            run(fixture(), out, "gemini", "test", "key", limit=1, call=success)
            first = report(fixture(), out)
            run(fixture(), out, "gemini", "test", "key", call=success)
            later = report(fixture(), out)
            self.assertIn(first[0], later)
            page = (out / "compare.html").read_text()
            self.assertIn("&lt;script&gt;", page)
            self.assertNotIn("<script>alert(1)</script>", page)
            self.assertTrue(all("mode" not in item for item in later))


if __name__ == "__main__":
    unittest.main()
