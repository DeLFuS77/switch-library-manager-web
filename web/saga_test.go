package web

import "testing"

func TestSagaKeys(t *testing.T) {
	for name, want := range map[string]string{
		"Darkest Dungeon II":                      "darkest dungeon",
		"Darkest Dungeon®":                        "darkest dungeon",
		"Darksiders II Deathinitive Edition":      "darksiders",
		"Darksiders Genesis":                      "darksiders genesis",
		"Dadish 3D":                               "dadish",
		"Dadish 4":                                "dadish",
		"Danganronpa V3: Killing Harmony":         "danganronpa",
		"Danganronpa S: Ultimate Summer Camp":     "danganronpa",
		"The Legend of Zelda: Breath of the Wild": "legend of zelda",
		"Super Mario Maker 2":                     "super mario maker",
		"DARK SOULS™ REMASTERED":                  "dark souls",
		"Go":                                      "",
	} {
		if got := sagaKey(name); got != want {
			t.Errorf("%q: got %q, want %q", name, got, want)
		}
	}
	index := &sagaIndex{byKey: map[string][]string{"darksiders": nil, "darksiders genesis": nil, "dark": nil, "dark souls": nil}}
	if index.family("darksiders genesis") != "darksiders" {
		t.Error("Darksiders Genesis belongs to the Darksiders games")
	}
	if index.family("dark souls") != "dark souls" {
		t.Error("a generic word is no series")
	}
}

func TestSameSaga(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	sequel := *switchDB.TitlesMap["0100000000020"]
	sequel.Attributes.Name = "Known Game 2"
	switchDB.TitlesMap["0100000000020"] = &sequel
	web.state.set(switchDB, localDB)
	items := web.sameSaga("0100000000010000", "Known Game", "en")
	if len(items) != 1 || items[0].Id != "0100000000020000" || !items[0].Missing {
		t.Fatalf("the sequel, not in the library: %+v", items)
	}
	if items := web.sameSaga("0100000000020000", "Known Game 2", "en"); len(items) != 1 || items[0].Missing {
		t.Fatalf("the first game, in the library: %+v", items)
	}
}

func TestRegionalCopiesAreShownOnce(t *testing.T) {
	items := withoutRegionalCopies([]TitleItem{
		{Id: "A", Name: "Darksiders Genesis（ダークサイダーズ ジェネシス）", Missing: true},
		{Id: "B", Name: "Darksiders Genesis"},
		{Id: "C", Name: "Darksiders III（ダークサイダーズ３）", Missing: true},
		{Id: "D", Name: "Darksiders Warmastered Edition", Missing: true},
	}, "Darksiders III")
	if len(items) != 2 || items[0].Id != "B" || items[1].Id != "D" {
		t.Fatalf("one copy of each game, the owned one, without the game itself: %+v", items)
	}
}
