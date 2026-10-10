"""Bounded TMDB -> Wikidata -> RU Wikipedia source-coverage experiment. No AI."""
import argparse
from datetime import date, datetime, timezone
import hashlib
import json
from pathlib import Path
import random
import re
import time
import unicodedata
import urllib.error
import urllib.parse
import urllib.request
from holdout_capture import RussianParagraphs
from run import NoRedirect, append, digest, records

DAY = "2026-10-09"
SEED = 20261009
UA = "MovieHelperFactExperiment/1.0 (https://github.com/vaporon4a/movie-helper)"
CAPS = {"catalog": 7, "external": 70, "wikidata": 70, "wikipedia": 70}


def specifications():
    rng = random.Random(SEED)
    groups = [("1960-1979", "1960-01-01", "1979-12-31", None),
              ("1980-1999", "1980-01-01", "1999-12-31", None),
              ("2000-2009", "2000-01-01", "2009-12-31", None),
              ("2010-2019", "2010-01-01", "2019-12-31", None),
              ("2020+", "2020-01-01", DAY, None),
              ("russian-language", "1950-01-01", "2019-12-31", "ru")]
    result = []
    for name, start, end, language in groups:
        query = {"language": "ru-RU", "include_adult": "false", "include_video": "false",
                 "sort_by": "popularity.desc", "primary_release_date.gte": start,
                 "primary_release_date.lte": end, "vote_count.gte": "50" if language else "200",
                 "page": str(rng.randint(1, 3))}
        if language:
            query["with_original_language"] = language
        result.append({"group": name, "url": "https://api.themoviedb.org/3/discover/movie?" + urllib.parse.urlencode(query), "query": query})
    result.append({"group": "trending-week", "url": "https://api.themoviedb.org/3/trending/movie/week?language=ru-RU", "query": {"time_window": "week"}})
    return result


class Journal:
    def __init__(self, root, phase):
        self.root = root / phase
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.phase = phase

    def get(self, ident, url, headers=None, text=False, processor=None):
        old = {r["id"]: r for r in records(self.root / "responses.jsonl")}
        if ident in old:
            return old[ident]
        attempts = records(self.root / "attempts.jsonl")
        if any(a["id"] == ident for a in attempts):
            return {"id": ident, "status": "unknown", "reason": "reserved_without_response"}
        if len(attempts) >= CAPS[self.phase]:
            raise ValueError("Request budget exhausted")
        if urllib.parse.urlparse(url).hostname not in ("api.themoviedb.org", "www.wikidata.org", "ru.wikipedia.org"):
            raise ValueError("Endpoint outside allowlist")
        append(self.root / "attempts.jsonl", {"id": ident, "url": url, "at": datetime.now(timezone.utc).isoformat()})
        start = time.monotonic()
        try:
            req = urllib.request.Request(url, headers={"User-Agent": UA, **(headers or {})})
            # Only allow HTTPS redirects inside this public-source allowlist.
            with urllib.request.build_opener(NoRedirect()).open(req, timeout=25) as response:
                body = response.read((4 << 20) + 1)
            if len(body) > 4 << 20:
                raise ValueError("Oversized response")
            payload = body.decode("utf-8") if text else json.loads(body)
            if processor:
                payload = processor(payload)
            row = {"id": ident, "status": "ok", "payload": payload}
        except urllib.error.HTTPError as error:
            row = {"id": ident, "status": "http_error", "http_status": error.code,
                   "retry_after": str(error.headers.get("Retry-After", ""))[:80]}
        except (OSError, TimeoutError):
            row = {"id": ident, "status": "network_error"}
        except (ValueError, UnicodeError):
            row = {"id": ident, "status": "invalid_response"}
        row["duration_ms"] = int((time.monotonic() - start) * 1000)
        # Headers, credentials, and upstream error bodies never enter the journal.
        append(self.root / "responses.jsonl", row)
        time.sleep(0.15)
        return row


def frozen(path, value):
    if path.exists():
        if json.loads(path.read_text()) != value:
            raise ValueError("Frozen artifact differs")
    else:
        with path.open("x") as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2)
            stream.write("\n")


def token(path):
    # Read exactly one secret, only on the remote host. No env snapshot.
    for line in path.read_text().splitlines():
        name, separator, value = line.partition("=")
        if separator and name == "TMDB_API_TOKEN" and value.strip():
            value = value.strip()
            if "\n" in value or "\r" in value:
                break
            return value
    raise ValueError("TMDB credential unavailable")


def movie(row):
    ident = row.get("id")
    released = row.get("release_date", "")
    try:
        published = date.fromisoformat(released)
    except (ValueError, TypeError):
        return None
    if type(ident) is not int or ident <= 0 or row.get("adult") is not False or published > date.fromisoformat(DAY):
        return None
    if not isinstance(row.get("title"), str) or not row["title"].strip():
        return None
    return {"id": ident, "title": row["title"], "original_title": row.get("original_title", ""),
            "release_date": released, "year": published.year, "genres": row.get("genre_ids", []),
            "language": row.get("original_language", ""), "votes": row.get("vote_count", 0),
            "rating": row.get("vote_average", 0), "popularity": row.get("popularity", 0)}


