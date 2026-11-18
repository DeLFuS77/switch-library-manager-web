package web

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// The hours of a year are what each title grew from one day kept to the next, counted in the year
// of the later day: the first day kept is where counting starts, a title first told later counts
// whole, and a time that went down counts nothing.
func TestPlayInYear(t *testing.T) {
	history := &netPlayHistory{Days: map[string]map[string]int64{
		"2025-12-30": {"A": 100},
		"2026-01-02": {"A": 160, "B": 30},
		"2026-03-01": {"A": 200, "B": 10},
		"2026-06-01": {"B": 50},
		"2027-01-05": {"A": 400},
	}}
	for year, want := range map[int]struct {
		minutes map[string]int64
		kept    bool
	}{
		2024: {map[string]int64{}, false},
		2025: {map[string]int64{}, true},
		2026: {map[string]int64{"A": 100, "B": 70}, true},
		2027: {map[string]int64{"A": 200}, true},
	} {
		minutes, first, kept := playInYear(history, year)
		if kept != want.kept || first.Format(time.DateOnly) != "2025-12-30" || len(minutes) != len(want.minutes) {
			t.Errorf("%d: %v %v %v", year, minutes, first, kept)
			continue
		}
		for title, played := range want.minutes {
			if minutes[title] != played {
				t.Errorf("%d: %s played %d, want %d", year, title, minutes[title], played)
			}
		}
	}
	// kept since a day of the year: counted from it
	minutes, first, kept := playInYear(&netPlayHistory{Days: map[string]map[string]int64{"2026-05-01": {"A": 500}, "2026-05-02": {"A": 530}}}, 2026)
	if !kept || minutes["A"] != 30 || first.Format(time.DateOnly) != "2026-05-01" {
		t.Errorf("since May: %v %v %v", minutes, first, kept)
	}
	if minutes, _, kept := playInYear(nil, 2026); kept || len(minutes) != 0 {
		t.Error("nothing kept")
	}
}

