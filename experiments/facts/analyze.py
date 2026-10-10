"""Validate downloaded blind ratings and retain a reproducible local analysis."""
import argparse
import json
from pathlib import Path

from run import digest

FIELDS = ("interest", "clarity", "share")


def analyze(ratings, deck, key, phase="selection"):
    if phase not in ("selection", "rewrite"):
        raise ValueError("Invalid experiment phase")
    expected = {r["label"]: r for r in deck}
    if len(expected) != len(deck) or set(key) != set(expected):
        raise ValueError("Inconsistent answer key")
    seen = set()
    joined = []
    for row in ratings:
        if set(row) != {"label", "batch", *FIELDS, "supported", "comment"}:
            raise ValueError("Invalid rating fields")
        label = row["label"]
        if label not in expected or label in seen or row["batch"] != expected[label]["batch"] or row["batch"] != key[label]["batch"]:
            raise ValueError("Unknown, duplicate, or mismatched rating")
        seen.add(label)
        if any(row[f] is not None and (type(row[f]) is not int or row[f] not in (0, 1, 2)) for f in FIELDS):
            raise ValueError("Invalid score")
        if row["supported"] is not None and type(row["supported"]) is not bool or not isinstance(row["comment"], str):
            raise ValueError("Invalid support or comment")
        joined.append({**row, **key[label]})
    if seen != set(expected):
        raise ValueError("Missing ratings rows")
    summary = {}
    for mode in sorted({r["mode"] for r in joined}):
        rows = [r for r in joined if r["mode"] == mode and r["selected_id"] != "NONE"]
        summary[mode] = {"selected": len(rows), "fields": {}}
        for field in FIELDS:
            scores = [r[field] for r in rows if r[field] is not None]
            summary[mode]["fields"][field] = {"count": len(scores), "sum": sum(scores),
                                                 "mean": sum(scores) / len(scores) if scores else None}
    pairs = []
    if phase == "rewrite":
        for batch in sorted({r["batch"] for r in joined}):
            rows = {r["mode"]: r for r in joined if r["batch"] == batch and r["selected_id"] != "NONE"}
            if set(rows) == {"compact", "story"} and rows["compact"]["selected_id"] == rows["story"]["selected_id"]:
                pairs.append({"batch": batch, "selected_id": rows["compact"]["selected_id"],
                              "story_minus_compact": {f: rows["story"][f] - rows["compact"][f]
                                                      if rows["story"][f] is not None and rows["compact"][f] is not None else None for f in FIELDS}})
    return {"ratings_sha256": digest(ratings), "phase": phase, "pair_comparisons": pairs,
            "rated_stories": sum(all(r[f] is not None for f in FIELDS) and r["selected_id"] != "NONE" for r in joined),
            "missing_fields": [{"label": r["label"], "fields": [f for f in (*FIELDS, "supported") if r[f] is None]}
                               for r in joined if r["selected_id"] != "NONE" and any(r[f] is None for f in (*FIELDS, "supported"))],
            "summary": summary, "ratings": joined,
            "limitations": (f"Один оценщик, {len(joined)} строк оценок. Отсутствующие API-ответы не превращаются в нулевые оценки. "
                            "Повторы одного эпизода не независимы. supported — мнение оценщика, не доказательство достоверности. "
                            + (f"Оценивались пересказы. Полных пар одного эпизода: {len(pairs)}; общие средние при разном составе эпизодов нельзя считать эффектом длины."
                               if phase == "rewrite" else "Оценивались краткие описания отбора, а не готовые публикации. Сравнение критериев смешано с качеством пересказа."))}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ratings", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True, help="existing experiment results directory")
    parser.add_argument("--phase", choices=("selection", "rewrite"), default="selection")
    args = parser.parse_args()
    ratings = json.loads(args.ratings.read_text())
    result = analyze(ratings, json.loads((args.out / "blind.json").read_text()), json.loads((args.out / "answer-key.json").read_text()), phase=args.phase)
    # Never overwrite another evaluation or the user's original download.
    target = args.out / ("evaluation-" + result["ratings_sha256"][:16])
    target.mkdir(mode=0o700, exist_ok=False)
    (target / "ratings.json").write_bytes(args.ratings.read_bytes())
    (target / "analysis.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(target)
    print(json.dumps({k: result[k] for k in ("rated_stories", "missing_fields", "summary")}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
