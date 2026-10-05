"""Builds a compact titles.<lang>.json (title ID -> name/description) from a titledb regional file."""
import json
import sys

source, target = sys.argv[1], sys.argv[2]
with open(source, encoding="utf-8") as f:
    regional = json.load(f)

# Several store entries can share a title ID (bundles, "retail only" listings). Prefer the
# original listing: the one with a release date and the lowest store ID.
def preference(item):
    nsu_id, entry = item
    try:
        store_id = int(entry.get("nsuId") or nsu_id)
    except ValueError:
        store_id = float("inf")
    return (not entry.get("releaseDate"), store_id)


result = {}
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
    result[title_id] = item

if not result:
    raise SystemExit("no localized titles found in " + source)
with open(target, "w", encoding="utf-8") as f:
    json.dump(result, f, ensure_ascii=False, separators=(",", ":"))
print(f"{len(result)} localized titles written to {target}")
