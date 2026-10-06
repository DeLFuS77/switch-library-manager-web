package web

import (
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

func TestUpcomingGamesByMonth(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	game := func(id, name string, release int) *db.SwitchTitle {
		return &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: name, ReleaseDate: release}}
	}
	switchDB.TitlesMap["0100000000050"] = game("0100000000050000", "Known Game 2", 20261120)
	switchDB.TitlesMap["0100000000060"] = game("0100000000060000", "Other Game", 20261020)
	// the same game in another store, a day earlier and with a translated name
	switchDB.TitlesMap["0100000000070"] = game("0100000000070000", "Other Game（アザー）", 20261019)
	switchDB.TitlesMap["0100000000080"] = game("0100000000080000", "Released Game", 20261001)
	switchDB.TitlesMap["0100000000090"] = game("0100000000090000", "Old Game", 20200101)
	switchDB.TitlesMap["01000000000A0"] = game("01000000000A0000", "Other Game NETWORK TEST", 20261021)
	web.state.set(switchDB, localDB)
	web.wishes().set("0100000000070000", true)

	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.Local)
	games := web.upcomingGamesAt("en", now)
	if len(games) != 3 {
		t.Fatalf("the released game, the other game once and the sequel: %+v", games)
	}
	other := games[1]
	if other.Item.Id != "0100000000060000" || !other.Item.Wished || other.Item.ReleaseDate.Day() != 19 {
		t.Fatalf("one copy, without the translation, the earliest date, wished: %+v", other.Item)
	}
	if games[2].Saga == nil || games[2].Saga.Owned != 1 || !games[2].ForYou() {
		t.Fatalf("the sequel of a game of the library: %+v", games[2])
	}

	page := upcomingPage(games, &TitleItemFilter{}, now)
	if page.Total != 2 || page.ForYou != 2 || len(page.JustReleased) != 1 || len(page.Months) != 2 || page.Months[0].Month.Month() != time.October {
		t.Fatalf("by month: %+v", page)
	}
	if page.Featured == nil || page.Featured.Item.Id != "0100000000060000" || page.FeaturedDays != 13 {
		t.Fatalf("the next game for the user is featured: %+v %d", page.Featured, page.FeaturedDays)
	}
	page = upcomingPage(games, &TitleItemFilter{Status: STATUS_FOR_YOU}, now)
	if page.Shown != 2 || len(page.JustReleased) != 0 {
		t.Fatalf("for you: %+v", page)
	}
	if page = upcomingPage(games, &TitleItemFilter{Keyword: "known"}, now); page.Shown != 1 {
		t.Fatalf("search: %+v", page)
	}
}

func TestMonthsAndDays(t *testing.T) {
	date := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	for lang, want := range map[string]string{"en": "October 2026", "es": "Octubre de 2026", "de": "Oktober 2026", "ja": "2026年10月", "ko": "2026년 10월"} {
		if got := formatMonth(lang, date); got != want {
			t.Errorf("%s month = %q, want %q", lang, got, want)
		}
	}
	for lang, want := range map[string]string{"en": "Oct 8", "es": "8 oct", "de": "8. Okt.", "zh": "10月8日"} {
		if got := formatDay(lang, date); got != want {
			t.Errorf("%s day = %q, want %q", lang, got, want)
		}
	}
}
