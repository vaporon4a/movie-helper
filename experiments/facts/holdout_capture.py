"""Freeze a predeclared ten-film RU corpus, without editorial cherry-picking."""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import hashlib
from html.parser import HTMLParser
import json
from pathlib import Path
import re
import urllib.parse
import urllib.request

FILMS = [
    ("matrix", "Матрица_(фильм)", "Матрица"),
    ("back", "Назад_в_будущее", "Назад в будущее"),
    ("fight", "Бойцовский_клуб_(фильм)", "Бойцовский клуб"),
    ("shining", "Сияние_(фильм,_1980)", "Сияние"),
    ("amelie", "Амели", "Амели"),
    ("lotr", "Властелин_колец:_Братство_Кольца", "Властелин колец: Братство Кольца"),
    ("titanic", "Титаник_(фильм,_1997)", "Титаник"),
    ("alien", "Чужой_(фильм)", "Чужой"),
    ("ivan", "Иван_Васильевич_меняет_профессию", "Иван Васильевич меняет профессию"),
    ("diamond", "Бриллиантовая_рука", "Бриллиантовая рука"),
]
SECTION = re.compile(r"создан|съ[её]м|производ|разработ|сценари|спецэфф|кастинг|работа над фильмом", re.I)


class RussianParagraphs(HTMLParser):
    def __init__(self):
        super().__init__()
        self.heading = None
        self.buffer = None
        self.section = ""
        self.skip = 0
        self.hidden = 0
        self.paragraphs = []

    def handle_starttag(self, tag, attrs):
        if tag == "h2":
            self.heading = []
        if tag == "p":
            self.buffer = []
        if tag == "sup":
            self.skip += 1
        if tag in ("style", "script"):
            self.hidden += 1

    def handle_endtag(self, tag):
        if tag == "sup":
            self.skip = max(0, self.skip - 1)
        if tag in ("style", "script"):
            self.hidden = max(0, self.hidden - 1)
        if tag == "h2" and self.heading is not None:
            self.section = "".join(self.heading)
            self.heading = None
        if tag == "p" and self.buffer is not None:
            text = " ".join("".join(self.buffer).split())
            if SECTION.search(self.section) and text:
                self.paragraphs.append((self.section, text))
            self.buffer = None

    def handle_data(self, data):
        if self.heading is not None:
            self.heading.append(data)
        if self.buffer is not None and not self.skip and not self.hidden:
            self.buffer.append(data)


def capture(spec):
    ident, title, display = spec
    try:
        url = "https://ru.wikipedia.org/wiki/" + urllib.parse.quote(title, safe="()_:,")
        req = urllib.request.Request(url, headers={"User-Agent": "MovieHelperFactExperiment/1.0 (https://github.com/vaporon4a/movie-helper)"})
        with urllib.request.urlopen(req, timeout=30) as response:
            raw = response.read(4 << 20).decode("utf-8")
        revision = re.search(r'"wgRevisionId":(\d+)', raw)
        if not revision:
            raise ValueError("missing_revision")
        parser = RussianParagraphs()
        parser.feed(raw)
        eligible = [(section, p) for section, p in parser.paragraphs
                    if 20 <= len(p.split()) <= 140 and p[-1] in ".!?»”"]
        # Spread over the entire eligible pool; no anchors or taste filtering.
        indices = list(range(len(eligible))) if len(eligible) <= 12 else [i * (len(eligible) - 1) // 11 for i in range(12)]
        source = {"id": ident, "title": title, "display_title": display, "lang": "ru",
                  "url": "https://ru.wikipedia.org/w/index.php?oldid=" + revision[1],
                  "revision": int(revision[1]), "license": "https://creativecommons.org/licenses/by-sa/4.0/",
                  "attribution": "Wikipedia contributors; whole paragraph, whitespace normalized, citation markers removed",
                  "captured_at": datetime.now(timezone.utc).isoformat()}
        passages = [{"id": f"{ident}-{i+1}", "source_id": ident, "paragraph": eligible[index][1],
                     "excerpt": eligible[index][1], "section": eligible[index][0],
                     "paragraph_sha256": hashlib.sha256(eligible[index][1].encode()).hexdigest()}
                    for i, index in enumerate(indices)]
        return source, passages, {"id": ident, "status": "captured", "production_paragraphs": len(parser.paragraphs),
                                  "eligible_paragraphs": len(eligible), "sampled": len(passages)}
    except (OSError, ValueError):
        return None, [], {"id": ident, "status": "capture_failed"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    if args.out.exists():
        raise ValueError("Corpus already exists")
    with ThreadPoolExecutor(max_workers=3) as pool:
        captures = list(pool.map(capture, FILMS))
    data = {"version": "ru-holdout-extractive-v2", "sources": [], "passages": [], "batches": [],
            "protocol": {"films": FILMS, "words": [20, 140], "max_per_film": 12,
                         "selection": "evenly spaced eligible whole production paragraphs; no editorial anchors"}, "coverage": []}
    for source, passages, coverage in captures:
        if source:
            data["sources"].append(source)
        data["passages"].extend(passages)
        data["coverage"].append(coverage)
    # Four fixed groups. Capture failures remain visible, never replaced.
    for n, group in enumerate((FILMS[:3], FILMS[3:6], FILMS[6:8], FILMS[8:])):
        ids = {s[0] for s in group}
        passages = [p["id"] for p in data["passages"] if p["source_id"] in ids]
        if passages:
            data["batches"].append({"id": f"holdout-{n+1}", "passage_ids": passages})
    args.out.parent.mkdir(parents=True, exist_ok=True)
    with args.out.open("x") as stream:
        json.dump(data, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    print(json.dumps(data["coverage"], ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
