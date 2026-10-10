"""Offline audit of an ID-only holdout; no API, no automatic editorial scores."""
import argparse
import html
import json
from pathlib import Path
from run import digest, load_dataset, records


def summarize(data, root, review):
    if review["dataset_sha256"] != digest(data):
        raise ValueError("Review corpus mismatch")
    passages = {p["id"]: p for p in data["passages"]}
    scores = {r["id"]: r for r in review["rows"]}
    if set(scores) != set(passages):
        raise ValueError("Incomplete editorial review")
    batches = {b["id"]: set(b["passage_ids"]) for b in data["batches"]}
    rows, attempts, tokens = [], 0, 0
    for provider in ("gemini", "oss120"):
        folder = root / "api" / provider
        manifest = json.loads((folder / "manifest.json").read_text())
        if manifest["dataset_sha256"] != digest(data):
            raise ValueError("Run corpus mismatch")
        issued = records(folder / "attempts.jsonl")
        results = records(folder / "results.jsonl")
        if len(issued) > 4 or len(results) != len(issued):
            raise ValueError("Attempt/result mismatch")
        issued_jobs = [(r["batch"], r["mode"]) for r in issued]
        result_jobs = [(r["batch"], r["mode"]) for r in results]
        if len(set(issued_jobs)) != len(issued_jobs) or issued_jobs != result_jobs:
            raise ValueError("Attempt/result mismatch")
        attempts += len(issued)
        for result in results:
            row = {"configuration": provider, "batch": result["batch"], "status": result["status"],
                   "http_status": result.get("http_status"), "provider_model": result.get("provider_model"),
                   "duration_ms": result["duration_ms"]}
            usage = result.get("usage", {})
            tokens += usage.get("totalTokenCount", usage.get("total_tokens", 0))
            if result["status"] == "ok":
                selection = result["selection"]
                ident = selection["selected_id"]
                row["selected_id"] = ident
                if ident != "NONE":
                    if ident not in batches[result["batch"]]:
                        raise ValueError("Selected ID outside batch")
                    p = passages[ident]
                    if selection["hook"] != p["excerpt"] or selection["evidence"] != p["excerpt"]:
                        raise ValueError("Displayed text differs from source")
                    row.update(editor_usable=scores[ident]["editor_usable"], reason=scores[ident]["reason"])
            rows.append(row)
    return {"dataset_sha256": digest(data), "attempts": attempts, "cap": 8,
            "reported_total_tokens": tokens, "rows": rows,
            "editor_usable_candidates": sum(r["editor_usable"] for r in scores.values()),
            "limitations": ["One subjective Codex reviewer, not human ratings", "Only ten predeclared famous films",
                            "Four paragraphs per film, not an exhaustive source search", "Source facts not independently verified",
                            "Gemini and Groq overlap only on the first batch; no model winner established",
                            "No production, DB, Telegram or deployment changes"]}


