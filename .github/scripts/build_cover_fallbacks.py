"""Builds titles.covers.json: covers of other regions for titles without one in titles.json.

Usage: build_cover_fallbacks.py titles.json titledb-folder output.json

titles.json comes from the US store. Some games have no cover there (or are not sold there
at all) but have one in another store; the app uses these covers for them.
"""
import json
import sys
from pathlib import Path

titles_path, titledb, target = sys.argv[1], Path(sys.argv[2]), sys.argv[3]

with open(titles_path, encoding="utf-8") as f:
    titles = json.load(f)

with_cover = set()
for key, entry in titles.items():
    title_id = (entry.get("id") or key or "").upper()
    if entry.get("iconUrl"):
        with_cover.add(title_id)

# stores in order of preference: English first, then the biggest catalogues
preferred = ["GB.en", "AU.en", "CA.en", "JP.ja", "ES.es", "FR.fr", "DE.de", "IT.it", "BR.pt", "PT.pt", "MX.es", "NL.nl", "KR.ko", "HK.zh"]
files = sorted(titledb.glob("[A-Z][A-Z].[a-z][a-z].json"))
files = [titledb / f"{name}.json" for name in preferred if (titledb / f"{name}.json").is_file()] + \
        [f for f in files if f.stem not in preferred and f.stem != "US.en"]

result = {}
for path in files:
    with open(path, encoding="utf-8") as f:
        regional = json.load(f)
    added = 0
    for entry in regional.values():
        title_id = (entry.get("id") or "").upper()
        icon = entry.get("iconUrl") or ""
        if len(title_id) != 16 or not icon or title_id in with_cover or title_id in result:
            continue
        item = {"iconUrl": icon}
        if entry.get("bannerUrl"):
            item["bannerUrl"] = entry["bannerUrl"]
        result[title_id] = item
        added += 1
    print(f"{path.name}: {added} covers")

with open(target, "w", encoding="utf-8") as f:
    json.dump(result, f, separators=(",", ":"))
print(f"{len(result)} cover fallbacks written to {target}")
