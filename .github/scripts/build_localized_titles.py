"""Builds a compact titles.<lang>.json (title ID -> name/description) from titledb regional files.

Usage: build_localized_titles.py SOURCE TARGET [MORE_SOURCES...]
A language sold in several stores (Chinese: mainland China, Hong Kong, Taiwan) takes the titles of
all of them; a title found in several keeps the one of the first store.
"""
import json
import os
import sys

target = sys.argv[2]
sources = [path for path in [sys.argv[1]] + sys.argv[3:] if os.path.isfile(path)]

# Several store entries can share a title ID (bundles, "retail only" listings). Prefer the
# original listing: the one with a release date and the lowest store ID.
def preference(item):
    nsu_id, entry = item
    try:
        store_id = int(entry.get("nsuId") or nsu_id)
    except ValueError:
        store_id = float("inf")
    return (not entry.get("releaseDate"), store_id)


def titles_of(path):
    with open(path, encoding="utf-8") as f:
        regional = json.load(f)
    titles = {}
    for _, entry in sorted(regional.items(), key=preference, reverse=True):
        title_id = (entry.get("id") or "").upper()
        name = (entry.get("name") or "").strip()
        if len(title_id) != 16 or not name:
            continue
        # sorted from least to most preferred, so the preferred entry is written last
        item = {"name": name}
        description = (entry.get("description") or "").strip()
        if description:
            item["description"] = description
        titles[title_id] = item
    return titles


result = {}
for path in sources:
    titles = titles_of(path)
    added = 0
    for title_id, item in titles.items():
        if title_id not in result:
            result[title_id] = item
            added += 1
    print(f"{path}: {len(titles)} titles, {added} new")

if not result:
    raise SystemExit("no localized titles found in " + ", ".join(sources or sys.argv[1:2]))
with open(target, "w", encoding="utf-8") as f:
    json.dump(result, f, ensure_ascii=False, separators=(",", ":"))
print(f"{len(result)} localized titles written to {target}")
