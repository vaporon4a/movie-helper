"""Offline coverage and editorial report for the frozen TMDB pilot."""
import argparse
from collections import Counter
import hashlib
import html
import json
from pathlib import Path
from run import digest, records
from catalog_probe import CAPS

GROUPS = {"1960-1979": "1960–1979", "1980-1999": "1980–1999", "2000-2009": "2000–2009",
          "2010-2019": "2010–2019", "2020+": "2020–2026", "russian-language": "Русскоязычные",
          "trending-week": "Недельные тренды"}


def summarize(root):
    catalog = json.loads((root / "catalog.json").read_text())
    external = json.loads((root / "external.json").read_text())
    mapping = json.loads((root / "wikidata.json").read_text())
    wiki = json.loads((root / "wikipedia.json").read_text())
    if external["catalog_sha256"] != digest(catalog) or mapping["external_sha256"] != digest(external) or mapping["catalog_sha256"] != digest(catalog) or wiki["wikidata_sha256"] != digest(mapping) or wiki["catalog_sha256"] != digest(catalog):
        raise ValueError("Source lineage mismatch")
    movies = catalog["movies"]
    ids = {m["id"] for m in movies}
    if len(ids) != len(movies):
        raise ValueError("Duplicate catalog movie")
    for data in (external, mapping, wiki):
        row_ids = [r["movie_id"] for r in data["rows"]]
        if len(set(row_ids)) != len(row_ids) or not set(row_ids) <= ids:
            raise ValueError("Invalid coverage movie IDs")
    for p in wiki["passages"]:
        if p["movie_id"] not in ids or hashlib.sha256(p["text"].encode()).hexdigest() != p["sha256"]:
            raise ValueError("Untraceable paragraph")
    ext = {r["movie_id"]: r for r in external["rows"]}
    maps = {r["movie_id"]: r for r in mapping["rows"]}
    sources = {r["movie_id"]: r for r in wiki["rows"]}
    def counts(ids):
        return {"selected": len(ids), "qid": sum(bool(ext.get(i, {}).get("qid")) for i in ids),
                "identity_verified": sum(maps.get(i, {}).get("status") in ("matched", "no_ru_article") for i in ids),
                "ru_link": sum(maps.get(i, {}).get("status") == "matched" for i in ids),
                "captured": sum(sources.get(i, {}).get("status") == "captured" for i in ids),
                "production": sum(sources.get(i, {}).get("production_paragraphs", 0) > 0 for i in ids),
                "eligible": sum(sources.get(i, {}).get("eligible_paragraphs", 0) > 0 for i in ids)}
    groups = [{"group": group["group"], **counts({m["id"] for m in movies if m["group"] == group["group"]})} for group in catalog["coverage"]]
    attempts = {phase: len(records(root / phase / "attempts.jsonl")) for phase in CAPS}
    if any(attempts[p] > CAPS[p] for p in CAPS):
        raise ValueError("Network cap exceeded")
    result = {"catalog_sha256": digest(catalog), "wikipedia_sha256": digest(wiki),
              "totals": counts(ids), "groups": groups, "attempts": attempts,
              "mapping_statuses": dict(Counter(r["status"] for r in mapping["rows"])),
              "wiki_statuses": dict(Counter(r["status"] for r in wiki["rows"])),
              "paragraphs": len(wiki["passages"]), "source_eligible_paragraphs": sum(r.get("eligible_paragraphs", 0) for r in wiki["rows"]),
              "genres": dict(Counter(g for m in movies for g in set(m["genres"]))),
              "limitations": ["One seeded sample of 70; overlapping source groups, deduplicated movie IDs",
                              "Popular candidates plus release/vote filters, not a uniform sample of all cinema",
                              "Strict title/year/ID gate can exclude a correct movie that needs alias review",
                              "No approximate title matching or IMDb fallback in this experiment",
                              "Production extraction uses an explicit heading whitelist and word limits",
                              "Source availability does not establish interest or truth of film facts",
                              "No AI calls or production/DB/Telegram/deployment changes"]}
    if (root / "server-access.json").exists():
        probe = json.loads((root / "server-access.json").read_text())
        if probe.get("requests", 0) > 1:
            raise ValueError("Server probe cap exceeded")
        result["server_source_probe"] = probe
    review_path = root / "editor-review.json"
    if review_path.exists():
        review = json.loads(review_path.read_text())
        plan = json.loads((root / "editorial-sample-plan.json").read_text())
        if review["wikipedia_sha256"] != digest(wiki) or review["sample_plan_sha256"] != digest(plan):
            raise ValueError("Editorial lineage mismatch")
        passages = {p["id"]: p for p in wiki["passages"]}
        seen = set()
        for row in review["rows"]:
            if row["id"] in seen or row["id"] not in passages or passages[row["id"]]["movie_id"] not in plan["movie_ids"]:
                raise ValueError("Invalid editorial sample")
            seen.add(row["id"])
        result["editorial"] = {"planned_films": len(plan["movie_ids"]), "reviewed_paragraphs": len(review["rows"]),
                               "usable_paragraphs": sum(r["usable"] for r in review["rows"]),
                               "usable_films": len({passages[r["id"]]["movie_id"] for r in review["rows"] if r["usable"]}),
                               "kind": "Subjective Codex text review, not independent fact checking or user ratings"}
    diagnostic_path = root / "middle-diagnostic-review.json"
    if diagnostic_path.exists():
        diagnostic = json.loads(diagnostic_path.read_text())
        plan = json.loads((root / "middle-diagnostic-plan.json").read_text())
        if diagnostic["wikipedia_sha256"] != digest(wiki) or diagnostic["sample_plan_sha256"] != digest(plan) or set(r["id"] for r in diagnostic["rows"]) != set(plan["paragraph_ids"]):
            raise ValueError("Diagnostic lineage mismatch")
        result["exploratory_middle_check"] = {"reviewed_paragraphs": len(diagnostic["rows"]),
                                              "usable_paragraphs": sum(r["usable"] for r in diagnostic["rows"]),
                                              "original_holdout": False}
    return result, catalog, mapping, wiki


