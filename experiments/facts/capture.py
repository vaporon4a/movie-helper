"""Capture explicitly chosen Wikipedia paragraphs; never generate source facts."""
import hashlib
import json
import re
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from html.parser import HTMLParser
from pathlib import Path

ROOT = Path(__file__).resolve().parent
LICENSE = "https://creativecommons.org/licenses/by-sa/4.0/"


class Paragraphs(HTMLParser):
    def __init__(self):
        super().__init__()
        self.paragraphs, self.buffer = [], None
        self.skip = 0
        self.heading = None
        self.production = False

    def handle_starttag(self, tag, attrs):
        if tag == "p":
            self.buffer = []
        if tag == "h2":
            self.heading = []
        if tag == "sup":
            self.skip += 1

    def handle_endtag(self, tag):
        if tag == "sup":
            self.skip = max(0, self.skip - 1)
        if tag == "h2" and self.heading is not None:
            heading = "".join(self.heading).lower()
            self.production = any(word in heading for word in ("production", "development", "filming"))
            self.heading = None
        if tag == "p" and self.buffer is not None:
            if self.production:
                self.paragraphs.append(" ".join("".join(self.buffer).split()))
            self.buffer = None

    def handle_data(self, data):
        if self.heading is not None:
            self.heading.append(data)
        if self.buffer is not None and not self.skip:
            self.buffer.append(data)


def window(paragraph, needle):
    """A contiguous, sentence-bounded quote, with context around the anchor."""
    index = paragraph.casefold().find(needle.casefold())
    boundaries = [0] + [m.end() for m in re.finditer(r"[.!?]\s+", paragraph)] + [len(paragraph)]
    sentence = max(i for i, start in enumerate(boundaries[:-1]) if start <= index)
    start = boundaries[max(0, sentence - 1)]
    end = boundaries[min(len(boundaries) - 1, sentence + 3)]
    return paragraph[start:end].strip()


def capture(spec):
    title = spec["wiki_title"]
    url = "https://en.wikipedia.org/wiki/" + urllib.parse.quote(title, safe="()_")
    request = urllib.request.Request(url, headers={"User-Agent": "MovieHelperFactExperiment/1.0 (https://github.com/vaporon4a/movie-helper)"})
    with urllib.request.urlopen(request, timeout=30) as response:
        html = response.read(4 << 20).decode("utf-8")
    revision = re.search(r'"wgRevisionId":(\d+)', html)
    if revision is None:
        raise ValueError("Missing Wikipedia revision for " + title)
    parser = Paragraphs()
    parser.feed(html)
    source = {
        "id": spec["id"], "title": title, "display_title": spec["display_title"],
        "url": "https://en.wikipedia.org/w/index.php?oldid=" + revision[1],
        "revision": int(revision[1]), "license": LICENSE,
        "attribution": "Wikipedia contributors; excerpt normalized for whitespace, citation markers removed",
        "captured_at": datetime.now(timezone.utc).isoformat(),
    }
    passages = []
    for n, anchor in enumerate(spec["anchors"]):
        matches = [p for p in parser.paragraphs if anchor.casefold() in p.casefold()]
        if not matches:
            raise ValueError("Anchor not found: " + title + ": " + anchor)
        paragraph = matches[0]
        passages.append({
            "id": f'{spec["id"]}-{n+1}', "source_id": spec["id"],
            "paragraph": paragraph, "excerpt": window(paragraph, anchor),
            "paragraph_sha256": hashlib.sha256(paragraph.encode()).hexdigest(),
        })
    return source, passages


def main():
    specs = json.loads((ROOT / "source_specs.json").read_text())
    dataset = {"version": "fact-pilot-v1", "sources": [], "passages": [], "batches": []}
    for spec in specs:
        source, passages = capture(spec)
        dataset["sources"].append(source)
        dataset["passages"].extend(passages)
        print(source["title"], source["revision"], len(passages))
    for n in range(4):
        dataset["batches"].append({"id": f"batch-{n+1}", "passage_ids": [f'{s["id"]}-{n+1}' for s in specs]})
    target = ROOT / "dataset.json"
    if target.exists():
        raise ValueError("Dataset exists; archive it explicitly before recapturing")
    target.write_text(json.dumps(dataset, ensure_ascii=False, indent=2) + "\n")


if __name__ == "__main__":
    main()
