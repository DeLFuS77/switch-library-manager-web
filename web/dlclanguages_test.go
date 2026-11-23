package web

import (
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The language packs in the languages not chosen are not missing DLC: not in the library, on the
// page of the game (which tells them apart) or on the DLC page (which lists them apart) (#200).
func TestLanguagePacksNotChosenAreNotMissing(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	german, spanish := "0100000000011003", "0100000000011004"
	switchDB.TitlesMap["0100000000010"].Dlc[german] = db.TitleAttributes{Id: german, Name: "Game Language Pack (DE)"}
	switchDB.TitlesMap["0100000000010"].Dlc[spanish] = db.TitleAttributes{Id: spanish, Name: "Game In-Game Commentary - Spanish"}
	web.state.set(switchDB, localDB)

	missingWith := func(languages []string) ([]TitleItem, TitleDetail) {
		t.Helper()
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.DLCLanguages = languages })
		detail, ok := web.getTitleDetail("0100000000010000", "en")
		if !ok {
			t.Fatal("not found")
		}
		return web.buildMissingDLC("en"), detail
	}

	// none chosen: every pack is missing, as before
	if page, detail := missingWith(nil); len(page) != 1 || len(page[0].MissingDLCItems) != 3 || len(page[0].LanguageHidden) != 0 || detail.MissingDlc != 3 {
		t.Fatalf("none chosen: %+v, %d missing", page, detail.MissingDlc)
	}

	page, detail := missingWith([]string{"es"})
	if len(page) != 1 || len(page[0].MissingDLCItems) != 2 || page[0].DlcTotal != 3 {
		t.Fatalf("DLC page: %+v", page)
	}
	if hidden := page[0].LanguageHidden; len(hidden) != 1 || hidden[0].Id != german {
		t.Fatalf("listed apart: %+v", hidden)
	}
	if detail.MissingDlc != 2 || detail.DlcTotal != 3 {
		t.Fatalf("game: %d missing of %d", detail.MissingDlc, detail.DlcTotal)
	}
	for _, dlc := range detail.Dlc {
		if dlc.LanguageHidden != (dlc.Id == german) || dlc.Ignored {
			t.Errorf("%s: other language %v, ignored %v", dlc.Id, dlc.LanguageHidden, dlc.Ignored)
		}
	}
	for _, item := range web.buildLibrary("en") {
		if item.Id == "0100000000010000" && item.MissingDlcCount != 2 {
			t.Errorf("library: %d missing", item.MissingDlcCount)
		}
	}
}

// What the form sends is kept as known codes, each once, in the order of Settings.
func TestChosenDLCLanguages(t *testing.T) {
	got := chosenDLCLanguages([]string{"zh-hans", "ES", "xx", "es", " pt-br "})
	if len(got) != 3 || got[0] != "es" || got[1] != "pt-br" || got[2] != "zh-hans" {
		t.Fatalf("%v", got)
	}
}
