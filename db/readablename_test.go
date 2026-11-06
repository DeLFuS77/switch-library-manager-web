package db

import "testing"

// A name taken from a file or dump name is made readable when the file gives none of its own,
// and a name already readable is kept as it is.
func TestReadableTitleName(t *testing.T) {
	for name, want := range map[string]string{
		// a NUP made of a dump without its control data, named by its folder
		"NintendoSwitchSportsResortv1.1.0 ": "Nintendo Switch Sports Resort",
		"NintendoSwitchSportsResort_v1.1.0": "Nintendo Switch Sports Resort",
		"Super_Mario_Odyssey_v1.3.0":        "Super Mario Odyssey",
		"Game Name v1.0.2":                  "Game Name",
		"Game Name 1.0":                     "Game Name",
		// a number without a dot is part of the name
		"FIFA 23":       "FIFA 23",
		"Splatoon 3":    "Splatoon 3",
		"Mario Kart 8 ": "Mario Kart 8",
		// one or two words written together, and names with spaces, keep their letters
		"McPixel":                "McPixel",
		"eBaseball Powerful Pro": "eBaseball Powerful Pro",
		"DOOM":                   "DOOM",
		"The LEGO Movie":         "The LEGO Movie",
		// nothing left: the name as it was
		"v1.0.0": "v1.0.0",
		"":       "",
	} {
		if got := ReadableTitleName(name); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}
