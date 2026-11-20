"""Builds titles.dlc.json: the DLC of each game, from titles.json.

titles.json has every DLC titledb knows (over 20,000), but it is 125 MB: a program that only
wants to know which DLC a game has, as NXPress to tell the ones missing from a list of files,
gets them from this file instead, a fraction of its size. versions.json does not do: it lists
only the DLC that had a new version.

A DLC is a title whose ID has bit 12 set and an index in its last three digits; its game is the
same ID with both taken away (0100A1B002C4F001 is a DLC of 0100A1B002C4E000). Each DLC keeps its
name and the day it came out (as titles.json gives it, 20250204), when titles.json has them: a
DLC with neither is still a DLC of the game.

Output, by the title ID of each game, upper case:
{"0100A1B002C4E000": {"0100A1B002C4F001": {"name": "Comet Pack", "releaseDate": 20250204}}}

Usage: python build_dlc_titles.py titles.json output.json
"""
import json
import sys


def game_of(title_id):
    """The title ID of the game of a DLC (upper case), or None when the ID is not of a DLC."""
    if len(title_id) != 16:
        return None
    try:
        value = int(title_id, 16)
    except ValueError:
        return None
    # a DLC has bit 12 set and an index from 1: an update (800, without bit 12) or a game (000)
    # is not one
    if not value & 0x1000 or not value & 0xFFF:
        return None
    return f"{value & ~0x1FFF:016X}"


def build(titles):
    """The DLC of each game of titles.json, sorted by the ID of the game and of each DLC."""
    games = {}
    for key, entry in titles.items():
        if not isinstance(entry, dict):
            continue
        title_id = (entry.get("id") or key or "").upper()
        game = game_of(title_id)
        if game is None:
            continue
        item = {}
        name = (entry.get("name") or "").strip()
        if name:
            item["name"] = name
        released = entry.get("releaseDate")
        # a day as titles.json writes it: 8 digits, from 2017 on
        if isinstance(released, int) and 20170101 <= released <= 29991231:
            item["releaseDate"] = released
        games.setdefault(game, {})[title_id] = item
    return {game: dict(sorted(dlc.items())) for game, dlc in sorted(games.items())}


def main():
    if len(sys.argv) != 3:
        raise SystemExit("Usage: build_dlc_titles.py titles.json output.json")
    with open(sys.argv[1], encoding="utf-8") as source:
        titles = json.load(source)
    result = build(titles)
    with open(sys.argv[2], "w", encoding="utf-8") as target:
        json.dump(result, target, ensure_ascii=False, separators=(",", ":"))
    print(f"{sum(len(dlc) for dlc in result.values())} DLC of {len(result)} games written to {sys.argv[2]}")


if __name__ == "__main__":
    main()