def choose(rows, group, used):
    candidates = [m for row in rows if (m := movie(row)) is not None and m["id"] not in used]
    random.Random(str(SEED) + group).shuffle(candidates)
    selected, primary = [], {}
    for cap in (3, 10):
        for m in candidates:
            genre = m["genres"][0] if m["genres"] else 0
            if m["id"] in used or primary.get(genre, 0) >= cap:
                continue
            used.add(m["id"])
            primary[genre] = primary.get(genre, 0) + 1
            selected.append({**m, "group": group})
            if len(selected) == 10:
                return selected
    return selected


def catalog(root, key):
    journal = Journal(root, "catalog")
    selected, coverage, used = [], [], set()
    for spec in specifications():
        row = journal.get(spec["group"], spec["url"], {"Authorization": "Bearer " + key})
        if row["status"] == "ok":
            raw = row["payload"].get("results", [])
            batch = choose(raw, spec["group"], used)
            selected.extend(batch)
            coverage.append({"group": spec["group"], "status": "ok", "returned": len(raw), "selected": len(batch)})
        else:
            coverage.append({"group": spec["group"], "status": row["status"], "selected": 0})
            break
    frozen(root / "catalog.json", {"seed": SEED, "date": DAY, "specifications": specifications(), "coverage": coverage, "movies": selected})
    print("catalog", len(selected), "unique movies", flush=True)


def external(root, key):
    data = json.loads((root / "catalog.json").read_text())
    journal = Journal(root, "external")
    output = []
    for m in data["movies"]:
        row = journal.get(str(m["id"]), f'https://api.themoviedb.org/3/movie/{m["id"]}/external_ids', {"Authorization": "Bearer " + key})
        link = {"movie_id": m["id"], "status": row["status"]}
        if row["status"] == "ok":
            raw = row["payload"]
            qid = raw.get("wikidata_id")
            imdb = raw.get("imdb_id")
            link.update(qid=qid if isinstance(qid, str) and re.fullmatch(r"Q[1-9]\d*", qid) else None,
                        imdb=imdb if isinstance(imdb, str) and re.fullmatch(r"tt\d+", imdb) else None)
        output.append(link)
        if row.get("http_status") in (401, 403, 429) or row.get("http_status", 0) >= 500:
            break
        if len(output) % 10 == 0:
            print("external", len(output), flush=True)
    frozen(root / "external.json", {"catalog_sha256": digest(data), "rows": output})


def values(entity, prop):
    return [c["mainsnak"]["datavalue"]["value"] for c in entity.get("claims", {}).get(prop, [])
            if c.get("rank") != "deprecated" and "datavalue" in c.get("mainsnak", {})]


def normalized(title):
    title = re.sub(r"\s*\((?:фильм|film)[^)]*\)\s*$", "", title, flags=re.I)
    title = unicodedata.normalize("NFKC", title).casefold().replace("ё", "е")
    return "".join(c for c in title if c.isalnum())