def render(summary, catalog, mapping, wiki, root):
    esc = html.escape
    rows = []
    for g in summary["groups"]:
        rows.append('<tr><td>' + GROUPS[g["group"]] + '</td>' + ''.join(f'<td>{g[k]}</td>' for k in ("selected", "identity_verified", "ru_link", "production", "eligible")) + '</tr>')
    movies = {m["id"]: m for m in catalog["movies"]}
    sources = {r["movie_id"]: r for r in wiki["rows"]}
    maps = {r["movie_id"]: r for r in mapping["rows"]}
    cards = []
    quotes = set()
    review = json.loads((root / "editor-review.json").read_text()) if (root / "editor-review.json").exists() else {"rows": []}
    diagnostic = json.loads((root / "middle-diagnostic-review.json").read_text()) if (root / "middle-diagnostic-review.json").exists() else {"rows": []}
    passages = {p["id"]: p for p in wiki["passages"]}
    primary_ids = {r["id"] for r in review["rows"]}
    for r in sorted(review["rows"] + diagnostic["rows"], key=lambda r: not r["usable"]):
        p = passages[r["id"]]
        m = movies[p["movie_id"]]
        snippet = ''
        if m["id"] not in quotes:
            snippet = '<blockquote>' + esc(' '.join(p["text"].split()[:25])) + '…</blockquote><small>Короткое начало цитаты. Полная история — в исходной статье.</small>'
            quotes.add(m["id"])
        source = sources[m["id"]]
        cards.append(f'<article data-group="{esc(m["group"])}" data-usable="{str(r["usable"]).lower()}" {"" if r["usable"] else "hidden"}><span class="pill {"yes" if r["usable"] else "no"}">{"Подходит без пересказа" if r["usable"] else "Не готов к публикации"}</span><h3>{esc(m["title"])} · {m["year"]}</h3><p>{esc(r["reason"])}</p>{snippet}<p><a href="{esc(source["url"], quote=True)}" target="_blank" rel="noopener">Wikipedia · версия {source["revision"]}</a> · <a href="https://creativecommons.org/licenses/by-sa/4.0/">CC BY-SA 4.0</a></p><small>Интерес {r["interest"]}/2 · Понятность {r["clarity"]}/2 · {esc(r["id"])} · {"Исходная выборка" if r["id"] in primary_ids else "Дополнительная диагностика"}</small></article>')
    entries = []
    for m in catalog["movies"]:
        source, link = sources.get(m["id"], {}), maps.get(m["id"], {})
        entries.append(f'<tr data-group="{esc(m["group"])}"><td><a href="https://www.themoviedb.org/movie/{m["id"]}">{esc(m["title"])}</a></td><td>{m["year"]}</td><td>{GROUPS[m["group"]]}</td><td>{esc(link.get("status", "not_checked"))}</td><td>{source.get("eligible_paragraphs", "—")}</td></tr>')
    t = summary["totals"]
    editorial = summary.get("editorial", {})
    quality = f'<p>Заранее отобраны {editorial.get("planned_films", 0)} фильмов без замены отсутствующих источников. Проверено {editorial.get("reviewed_paragraphs", 0)} абзацев; {editorial.get("usable_paragraphs", 0)} из них, из {editorial.get("usable_films", 0)} фильмов, подходят без пересказа по моей оценке. Это субъективная оценка понятности/интереса; правдивость историй независимо не проверялась.</p>'
    if diagnostic["rows"]:
        quality += '<p>Дополнительно проверены восемь средних абзацев: один пригоден без пересказа. Это отдельная поисковая диагностика после основной оценки. В «Побеге из Шоушенка» интересная история с чеком смешана со спойлером — нужен отбор отдельного эпизода с контекстом.</p>'
    options = '<option value="all">Все группы</option>' + ''.join(f'<option value="{esc(k)}">{v}</option>' for k, v in GROUPS.items())
    return '''<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>TMDB → русские кинофакты</title><style>
body{margin:0;background:#111620;color:#ecf0f8;font:17px/1.55 system-ui}main{max-width:1100px;margin:auto;padding:32px 22px}h1{font-size:36px;line-height:1.2}h2{margin-top:35px}a{color:#9bcbff}small,.muted{color:#b0bed3}.stats,.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:16px}.stat,article,.note{background:#1c2533;border:1px solid #35445c;border-radius:16px;padding:22px}.stat strong{display:block;font-size:34px}.note{margin:24px 0}table{width:100%;border-collapse:collapse;font-size:15px}td,th{text-align:left;padding:11px;border-bottom:1px solid #35445c}.scroll{overflow:auto}blockquote{margin:15px 0;padding:10px 16px;border-left:3px solid #82b7ef;color:#c1ccdd}.pill{font-size:13px;border-radius:20px;padding:4px 9px}.yes{background:#294c39;color:#ace3be}.no{background:#574525;color:#eed093}select{font:inherit;background:#1c2533;color:#edf1f8;border:1px solid #536884;padding:10px;border-radius:10px}article[hidden],tr[hidden]{display:none}</style><main><p class="muted">Исследование источников · 9 октября 2026 · без AI-запросов и изменений production</p><h1>Можно ли заменить фиксированный<br>список фильмов каталогом TMDB?</h1><p>Получаем кандидатов по годам и популярности, перемешиваем с фиксированным seed, проверяем идентификаторы и ищем русские материалы о съёмках.</p><div class="stats">''' + f'<div class="stat"><strong>{t["selected"]}</strong>уникальных фильмов</div><div class="stat"><strong>{t["ru_link"]}</strong>проверенных связей с русской статьёй</div><div class="stat"><strong>{t["eligible"]}</strong>Фильмы с абзацами подходящей длины</div>' + '''</div><div class="note"><b>Наличие статьи ≠ наличие хорошей истории.</b><p>Проверены TMDB ID, Wikidata ID, обратный идентификатор P4947, согласованность IMDb, название/алиасы, год и Wikidata ID самой статьи. Неоднозначные фильмы не сопоставлялись по похожему названию. Количество абзацев отражает правила сборщика, а не все возможные источники.</p></div><h2>Покрытие разных групп</h2><div class="scroll"><table><thead><tr><th>Группа</th><th>Фильмов</th><th>ID + имя/год</th><th>RU-статья</th><th>Раздел о создании</th><th>20–140 слов</th></tr></thead><tbody>''' + ''.join(rows) + '''</tbody></table></div><h2>Редакторская выборка</h2>''' + quality + '<label for="group">Показать группу </label><select id="group">' + options + '</select> <label><input type="checkbox" id="rejected"> Показать отклонённые</label><div class="cards" style="margin-top:20px">' + ''.join(cards) + '''</div><h2>Все фильмы и результаты проверки</h2><details><summary>Раскрыть каталог</summary><div class="scroll"><table><thead><tr><th>Фильм</th><th>Год</th><th>Группа</th><th>Связь</th><th>Абзацы</th></tr></thead><tbody>''' + ''.join(entries) + '''</tbody></table></div></details><h2>Как использовать результат</h2><p>Для бота нужен сохраняемый каталог кандидатов: разнообразие по эпохам/жанрам, отдельная проверка связи со статьёй, отложенный поиск источников и готовый пул отобранных историй. Фиксированный список остаётся резервом. Повторы отслеживаем по конкретному эпизоду; для фильма задаём временный перерыв.</p><p>Тренды проверяем отдельно от узнаваемой классики. Если фильмы дают мало содержательных источников, их долю стоит ограничить, а отсутствие статьи кэшировать — без бессмысленных повторных AI-запросов.</p><p class="muted">''' + f'Запросы: {summary["attempts"]}. Автоматических повторов не было. Это один набор из 70 фильмов; переносить его проценты на весь каталог нельзя.' + '''</p><p class="muted">Данные каталога: TMDB. This product uses the TMDB API but is not endorsed or certified by TMDB. Тексты Wikipedia: CC BY-SA 4.0; редакторская оценка Codex хранится отдельно от источников и пользовательских оценок.</p></main><script>function filter(){const g=document.getElementById('group').value;const rejected=document.getElementById('rejected').checked;document.querySelectorAll('[data-group]').forEach(el=>{el.hidden=(g!=='all'&&el.dataset.group!==g)||(el.tagName==='ARTICLE'&&!rejected&&el.dataset.usable!=='true')})}document.getElementById('group').addEventListener('change',filter);document.getElementById('rejected').addEventListener('change',filter)</script></html>'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    args = parser.parse_args()
    result, catalog, mapping, wiki = summarize(args.root)
    (args.root / "summary.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    (args.root / "catalog.html").write_text(render(result, catalog, mapping, wiki, args.root))
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
