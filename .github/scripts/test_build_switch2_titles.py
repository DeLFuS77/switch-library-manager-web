"""Tests of build_switch2_titles.py: the list of switchbrew and what Wikidata adds to it.

Usage: python -m unittest discover -s .github/scripts -p "test_*.py"
"""
import unittest

import build_switch2_titles as builder

WIKI = """
{| class="wikitable sortable"
! ApplicationId !! Name !! Region !! Version !! Sold
|-
| 0400C3F00006E000 || [[Mario Kart World]] || USA EUR JPN || 0 || Cartridge / Digital
|-
| 04000000000A0000 || Gecko Gods || USA || 0 || Digital
|-
| 04000000000B0000 || シャインポスト Be Your アイドル！ || JPN || 0 || Game-key card
|-
| 04000000000C0000 || Shared Alias || EUR || 0 || Digital
|-
| 04000000000D0000 || EA SPORTS™ Madden NFL 27 for Nintendo Switch™ 2 || USA || 0 || Digital
|-
| 04000000000D0800 || An update || USA || 0 || Digital
|}
"""


def row(item, name, tid=None, igdb=None, date=None, precision=11, console=None, others=False):
    """A row of the answer of Wikidata."""
    result = {"g": {"value": "http://www.wikidata.org/entity/" + item}, "name": {"value": name},
              "others": {"value": "true" if others else "false"}}
    if tid:
        result["tid"] = {"value": tid}
    if igdb:
        result["igdb"] = {"value": igdb}
    if date:
        result["date"] = {"value": date + "T00:00:00Z"}
        result["precision"] = {"value": str(precision)}
    if console:
        result["console"] = {"value": "http://www.wikidata.org/entity/" + console}
    return result


class BuildTest(unittest.TestCase):
    def test_games_of_the_list(self):
        titles = builder.build(WIKI)["titles"]
        self.assertEqual(sorted(titles), ["04000000000A0000", "04000000000B0000", "04000000000C0000", "04000000000D0000",
                                          "0400C3F00006E000"])
        self.assertEqual(titles["0400C3F00006E000"], {"name": "Mario Kart World", "regions": ["EU", "JP", "US"],
                                                      "sold": ["cartridge", "digital"]})

    def test_wikidata(self):
        result = builder.build(WIKI)
        rows = [
            # by its title ID, a date of the Switch 2 alone without a console
            row("Q1", "Mario Kart World", tid="0400C3F00006E000", igdb="mario-kart-world", date="2025-06-05"),
            # by its name, the date of the Switch 2 of a game out on the Switch too
            row("Q2", "Gecko Gods", igdb="gecko-gods--1", date="2024-01-01", console="Q19610114", others=True),
            row("Q2", "Gecko Gods", igdb="gecko-gods--1", date="2026-06-25", console=builder.SWITCH2, others=True),
            # by its Japanese name; a date of the year only is not the day
            row("Q3", "Shine Post: Be Your Idol!", igdb="shine-post-be-your-idol"),
            row("Q3", "シャインポスト Be Your アイドル！", igdb="shine-post-be-your-idol", date="2025-01-01", precision=9),
            # a name two games have: neither is taken
            row("Q4", "Shared Alias", igdb="first-game"),
            row("Q5", "Shared Alias", igdb="second-game"),
            # by its name without the marks and the console
            row("Q6", "EA Sports Madden NFL 27", igdb="madden-nfl-27"),
            # a date without a console of a game out on other consoles too is not of the Switch 2
            row("Q6", "EA Sports Madden NFL 27", igdb="madden-nfl-27", date="2026-08-13", others=True),
        ]
        linked = builder.add_wikidata(result, rows)
        titles = result["titles"]
        self.assertEqual(titles["0400C3F00006E000"]["igdb"], "mario-kart-world")
        self.assertEqual(titles["0400C3F00006E000"]["date"], "2025-06-05")
        self.assertEqual(titles["04000000000A0000"]["date"], "2026-06-25")
        self.assertEqual(titles["04000000000B0000"]["igdb"], "shine-post-be-your-idol")
        self.assertNotIn("date", titles["04000000000B0000"])
        self.assertNotIn("igdb", titles["04000000000C0000"])
        self.assertEqual(titles["04000000000D0000"]["igdb"], "madden-nfl-27")
        self.assertNotIn("date", titles["04000000000D0000"])
        self.assertEqual(linked, 4)

    def test_without_wikidata(self):
        result = builder.build(WIKI)
        self.assertEqual(builder.add_wikidata(result, []), 0)
        self.assertNotIn("igdb", result["titles"]["0400C3F00006E000"])


if __name__ == "__main__":
    unittest.main()
