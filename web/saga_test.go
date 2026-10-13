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
	index := &sagaIndex{
		byKey: map[string][]string{"darksiders": {"a"}, "darksiders genesis": {"b"}, "dark": {"c"}, "dark souls": {"d"},
			"kingdom": {"e"}, "kingdom hearts": {"f"}, "kingdom two crowns": {"g"}},
		publisherOf: map[string]string{"a": "thq nordic", "b": "thq nordic", "c": "thq nordic", "d": "bandai namco",
			"e": "raw fury", "f": "square enix", "g": "raw fury"},
	}
	if index.family("darksiders genesis", "thq nordic") != sagaId("darksiders", "thq nordic") {
		t.Error("Darksiders Genesis belongs to the Darksiders games")
	}
	if index.family("dark souls", "bandai namco") != sagaId("dark souls", "bandai namco") {
		t.Error("a generic word is no series")
	}
	// a word in common is no series: another publisher, another series
	if index.family("kingdom two crowns", "raw fury") != sagaId("kingdom", "raw fury") {
		t.Error("Kingdom Two Crowns follows Kingdom of the same publisher")
	}
	if index.family("kingdom hearts", "square enix") != sagaId("kingdom hearts", "square enix") {
		t.Error("Kingdom Hearts is not in the series of Kingdom")
	}
	// games of one publisher that begin alike are a series, even without a game of that name
	index.byKey["sonic forces"], index.byKey["sonic frontiers"] = []string{"h"}, []string{"i"}
	index.publisherOf["h"], index.publisherOf["i"] = "sega", "sega"
	index.keysOf = map[string][]string{"sega": {"sonic forces", "sonic frontiers"}, "x": {"escape from a", "escape from b"}}
	index.beginTotal = map[string]int{"sonic": 2, "escape": 40, "escape from": 30}
	index.beginBy = map[string]int{sagaId("sonic", "sega"): 2, sagaId("escape", "x"): 2, sagaId("escape from", "x"): 2}
	// a beginning that the games of hundreds of publishers share is no series
	if index.family("escape from a", "x") != sagaId("escape from a", "x") {
		t.Error("two games that begin with a common word are no series")
	}
	if index.family("sonic forces", "sega") != sagaId("sonic", "sega") || index.family("sonic frontiers", "sega") != sagaId("sonic", "sega") {
		t.Error("Sonic Forces and Sonic Frontiers are one series")
	}
	if publisherKey("Raw Fury AB") != publisherKey("raw fury") || publisherKey("Nintendo Co., Ltd.") != "nintendo" {
		t.Error("company forms are left out")
	}
}

func TestSagaTitles(t *testing.T) {
	for name, want := range map[string]string{
		"Darksiders III":                          "Darksiders",
		"Super Mario Maker™ 2":                    "Super Mario Maker",
		"The Legend of Zelda: Breath of the Wild": "The Legend of Zelda",
		"DARK SOULS™ REMASTERED":                  "DARK SOULS",
		"Darkest Dungeon®":                        "Darkest Dungeon",
	} {
		if got := sagaTitle(name); got != want {
			t.Errorf("%q: got %q, want %q", name, got, want)
		}
	}
}

func TestNotAGame(t *testing.T) {
	for name, want := range map[string]bool{
		"DARK SOULS REMASTERED NETWORK TEST Ver.": true, "DAEMON X MACHINA Prototype Orders": true,
		"Super Mario Maker 2 - Media Review": true, "DARK SOULS REMASTERED": false, "Betaman": false,
	} {
		if notAGame.MatchString(name) != want {
			t.Errorf("%q: %v", name, !want)
		}
	}
	if regionalName("DARK SOULS™ Remastered") != regionalName("DARK SOULS REMASTERED") {
		t.Error("the same game with and without marks")
	}
}

func TestSameSaga(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	sequel := *switchDB.TitlesMap["0100000000020"]
	sequel.Attributes.Name = "Known Game 2"
	switchDB.TitlesMap["0100000000020"] = &sequel
	web.state.set(switchDB, localDB)
	saga := web.sameSaga("0100000000010000", "Known Game", "en")
	items := saga.Games
	if len(items) != 1 || items[0].Id != "0100000000020000" || !items[0].Missing {
		t.Fatalf("the sequel, not in the library: %+v", items)
	}
	if saga.Owned != 1 || saga.Total != 2 || saga.Percent() != 50 {
		t.Fatalf("1 of 2 games: %+v", saga)
	}
	saga = web.sameSaga("0100000000020000", "Known Game 2", "en")
	if items := saga.Games; len(items) != 1 || items[0].Missing {
		t.Fatalf("the first game, in the library: %+v", items)
	}
	if saga.Owned != 1 || saga.Total != 2 {
		t.Fatalf("the game of the page is not in the library: %+v", saga)
	}

	sagas := web.sagaProgress("en")
	if len(sagas) != 1 || sagas[0].Name != "Known Game" || sagas[0].Owned != 1 || sagas[0].Total != 2 || sagas[0].Complete() {
		t.Fatalf("the series of the library: %+v", sagas)
	}
	if missing := sagas[0].MissingGames(); len(missing) != 1 || missing[0].Id != "0100000000020000" || sagas[0].MoreMissing() != 0 {
		t.Fatalf("the missing game of the series: %+v", missing)
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

func TestSagaDemos(t *testing.T) {
	for name, want := range map[string]bool{"Super Mario Odyssey™ Demo for KIOSK": true, "Puzzle Puppers Demo": true, "Game Trial": true,
		"モンスターハンターライズ 体験版": true, "Demolition Crew": false, "Demon Turf": false, "Super Mario Odyssey™": false} {
		if sagaDemo.MatchString(name) != want {
			t.Errorf("%q: %v", name, !want)
		}
	}
}
