"""Builds titles.series.json: the series of Nintendo Switch games, from Wikidata.

Wikidata links games to their series ("part of the series", P179) and many of them to
their Switch title ID (P11072). The app uses this to group the games of a series exactly,
instead of guessing from their names. Wikidata is CC0, free to reuse.

Output:
{
  "series": {"Q12345": {"en": "The Legend of Zelda", "es": "...", ...}},
  "ids":    {"0100000000010000": "Q12345"},     # title IDs of base games
  "names":  {"The Legend of Zelda: Breath of the Wild": "Q12345"}   # English names and aliases
}

Usage: python build_series.py output.json
"""
import json
import sys
import time
import urllib.parse
import urllib.request

ENDPOINT = "https://query.wikidata.org/sparql"
AGENT = "switch-library-manager-web/1.0 (https://github.com/DeLFuS77/switch-library-manager-web)"
LANGUAGES = ["en", "es", "fr", "de", "it", "pt", "nl", "ru", "ja", "ko", "zh"]
SWITCH = "wd:Q19610114"

GAMES = """
SELECT DISTINCT ?g ?s ?tid ?name WHERE {
  ?g wdt:P179 ?s .
  { ?g wdt:P11072 ?any } UNION { ?g wdt:P400 %s }
  OPTIONAL { ?g wdt:P11072 ?tid }
  { ?g rdfs:label ?name } UNION { ?g skos:altLabel ?name }
  FILTER(LANG(?name) = "en")
}
""" % SWITCH

SERIES = """
SELECT DISTINCT ?s ?label WHERE {
  ?g wdt:P179 ?s .
  { ?g wdt:P11072 ?any } UNION { ?g wdt:P400 %s }
  ?s rdfs:label ?label .
  FILTER(LANG(?label) IN (%s))
}
""" % (SWITCH, ", ".join('"%s"' % lang for lang in LANGUAGES))


def query(sparql):
    data = urllib.parse.urlencode({"query": sparql}).encode()
    for attempt in range(4):
        try:
            request = urllib.request.Request(ENDPOINT, data=data, headers={
                "User-Agent": AGENT, "Accept": "application/sparql-results+json"})
            with urllib.request.urlopen(request, timeout=180) as response:
                return json.load(response)["results"]["bindings"]
        except Exception as error:  # the public endpoint is busy at times
            print("query failed (%s), retrying" % error, file=sys.stderr)
            time.sleep(15 * (attempt + 1))
    raise SystemExit("Wikidata did not answer")


def qid(binding, key):
    return binding[key]["value"].rsplit("/", 1)[-1]


def main(output):
    games = {}
    for row in query(GAMES):
        game = games.setdefault(qid(row, "g"), {"series": set(), "ids": set(), "names": set()})
        game["series"].add(qid(row, "s"))
        game["names"].add(row["name"]["value"])
        if "tid" in row:
            tid = row["tid"]["value"].strip().upper()
            # base games only: their update and DLC belong to them anyway
            if len(tid) == 16 and tid.endswith("000"):
                game["ids"].add(tid)

    # a game in several series goes to the broadest one, the one with the most Switch
    # games: "Final Fantasy" rather than "Final Fantasy Pixel Remaster"
    members = {}
    for game in games.values():
        for series in game["series"]:
            members[series] = members.get(series, 0) + 1

    def choose(candidates):
        return min(candidates, key=lambda s: (-members[s], s))

    ids, names, used = {}, {}, set()
    for game in games.values():
        series = choose(game["series"])
        used.add(series)
        for tid in game["ids"]:
            ids[tid] = series
        for name in game["names"]:
            names.setdefault(name, series)

    labels = {}
    for row in query(SERIES):
        series = qid(row, "s")
        if series in used:
            labels.setdefault(series, {})[row["label"]["xml:lang"]] = row["label"]["value"]
    # a series without an English name cannot be shown
    labels = {s: names_ for s, names_ in labels.items() if "en" in names_}
    ids = {t: s for t, s in ids.items() if s in labels}
    names = {n: s for n, s in names.items() if s in labels}

    with open(output, "w", encoding="utf-8") as target:
        json.dump({"series": labels, "ids": ids, "names": names}, target, ensure_ascii=False, sort_keys=True)
    print("%d series, %d title IDs, %d names" % (len(labels), len(ids), len(names)))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)
    main(sys.argv[1])
