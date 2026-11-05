"""Builds titles.switch2.json: the games of the Nintendo Switch 2 alone, from switchbrew.

titledb lists few games of the Switch 2 (title IDs from 04). The title list of switchbrew,
kept by hand by its editors, has the others: their title ID, name, regions and how they are
sold. The app adds the games titles.json does not list, and asks IGDB for their cover,
release date and description when the user set it up.

Wikidata (CC0, free to reuse) knows many of these games by their names in every language and
links them to IGDB: each game it finds gets the slug of its game on IGDB, which the app asks
first, and the day it came out on the Switch 2, when Wikidata has it to the day. A game whose
name is in Japanese in the list, or that IGDB names otherwise, is found this way.

Output:
{
  "source": "https://switchbrew.org/wiki/Switch_2:_Title_list/Games",
  "titles": {"0400C3F00006E000": {"name": "Mario Kart World", "regions": ["EU", "US"], "sold": ["digital"],
                                  "igdb": "mario-kart-world", "date": "2025-06-05"}}
}

Usage: python build_switch2_titles.py output.json [page.wiki]
With a saved page (the raw wikitext), switchbrew is not downloaded.
"""
import json
import re
import sys
import time
import unicodedata
import urllib.parse
import urllib.request

PAGE = "https://switchbrew.org/wiki/Switch_2:_Title_list/Games"
RAW = "https://switchbrew.org/w/index.php?title=Switch_2:_Title_list/Games&action=raw"
AGENT = "switch-library-manager-web/1.0 (https://github.com/DeLFuS77/switch-library-manager-web)"
REGIONS = {"USA": "US", "EUR": "EU", "JPN": "JP", "KOR": "KR", "CHN": "CN"}
SOLD = {"digital": "digital", "cartridge": "cartridge", "game key card": "key card", "game-key card": "key card", "key card": "key card"}
TITLE_ID = re.compile(r"^04[0-9A-F]{14}$")

WIKIDATA = "https://query.wikidata.org/sparql"
# the games of the Nintendo Switch 2 (Q122761124) with their names and aliases in every language,
# their Switch title IDs (P11072), their game on IGDB (P5794, its slug) and their release dates
# (P577) with the console each one is of and how exact it is; ?others tells a game out on other
# consoles too, whose dates without a console may be of another one
WIKIDATA_GAMES = """
SELECT ?g ?name ?tid ?igdb ?date ?precision ?console ?others WHERE {
  ?g wdt:P400 wd:Q122761124 .
  BIND(EXISTS { ?g wdt:P400 ?other . FILTER(?other != wd:Q122761124) } AS ?others)
  { ?g rdfs:label ?name } UNION { ?g skos:altLabel ?name }
  OPTIONAL { ?g wdt:P11072 ?tid }
  OPTIONAL { ?g wdt:P5794 ?igdb }
  OPTIONAL { ?g p:P577 ?released . ?released psv:P577 [ wikibase:timeValue ?date ; wikibase:timePrecision ?precision ] .
             OPTIONAL { ?released pq:P400 ?console } }
}
"""
SWITCH2 = "Q122761124"
# precision of a date of Wikidata that gives the day
DAY = 11
SLUG = re.compile(r"^[a-z0-9][a-z0-9-]*$")


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


def wikidata_rows():
    data = urllib.parse.urlencode({"query": WIKIDATA_GAMES}).encode()
    for attempt in range(4):
        try:
            request = urllib.request.Request(WIKIDATA, data=data, headers={
                "User-Agent": AGENT, "Accept": "application/sparql-results+json"})
            with urllib.request.urlopen(request, timeout=180) as response:
                return json.load(response)["results"]["bindings"]
        except Exception as error:  # the public endpoint is busy at times
            print("Wikidata did not answer (%s), retrying" % error, file=sys.stderr)
            time.sleep(15 * (attempt + 1))
    return None


def plain_name(name):
    """A name to compare: without marks, case and punctuation."""
    name = unicodedata.normalize("NFKC", name).lower()
    name = re.sub(r"[™®©]", "", name)
    return " ".join(re.sub(r"[^\w]+", " ", name).split())


def name_keys(name):
    """The forms of a name of the list a game of Wikidata is looked for by."""
    bare = re.sub(r"(?i)\bfor nintendo switch\s*2\b|\bstandard edition\b", " ", unicodedata.normalize("NFKC", name))
    return [key for key in dict.fromkeys([plain_name(name), plain_name(bare)]) if key]


def wikidata_games(rows):
    """The games of Wikidata by item: their names, title IDs, slug of IGDB and the first day
    they came out on the Switch 2."""
    games = {}
    for row in rows:
        item = row["g"]["value"].rsplit("/", 1)[-1]
        game = games.setdefault(item, {"names": set(), "ids": set(), "igdb": set(), "date": None})
        game["names"].add(plain_name(row["name"]["value"]))
        if "tid" in row:
            game["ids"].add(row["tid"]["value"].strip().upper())
        if "igdb" in row and SLUG.match(row["igdb"]["value"]):
            game["igdb"].add(row["igdb"]["value"])
        console = row.get("console", {}).get("value", "").rsplit("/", 1)[-1]
        # a date of the Switch 2, or one without a console of a game of the Switch 2 alone
        alone = row.get("others", {}).get("value") == "false"
        if "date" in row and (console == SWITCH2 or not console and alone) and int(row.get("precision", {}).get("value", 0)) >= DAY:
            day = row["date"]["value"][:10]
            if re.match(r"^\d{4}-\d{2}-\d{2}$", day) and (game["date"] is None or day < game["date"]):
                game["date"] = day
    return games


def add_wikidata(result, rows):
    """Puts in the games of the list the slug of IGDB and the release date Wikidata gives: a game
    is found by its title ID, else by its name or an alias, when one game only has it."""
    games = wikidata_games(rows)
    by_id, by_name = {}, {}
    for item, game in games.items():
        for title_id in game["ids"]:
            by_id.setdefault(title_id, set()).add(item)
        for name in game["names"]:
            by_name.setdefault(name, set()).add(item)
    linked = 0
    for title_id, entry in result["titles"].items():
        items = by_id.get(title_id, set())
        if len(items) != 1:
            items = set()
            for key in name_keys(entry["name"]):
                if by_name.get(key):
                    items = by_name[key]
                    break
        if len(items) != 1:
            continue
        game = games[next(iter(items))]
        if len(game["igdb"]) == 1:
            entry["igdb"] = next(iter(game["igdb"]))
        if game["date"]:
            entry["date"] = game["date"]
        if "igdb" in entry or "date" in entry:
            linked += 1
    return linked


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
    # without Wikidata the list goes out as it is: the app finds the games on IGDB by name
    rows = wikidata_rows()
    linked = add_wikidata(result, rows) if rows else 0
    with open(sys.argv[1], "w", encoding="utf-8", newline="\n") as output:
        json.dump(result, output, ensure_ascii=False, indent=1, sort_keys=False)
        output.write("\n")
    print("%d games of the Switch 2, %d linked to Wikidata" % (len(result["titles"]), linked))


if __name__ == "__main__":
    main()
