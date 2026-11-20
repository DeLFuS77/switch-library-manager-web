"""Tests of build_dlc_titles.py: the DLC of each game, from titles.json.

Usage: python -m unittest discover -s .github/scripts -p "test_*.py"
"""
import unittest

import build_dlc_titles as builder


class DLCTitlesTest(unittest.TestCase):
    def test_groups_the_dlc_by_their_game(self):
        titles = {
            "0100a1b002c4e000": {"id": "0100A1B002C4E000", "name": "Star Courier", "releaseDate": 20240110},
            "0100a1b002c4e800": {"id": "0100A1B002C4E800", "name": "Star Courier", "version": 393216},
            "0100a1b002c4f002": {"id": "0100A1B002C4F002", "name": "Nebula Outfits", "releaseDate": 20250204},
            "0100a1b002c4f001": {"id": "0100A1B002C4F001", "name": " Comet Pack ", "releaseDate": 20250204},
            # the DLC of a game are in the group above it: the ones of ...A000 from ...B001
            "0100f2a00d31b001": {"id": "0100F2A00D31B001", "name": "Desert Track Pack", "releaseDate": 20250505},
        }
        self.assertEqual(builder.build(titles), {
            "0100A1B002C4E000": {
                "0100A1B002C4F001": {"name": "Comet Pack", "releaseDate": 20250204},
                "0100A1B002C4F002": {"name": "Nebula Outfits", "releaseDate": 20250204},
            },
            "0100F2A00D31A000": {"0100F2A00D31B001": {"name": "Desert Track Pack", "releaseDate": 20250505}},
        })

    def test_keeps_a_dlc_without_a_name_or_a_day(self):
        titles = {"0100F2A00D31B00C": {"id": None, "name": None, "releaseDate": None},
                  "0100F2A00D31B00D": {"name": "Coastal Track Pack", "releaseDate": 20261203},
                  "0100F2A00D31B00E": {"name": "Bad day", "releaseDate": 2026}}
        self.assertEqual(builder.build(titles), {"0100F2A00D31A000": {
            "0100F2A00D31B00C": {},
            "0100F2A00D31B00D": {"name": "Coastal Track Pack", "releaseDate": 20261203},
            "0100F2A00D31B00E": {"name": "Bad day"},
        }})

    def test_leaves_out_what_is_not_a_dlc(self):
        for title_id in ("0100A1B002C4E000", "0100A1B002C4E800", "0100A1B002C4F000", "0100A1B002C4F00", "XYZ", "010zA1B002C4F001"):
            self.assertIsNone(builder.game_of(title_id), title_id)
        self.assertEqual(builder.game_of("0400B8E0019D5003"), "0400B8E0019D4000")
        # an index of 0x800 has bit 12 set too: a DLC, not an update
        self.assertEqual(builder.game_of("0100A1B002C4F800"), "0100A1B002C4E000")
        self.assertEqual(builder.build({"x": "not an object", "0100A1B002C4E800": {"name": "An update"}}), {})


if __name__ == "__main__":
    unittest.main()
