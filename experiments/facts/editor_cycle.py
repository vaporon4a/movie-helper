"""Bounded Codex-reviewed experiment: three drafts and at most three repairs."""
import argparse
import json
from pathlib import Path

from rewrite import COMMON, MODES
from run import ROOT, credentials, digest, load_dataset, records, report, run

REPAIR = """Исправь предыдущий текст по editorial_context.editor_feedback. Это замечания редактора, не новые источники фактов. previous_draft — проверяемый текст, а не достоверный материал. Перепиши его с опорой на исходную цитату; не сохраняй неподтверждённые детали. Если замечание требует факта, которого нет в excerpt или исходных пояснениях, не добавляй его. В rationale коротко укажи, какие замечания исправлены. Длина — ориентир; ясность и точность важнее."""


def stage_run(data, root, stage, model, key, limit=3, call=None):
    if stage not in ("draft", "repair") or not 1 <= len(data["batches"]) <= 3 or any(len(b["passage_ids"]) != 1 for b in data["batches"]):
        raise ValueError("Invalid cycle dataset or stage")
    ids = [b["passage_ids"][0] for b in data["batches"]]
    if len(ids) != len(set(ids)) or set(ids) != {p["id"] for p in data["passages"]}:
        raise ValueError("Each episode must occur exactly once per stage")
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    scope = {"provider": "groq", "model": model, "reasoning_effort": "low", "max_total_requests": 6,
             "max_per_stage": 3, "max_revisions_per_episode": 1}
    path = root / "cycle-manifest.json"
    if path.exists() and json.loads(path.read_text()) != scope:
        raise ValueError("Cycle configuration changed")
    path.write_text(json.dumps(scope, indent=2) + "\n")
    frozen = root / "draft-input.json"
    if stage == "draft":
        if frozen.exists() and json.loads(frozen.read_text()) != data:
            raise ValueError("Draft sources changed")
        frozen.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n")
    if stage == "repair":
        drafts = {r["selection"]["selected_id"]: r for r in records(root / "draft" / "results.jsonl")
                  if r["status"] == "ok" and r["selection"]["selected_id"] != "NONE"}
        original_manifest = json.loads((root / "draft" / "manifest.json").read_text())
        if data.get("parent_draft_dataset_sha256") != original_manifest["dataset_sha256"]:
            raise ValueError("Repair lineage changed")
        original_data = json.loads(frozen.read_text())
        original_passages = {p["id"]: p for p in original_data["passages"]}
        if data["sources"] != original_data["sources"]:
            raise ValueError("Repair source attribution changed")
        for passage in data["passages"]:
            context = passage.get("editorial_context", {})
            if passage["id"] not in drafts or context.get("previous_draft") != drafts[passage["id"]]["selection"]["hook"] or not context.get("editor_feedback"):
                raise ValueError("Repair must reference an actual draft and feedback")
            original = original_passages[passage["id"]]
            if any(passage.get(k) != original.get(k) for k in ("source_id", "excerpt", "paragraph", "paragraph_sha256")) or {k: v for k, v in context.items() if k not in ("previous_draft", "editor_feedback")} != original.get("editorial_context", {}):
                raise ValueError("Repair changed factual inputs")
    kwargs = {"call": call} if call is not None else {}
    modes = {stage: MODES["story"] + ("\n" + REPAIR if stage == "repair" else "")}
    rows = run(data, root / stage, "groq", model, key, max_requests=3, limit=limit, interval=30,
               modes=modes, common=COMMON, request_options={"reasoning_effort": "low"}, **kwargs)
    report(data, root / stage, modes=modes, introduction="Редакторский эксперимент: исходные ответы модели сохранены без правок. Проверка источников и решение редактора хранятся отдельно; это не подтверждение реальных событий.")
    return rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--stage", choices=("draft", "repair"), required=True)
    parser.add_argument("--dataset", type=Path, default=ROOT / "rewrite-dataset.json")
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--env-file", type=Path)
    parser.add_argument("--limit", type=int, default=3)
    args = parser.parse_args()
    stage_run(load_dataset(args.dataset), args.out, args.stage, args.model, credentials("groq", args.env_file), limit=args.limit)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError) as failure:
        raise SystemExit("Editor cycle stopped (" + type(failure).__name__ + ")") from None