def extract_html(raw):
    """Persist selected paragraphs and provenance, never an entire article."""
    qid = re.search(r'"wgWikibaseItemId":"(Q\d+)"', raw)
    revision = re.search(r'"wgRevisionId":(\d+)', raw)
    parser = RussianParagraphs()
    parser.feed(raw)
    eligible = [(s, p) for s, p in parser.paragraphs if 20 <= len(p.split()) <= 140 and p[-1] in ".!?»”"]
    indices = list(range(len(eligible))) if len(eligible) <= 4 else [i * (len(eligible) - 1) // 3 for i in range(4)]
    return {"qid": qid[1] if qid else None, "revision": int(revision[1]) if revision else None,
            "production_paragraphs": len(parser.paragraphs), "eligible_paragraphs": len(eligible),
            "selected": [{"section": eligible[i][0], "text": eligible[i][1]} for i in indices],
            "html_sha256": hashlib.sha256(raw.encode()).hexdigest()}


def identity(m, link, entity):
    qid = link["qid"]
    output = {"movie_id": m["id"], "qid": qid, "entity_revision": entity.get("lastrevid")}
    if entity.get("id") != qid:
        return {**output, "status": "entity_mismatch"}
    tmdb = set(values(entity, "P4947"))
    imdb = set(values(entity, "P345"))
    if tmdb != {str(m["id"])} or (link.get("imdb") and imdb and imdb != {link["imdb"]}):
        return {**output, "status": "id_mismatch_or_missing"}
    titles = [l["value"] for l in entity.get("labels", {}).values() if l.get("language") in ("ru", "en")]
    titles += [a["value"] for lang, rows in entity.get("aliases", {}).items() if lang in ("ru", "en") for a in rows]
    years = {int(v["time"][1:5]) for v in values(entity, "P577") if isinstance(v, dict) and re.match(r"\+\d{4}-", v.get("time", ""))}
    title_match = bool({normalized(m["title"]), normalized(m["original_title"])} & {normalized(t) for t in titles})
    year_match = bool(years) and min(abs(y - m["year"]) for y in years) <= 1
    ru = entity.get("sitelinks", {}).get("ruwiki", {})
    output.update(title_match=title_match, year_match=year_match, release_years=sorted(years), wiki_title=ru.get("title"))
    if not title_match or not year_match:
        return {**output, "status": "needs_identity_review"}
    return {**output, "status": "matched" if ru.get("title") else "no_ru_article"}


def wikidata(root):
    catalog = json.loads((root / "catalog.json").read_text())
    links = json.loads((root / "external.json").read_text())
    if links["catalog_sha256"] != digest(catalog):
        raise ValueError("Catalog lineage mismatch")
    external = {r["movie_id"]: r for r in links["rows"]}
    journal, output = Journal(root, "wikidata"), []
    for n, m in enumerate(catalog["movies"]):
        link = external.get(m["id"], {})
        if not link.get("qid"):
            output.append({"movie_id": m["id"], "status": "no_qid" if link.get("status") == "ok" else "external_unavailable"})
            continue
        qid = link["qid"]
        row = journal.get(qid, f"https://www.wikidata.org/wiki/Special:EntityData/{qid}.json")
        if row["status"] == "ok":
            entity = row["payload"].get("entities", {}).get(qid, {})
            output.append(identity(m, link, entity))
        else:
            output.append({"movie_id": m["id"], "qid": qid, "status": row["status"], "http_status": row.get("http_status")})
        if row.get("http_status") == 429:
            break
        if (n + 1) % 10 == 0:
            print("wikidata", n + 1, flush=True)
    frozen(root / "wikidata.json", {"catalog_sha256": digest(catalog), "external_sha256": digest(links), "rows": output})


def wikipedia(root):
    catalog = json.loads((root / "catalog.json").read_text())
    mapping = json.loads((root / "wikidata.json").read_text())
    if mapping["catalog_sha256"] != digest(catalog):
        raise ValueError("Catalog lineage mismatch")
    maps = {r["movie_id"]: r for r in mapping["rows"]}
    journal, output, passages = Journal(root, "wikipedia"), [], []
    for n, m in enumerate(catalog["movies"]):
        link = maps.get(m["id"], {})
        if link.get("status") != "matched":
            output.append({"movie_id": m["id"], "status": "not_resolved"})
            continue
        url = "https://ru.wikipedia.org/wiki/" + urllib.parse.quote(link["wiki_title"].replace(" ", "_"), safe="()_:,")
        row = journal.get(str(m["id"]), url, text=True, processor=extract_html)
        if row["status"] != "ok":
            output.append({"movie_id": m["id"], "status": row["status"], "http_status": row.get("http_status")})
            if row.get("http_status") == 429:
                break
            continue
        raw = row["payload"]
        if not raw["revision"] or raw["qid"] != link["qid"]:
            output.append({"movie_id": m["id"], "status": "wiki_identity_mismatch_or_missing"})
            continue
        chosen = [{"id": f'{m["id"]}-{i+1}', "movie_id": m["id"], "section": p["section"],
                   "text": p["text"], "sha256": hashlib.sha256(p["text"].encode()).hexdigest()}
                  for i, p in enumerate(raw["selected"])]
        passages.extend(chosen)
        output.append({"movie_id": m["id"], "status": "captured", "wiki_title": link["wiki_title"], "qid": link["qid"],
                       "revision": raw["revision"], "url": "https://ru.wikipedia.org/w/index.php?oldid=" + str(raw["revision"]),
                       "production_paragraphs": raw["production_paragraphs"], "eligible_paragraphs": raw["eligible_paragraphs"],
                       "selected_paragraphs": len(chosen), "license": "https://creativecommons.org/licenses/by-sa/4.0/"})
        if (n + 1) % 10 == 0:
            print("wikipedia", n + 1, flush=True)
    frozen(root / "wikipedia.json", {"catalog_sha256": digest(catalog), "wikidata_sha256": digest(mapping), "rows": output, "passages": passages})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--phase", choices=CAPS, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--env-file", type=Path)
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True, mode=0o700)
    frozen(args.out / "protocol.json", {"day": DAY, "seed": SEED, "caps": CAPS, "specifications": specifications(),
        "sampling": "10 per group; seeded shuffle; soft primary-genre cap 3, then fill; global ID deduplication",
        "identity": "TMDB QID + reverse P4947, IMDb consistency, title label/alias and year within one, Wikipedia QID",
        "paragraphs": "whole production paragraphs 20..140 words; four evenly spaced per film; no AI/taste filtering",
        "code_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()})
    import fcntl
    with (args.out / ".lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.phase in ("catalog", "external"):
            if not args.env_file:
                raise ValueError("Remote credential file required")
            globals()[args.phase](args.out, token(args.env_file))
        else:
            globals()[args.phase](args.out)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError) as error:
        raise SystemExit("Catalog experiment stopped (" + type(error).__name__ + ")") from None