def render(data, review, summary):
    esc = html.escape
    sources = {s["id"]: s for s in data["sources"]}
    passages = {p["id"]: p for p in data["passages"]}
    scores = {r["id"]: r for r in review["rows"]}
    cards, table, quoted_sources = [], [], set()
    for row in summary["rows"]:
        ident = row.get("selected_id")
        state = "503 — запрос остановлен" if row["http_status"] == 503 else "Отказ" if ident == "NONE" else "Подходит" if row.get("editor_usable") else "Нужна редактура"
        table.append(f'<tr><td>{esc(row["configuration"])}</td><td>{esc(row["batch"])}</td><td>{esc(ident or "—")}</td><td>{state}</td></tr>')
        if ident and ident != "NONE":
            p = passages[ident]
            source = sources[p["source_id"]]
            # One brief quote per source; full paragraphs remain in the frozen corpus.
            quote = ""
            if p["source_id"] not in quoted_sources:
                quote = '<blockquote>' + esc(" ".join(p["excerpt"].split()[:25])) + '…</blockquote><small>Начало исходного абзаца, до 25 слов. Полный текст — по ссылке на статью.</small>'
                quoted_sources.add(p["source_id"])
            cards.append(f'<article><span class="pill {"good" if row.get("editor_usable") else "bad"}">{state}</span><h2>{esc(source["display_title"])}</h2><p>{esc(row["reason"])}</p>{quote}<p><a href="{esc(source["url"], quote=True)}" target="_blank" rel="noopener">Wikipedia · фиксированная версия</a> · <a href="{source["license"]}">CC BY-SA 4.0</a></p><small>{esc(row["configuration"])} · {esc(ident)} · AI выбрал только ID, абзац не переписан.</small></article>')
    coverage = []
    for source in data["sources"]:
        ps = [p for p in data["passages"] if p["source_id"] == source["id"]]
        usable = sum(scores[p["id"]]["editor_usable"] for p in ps)
        coverage.append(f'<tr><td>{esc(source["display_title"])}</td><td>{len(ps)}</td><td>{usable}</td></tr>')
    return '''<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Русские факты · отбор без пересказа</title><style>
body{margin:0;background:#10151e;color:#e9edf5;font:17px/1.55 system-ui}main{max-width:1040px;margin:auto;padding:36px 24px}h1{font-size:36px;line-height:1.2}h2{font-size:24px}a{color:#9cc9ff}small{color:#acb9cd}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(290px,1fr));gap:20px}article,.note{background:#1b2331;border:1px solid #344159;border-radius:18px;padding:24px;margin:18px 0}blockquote{margin:18px 0;padding:12px 18px;border-left:3px solid #7caeff;color:#c4cfe0}table{border-collapse:collapse;width:100%;font-size:15px}td,th{padding:11px;border-bottom:1px solid #344159;text-align:left}.pill{border-radius:20px;padding:4px 10px;font-size:13px}.good{background:#244938;color:#a6e4be}.bad{background:#5b4525;color:#f5d69a}.numbers{font-size:23px}.muted{color:#adbbd0}</style><main>
<p class="muted">Эксперимент · 9 октября 2026 · production не менялся</p><h1>Пусть AI выбирает историю,<br>а не сочиняет её</h1><p>Десять новых фильмов. Русская Wikipedia. В API уходят целые абзацы, а возвращается только ID или NONE. Отображаемый текст точно совпадает с источником.</p>
<div class="note"><div class="numbers">9 из 40 абзацев подходят без пересказа · 4 из 5 выборов AI прошли редакторскую оценку</div><p>Это результат одного маленького прогона и субъективной проверки Codex, а не обещание 80% или 100% качества. Факты исходных статей отдельно не проверялись.</p></div>
<h2>Что выбрали модели</h2><table><thead><tr><th>Конфигурация</th><th>Пакет</th><th>ID</th><th>Проверка</th></tr></thead><tbody>''' + "".join(table) + '''</tbody></table><div class="grid">''' + "".join(cards) + '''</div>
<h2>Что мешает русским исходникам</h2><p>Текст статьи тоже бывает тяжёлым, содержит спойлеры или не имеет законченной истории. В одном абзаце обнаружен CSS: парсер исправлен, старый корпус и результат сохранены. Переписывание не требуется хорошему исходнику, но простой выбор ID не заменяет отбор по понятности.</p>
<h2>Доступность материалов</h2><p>После исправления адреса «Сияния» и заголовка «Работа над фильмом» собраны 88 абзацев. Для API и редакторской проверки взяты по четыре равномерно расположенных абзаца на фильм, без выбора по интересу.</p><table><thead><tr><th>Фильм</th><th>Проверено</th><th>Пригодно без пересказа</th></tr></thead><tbody>''' + "".join(coverage) + '''</tbody></table>
<h2>Рекомендуемый следующий шаг</h2><p>Собирать русские истории в фоне → выбирать ID → проверять язык, самостоятельность и необычность → сохранять пригодные тексты в готовый пул. Тяжёлые фрагменты отправлять в отдельную ветку ограниченной редакторской правки. Не расширять текст ради числа слов.</p><p>Нужны более широкий корпус, проверка ссылок на первичные источники и сравнение с читательскими оценками. Надёжность API решается отдельно запасом готовых материалов и отложенными попытками.</p>
<p class="muted">''' + f'{summary["attempts"]}/8 попыток · {summary["reported_total_tokens"]} reported tokens успешных ответов · Gemini остановлен после 503 · без повторов и дополнительных запросов. Старые оценки не изменены.' + '</p></main></html>'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dataset", type=Path, required=True)
    parser.add_argument("--root", type=Path, required=True)
    args = parser.parse_args()
    data = load_dataset(args.dataset, allowed_source_hosts=("ru.wikipedia.org",))
    review = json.loads((args.root / "candidate-review.json").read_text())
    summary = summarize(data, args.root, review)
    (args.root / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n")
    (args.root / "holdout.html").write_text(render(data, review, summary))
    print(json.dumps({k: v for k, v in summary.items() if k not in ("rows", "limitations")}, indent=2))


if __name__ == "__main__":
    main()
