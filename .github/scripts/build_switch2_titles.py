"""Builds titles.switch2.json: the games of the Nintendo Switch 2 alone, from switchbrew.

titledb lists few games of the Switch 2 (title IDs from 04). The title list of switchbrew,
kept by hand by its editors, has the others: their title ID, name, regions and how they are
sold. The app adds the games titles.json does not list, and asks IGDB for their cover,
release date and description when the user set it up.

Output:
{
  "source": "https://switchbrew.org/wiki/Switch_2:_Title_list/Games",
  "titles": {"0400C3F00006E000": {"name": "Mario Kart World", "regions": ["EU", "US"], "sold": ["digital"]}}
}

Usage: python build_switch2_titles.py output.json [page.wiki]
With a saved page (the raw wikitext), nothing is downloaded.
"""
import json
import re
import sys
import time
import urllib.request

PAGE = "https://switchbrew.org/wiki/Switch_2:_Title_list/Games"
RAW = "https://switchbrew.org/w/index.php?title=Switch_2:_Title_list/Games&action=raw"
AGENT = "switch-library-manager-web/1.0 (https://github.com/DeLFuS77/switch-library-manager-web)"
REGIONS = {"USA": "US", "EUR": "EU", "JPN": "JP", "KOR": "KR", "CHN": "CN"}
SOLD = {"digital": "digital", "cartridge": "cartridge", "game key card": "key card", "game-key card": "key card", "key card": "key card"}
TITLE_ID = re.compile(r"^04[0-9A-F]{14}$")


def download():
    for attempt in range(4):
        try:
            request = urllib.request.Request(RAW, headers={"User-Agent": AGENT})
            with urllib.request.urlopen(request, timeout=60) as response:
                return response.read().decode("utf-8")
        except Exception as error:  # the wiki is busy at times
            print("switchbrew did not answer (%s), retrying" % error, file=sys.stderr)
            time.sleep(10 * (attempt + 1))
    raise SystemExit("switchbrew did not answer")


def plain(text):
    """The text of a cell of the wiki, without its markup."""
    text = re.sub(r"<ref[^>]*/>|<ref[^>]*>.*?</ref>", "", text)
    text = re.sub(r"\[\[(?:[^|\]]*\|)?([^\]]*)\]\]", r"\1", text)
    text = re.sub(r"\[https?://\S+\s+([^\]]*)\]", r"\1", text)
    text = re.sub(r"\{\{[^}]*\}\}", "", text)
    text = re.sub(r"<[^>]+>", "", text)
    text = text.replace("'''", "").replace("''", "").replace("&amp;", "&").replace("&nbsp;", " ")
    return " ".join(text.split())


def games(wiki):
    """The rows of the table of the games: the first one of the page, until its end."""
    rows = []
    table = False
    for line in wiki.splitlines():
        line = line.strip()
        if line.startswith("{|"):
            if rows or table:
                break
            continue
        if line.startswith("!") and "ApplicationId" in line:
            table = True
            continue
        if line.startswith("|}"):
            if table:
                break
            continue
        if table and line.startswith("|") and not line.startswith("|-"):
            rows.append([cell.strip() for cell in line[1:].split("||")])
    return rows


def build(wiki):
    titles = {}
    names = {}
    for cells in games(wiki):
        if len(cells) < 5:
            continue
        title_id, name = cells[0].upper(), plain(cells[1])
        # games only: an ID that does not end with 000 is of a DLC or an update
        if not TITLE_ID.match(title_id) or int(title_id, 16) & 0xFFF or not name.strip(" -_.?"):
            continue
        names.setdefault(title_id, set()).add(name)
        if title_id in titles:
            continue
        regions = sorted({REGIONS[code] for code in plain(cells[2]).upper().split() if code in REGIONS})
        sold = []
        for way in re.split(r"[/,]|\s{2,}", plain(cells[4]).lower()):
            way = SOLD.get(way.strip())
            if way and way not in sold:
                sold.append(way)
        entry = {"name": name}
        if regions:
            entry["regions"] = regions
        if sold:
            entry["sold"] = sold
        titles[title_id] = entry
    # an ID the list gives two names is left out: which one is right is not known
    for title_id, found in names.items():
        if len(found) > 1:
            print("left out %s, named %s" % (title_id, " and ".join(sorted(found))), file=sys.stderr)
            titles.pop(title_id, None)
    return {"source": PAGE, "titles": dict(sorted(titles.items()))}


def main():
    if len(sys.argv) not in (2, 3):
        raise SystemExit(__doc__)
    if len(sys.argv) == 3:
        with open(sys.argv[2], encoding="utf-8") as source:
            wiki = source.read()
    else:
        wiki = download()
    result = build(wiki)
    if len(result["titles"]) < 50:
        raise SystemExit("only %d games of the Switch 2 were found: the page changed" % len(result["titles"]))
    with open(sys.argv[1], "w", encoding="utf-8", newline="\n") as output:
        json.dump(result, output, ensure_ascii=False, indent=1, sort_keys=False)
        output.write("\n")
    print("%d games of the Switch 2" % len(result["titles"]))


if __name__ == "__main__":
    main()
