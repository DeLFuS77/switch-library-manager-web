"""Builds versions.json from the one of blawar/titledb and the versions of other sources.

Usage: merge_versions.py BLAWAR_VERSIONS TARGET [--versions FILE]... [--nx-versions FILE [--nx-changelog FILE]]

versions.json maps the title ID of a game to the versions of its update and their release
dates: {"0100000000010000": {"65536": "2017-10-26", ...}}. blawar/titledb stops for weeks at
times, so the versions of other sources are added to it, and the app still learns of new
updates then:
- --versions: a versions.json of the same kind, such as the one of borntohonk/titledb;
- --nx-versions: the versions.txt of 16BitWonder/nx-versions ("id|version", the last version
  of every update), with the dates of its FULL-CHANGELOG.md when given ("-", which the app
  reads as no date, otherwise).
Every version of every source is kept, so each game gets the highest version any of them knows;
a version several sources know keeps the date of the first one. The versions of blawar are
needed; another source that cannot be read is left out, so a source that is down only delays
the versions it adds.
"""
import argparse
import json
import re
import sys

TITLE_ID = re.compile(r"[0-9a-fA-F]{16}")
DATE = re.compile(r"\d{4}-\d{2}-\d{2}")
CHANGELOG_SECTION = re.compile(r"## (\d{4}-\d{2}-\d{2})\b")
CHANGELOG_VERSION = re.compile(r"\[([0-9A-Fa-f]{16})\](?:\[v\d+\]->)?\[v(\d+)\]")
NO_DATE = "-"


def valid_version(title_id, version):
    # the versions of updates are multiples of 0x10000; a number far from that is not one
    if not 0 < version < 1 << 32:
        return False
    return not title_id.endswith("000") or version % 0x10000 == 0


def read_versions(path, trusted):
    """Reads a versions.json; the versions of one that is not trusted are checked."""
    with open(path, encoding="utf-8") as f:
        source = json.load(f)
    if not isinstance(source, dict) or not source:
        raise ValueError("not a JSON object with versions")
    titles = {}
    for title_id, versions in source.items():
        if not TITLE_ID.fullmatch(title_id) or not isinstance(versions, dict):
            continue
        title_id = title_id.lower()
        for version, date in versions.items():
            if not str(version).isdigit():
                continue
            number = int(version)
            if not trusted and not valid_version(title_id, number):
                continue
            if not isinstance(date, str) or not DATE.fullmatch(date):
                date = NO_DATE
            titles.setdefault(title_id, {})[number] = date
    return titles


def read_changelog(path):
    """The day nx-versions first listed each version of an update: (update ID, version) -> date."""
    dates = {}
    day = None
    with open(path, encoding="utf-8") as f:
        for line in f:
            section = CHANGELOG_SECTION.match(line)
            if section:
                day = section.group(1)
                continue
            found = CHANGELOG_VERSION.fullmatch(line.strip())
            if found and day:
                key = (found.group(1).lower(), int(found.group(2)))
                # the newest section is first: the oldest day is the one kept
                dates[key] = min(day, dates.get(key, day))
    return dates


def read_nx_versions(path, changelog):
    """The last version of every update in versions.txt, under the title ID of its game."""
    dates = {}
    if changelog:
        try:
            dates = read_changelog(changelog)
        except (OSError, ValueError) as error:
            print(f"{changelog} is left out, the versions of nx-versions have no date: {error}", file=sys.stderr)
    titles = {}
    with open(path, encoding="utf-8") as f:
        if f.readline().strip() != "id|version":
            raise ValueError("not a versions.txt of nx-versions")
        for line in f:
            parts = line.strip().split("|")
            if len(parts) != 2 or not TITLE_ID.fullmatch(parts[0]) or not parts[1].isdigit():
                continue
            update_id, version = parts[0].lower(), int(parts[1])
            # only updates: the versions of DLC come from titles.json
            if not update_id.endswith("800") or int(update_id, 16) & 0x1000:
                continue
            title_id = update_id[:-3] + "000"
            if valid_version(title_id, version):
                titles.setdefault(title_id, {})[version] = dates.get((update_id, version), NO_DATE)
    return titles


def merge(result, titles, name):
    added_versions, added_titles = 0, 0
    for title_id, versions in titles.items():
        if title_id not in result:
            added_titles += 1
        known = result.setdefault(title_id, {})
        for version, date in versions.items():
            if version not in known:
                known[version] = date
                added_versions += 1
    print(f"{name}: {len(titles)} titles, {added_versions} new versions ({added_titles} new titles)")


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("blawar")
    parser.add_argument("target")
    parser.add_argument("--versions", action="append", default=[])
    parser.add_argument("--nx-versions")
    parser.add_argument("--nx-changelog")
    args = parser.parse_args()

    result = {}
    merge(result, read_versions(args.blawar, True), args.blawar)

    sources = [(path, lambda path=path: read_versions(path, False)) for path in args.versions]
    if args.nx_versions:
        sources.append((args.nx_versions, lambda: read_nx_versions(args.nx_versions, args.nx_changelog)))
    for name, read in sources:
        try:
            titles = read()
        except (OSError, ValueError) as error:
            print(f"{name} is left out: {error}", file=sys.stderr)
            continue
        merge(result, titles, name)

    # in a fixed order, so the same versions give the same file
    output = {title_id: {str(version): result[title_id][version] for version in sorted(result[title_id])}
              for title_id in sorted(result)}
    with open(args.target, "w", encoding="utf-8") as f:
        json.dump(output, f, separators=(",", ":"))
    print(f"{len(output)} titles written to {args.target}")


if __name__ == "__main__":
    main()