// Your year sums the consoles of the user by game, names the three played most, and says since
// when it counts when that is in the year; nothing without a day kept that year.
func TestNetYearPlay(t *testing.T) {
	web, _, bob, console, _, keyId := consolesWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleYear()
	lite := createKey(t, bob, "Switch Lite", ROLE_CONSOLE)
	var liteId string
	for _, row := range web.auth.users.KeyRows("bob") {
		if row.Name == "Switch Lite" {
			liteId = row.ID
		}
	}
	if code := sendJSON(console, "POST", "/install/inventory", `{"items":[]}`, "Authorization", basic("switch", lite.Key)).Code; code != 200 {
		t.Fatalf("told: %d", code)
	}
	os.Remove(web.netPlayHistoryPath(keyId))
	day := func(key string, when string, play ...netPlayEntry) {
		at, _ := time.Parse(time.DateOnly, when)
		if err := web.keepNetPlayDay(key, play, at.Add(12*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	game, other, third, fourth := "0100000000010000", "0100000000020000", "0100000000030000", "0100000000040000"
	day(keyId, "2026-03-01", netPlayEntry{TitleId: game, Minutes: 1000})
	day(keyId, "2026-04-01", netPlayEntry{TitleId: game, Minutes: 1600}, netPlayEntry{TitleId: other, Minutes: 120})
	day(liteId, "2025-12-01", netPlayEntry{TitleId: game, Minutes: 50})
	day(liteId, "2026-02-01", netPlayEntry{TitleId: game, Minutes: 110}, netPlayEntry{TitleId: third, Minutes: 30}, netPlayEntry{TitleId: fourth, Minutes: 10})

	play := web.netYearPlay("bob", 2026, "en")
	if play == nil || play.Minutes != 600+60+120+30+10 || !play.Since.IsZero() || len(play.Top) != 3 {
		t.Fatalf("the year: %+v", play)
	}
	if top := play.Top[0]; top.Id != game || top.Name != "Game" || top.Minutes != 660 || play.Top[1].Id != other || play.Top[2].Minutes != 30 {
		t.Fatalf("the most played: %+v", play.Top)
	}
	if play := web.netYearPlay("bob", 2024, "en"); play != nil {
		t.Fatalf("no day kept that year: %+v", play)
	}
	if play := web.netYearPlay("alice", 2026, "en"); play != nil {
		t.Fatal("another user")
	}
	// kept only since a day of the year: since when
	os.Remove(web.netPlayHistoryPath(liteId))
	if play := web.netYearPlay("bob", 2026, "en"); play == nil || play.Since.Format(time.DateOnly) != "2026-03-01" || play.Minutes != 720 {
		t.Fatalf("since March: %+v", play)
	}

	page := bob.do("GET", "/year.html?y=2026", nil).Body.String()
	for _, want := range []string{"Hours played", "12 h", "since Mar 1", "Most played", `href="/title/0100000000010000.html">Game</a>`} {
		if !strings.Contains(page, want) {
			t.Errorf("Your year has no %s", want)
		}
	}
}

// The page of a game tells how long the user played it, on how many consoles, and how far into
// the story; the Library sorts by the time played and by when it was played last.
func TestTitlePlayedAndSorted(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	stats := netPlayStats{Minutes: 2040, Launches: 57, LastPlayed: now.Add(-3 * 24 * time.Hour).Unix()}
	story := TimeToBeat{Found: true, Normally: 48 * 3600}
	played := titlePlayed("en", stats, 2, story, now)
	if played.Text != "34 h · 57 times · last played 3 days ago" || played.Consoles != 2 || played.Percent != 70 || played.Story != "34 of 48 h · about 14 h left" {
		t.Errorf("played: %+v", played)
	}
	if played := titlePlayed("en", netPlayStats{Minutes: 3000}, 1, story, now); played.Percent != 100 || played.Story != "Played longer than the story" {
		t.Errorf("longer than the story: %+v", played)
	}
	if played := titlePlayed("en", stats, 1, TimeToBeat{Found: true, Hastily: 3600}, now); played.Story != "" || played.Percent != 0 {
		t.Errorf("without the time of the story: %+v", played)
	}
	if played := titlePlayed("es", stats, 1, story, now); played.Story != "34 de 48 h · te quedan unas 14 h" {
		t.Errorf("in Spanish: %+v", played)
	}

	web, admin, bob, _, _, _ := consolesWeb(t)
	web.HandleTitle()
	if page := bob.do("GET", "/title/0100000000010000.html", nil).Body.String(); !strings.Contains(page, "34 h · 57 times") || !strings.Contains(page, ">Played<") {
		t.Error("the page of the game tells the time played")
	}
	if page := admin.do("GET", "/title/0100000000010000.html", nil).Body.String(); strings.Contains(page, "57 times") {
		t.Error("another user played nothing")
	}

	switchDB, localDB := web.state.get()
	switchDB.TitlesMap["0100000000020"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000020000", Name: "Another Game"}}
	switchDB.TitlesMap["0100000000030"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000030000", Name: "Aardvark"}}
	web.state.set(switchDB, localDB)
	order := func(user string, sort string, direction string) (string, bool) {
		t.Helper()
		filter := &TitleItemFilter{PerPage: 96, SortBy: sort, SortOrder: direction, Page: 1, player: user}
		filter.Normalize()
		items, _, facets := web.getLibraryWithFacets(filter, "en")
		names := []string{}
		for _, item := range items {
			names = append(names, item.Name)
		}
		return strings.Join(names, ","), facets.Played
	}
	for _, c := range []struct{ sort, direction, want string }{
		{"played", "asc", "Game,Another Game,Aardvark"},
		{"played", "desc", "Another Game,Game,Aardvark"},
		{"played_recent", "asc", "Game,Another Game,Aardvark"},
		{"name", "asc", "Aardvark,Another Game,Game"},
	} {
		if got, played := order("bob", c.sort, c.direction); got != c.want || !played {
			t.Errorf("%s %s: %s", c.sort, c.direction, got)
		}
	}
	if got, played := order("alice", "played", "asc"); got != "Aardvark,Another Game,Game" || played {
		t.Errorf("never played: by name, %s", got)
	}
}
