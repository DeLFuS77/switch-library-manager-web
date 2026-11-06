package web

import (
	"slices"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The games of the Switch 2 alone (title IDs from 04) are not missing from a library of the
// Switch: no list, count, series, suggestion or region shows them, but a game of the library is
// never hidden, whatever its console.
func TestSwitch2GamesAreNotMissing(t *testing.T) {
	web := newTestWeb(t)
	title := func(id string, name string, release int) *db.SwitchTitle {
		return &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: name, Region: "US", Publisher: "Lunar", ReleaseDate: release,
			Genres: []string{"Racing"}, IconUrl: "https://example.com/" + id + ".jpg"}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": title("0100000000010000", "Moon Runner", 20200101),
		"0100000000020": title("0100000000020000", "Moon Runner 2", 20220101),
		"0400000000030": title("0400000000030000", "Moon Runner 3", 20250605),
		"0400000000040": title("0400000000040000", "Star Kart", 20250605),
		"0100000000050": title("0100000000050000", "Fortnite", 20180101),
		"0400000000060": title("0400000000060000", "Fortnite", 20250605),
		"0100000000070": title("0100000000070000", "Sky Racer", 20261120),
		"0400000000080": title("0400000000080000", "Sky Racer World", 20261120),
		"0400000000090": title("0400000000090000", "Comet Rally", 20250605),
	}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": ownedGame("0100000000010000"),
		// a game of the Switch 2 the library has
		"0400000000040": ownedGame("0400000000040000"),
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})

	all := &TitleItemFilter{}
	all.Normalize()
	library, _, _ := web.getLibraryWithFacets(all, "en")
	if got, want := itemNames(library), []string{"Fortnite", "Moon Runner", "Moon Runner 2", "Sky Racer", "Star Kart"}; !slices.Equal(got, want) {
		t.Errorf("library with the missing games: %v, want %v", got, want)
	}
	if mine, _ := web.getLibrary(defaultFilter(), "en"); !slices.Equal(itemNames(mine), []string{"Moon Runner", "Star Kart"}) {
		t.Errorf("the games of the library: %v", itemNames(mine))
	}
	missing, _, _ := web.getMissingGamesWithFacets(all, "en")
	if got, want := itemNames(missing), []string{"Fortnite", "Moon Runner 2", "Sky Racer"}; !slices.Equal(got, want) {
		t.Errorf("missing games: %v, want %v", got, want)
	}
	if count := web.missingGamesCount(); count != 3 {
		t.Errorf("missing games counted: %d", count)
	}

	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.Local)
	upcoming := []string{}
	for _, game := range web.upcomingGamesAt("en", now) {
		upcoming = append(upcoming, game.Item.Name)
	}
	if !slices.Equal(upcoming, []string{"Sky Racer"}) {
		t.Errorf("upcoming: %v", upcoming)
	}

	recommended := []string{}
	for _, recommendation := range web.recommendationsAt("en", now) {
		recommended = append(recommended, recommendation.Item.Name)
	}
	if len(recommended) == 0 || slices.Contains(recommended, "Comet Rally") {
		t.Errorf("suggested: %v", recommended)
	}

	found := false
	for _, saga := range web.sagaProgress("en") {
		for _, game := range saga.Games {
			if game.Name == "Moon Runner" {
				found = true
				if saga.Total != 2 || saga.Owned != 1 {
					t.Errorf("series: %d of %d, want 1 of 2", saga.Owned, saga.Total)
				}
			}
		}
	}
	if !found {
		t.Error("the series of the library is shown")
	}

	// a game of the Switch 2 is no copy of the one of the Switch of its name
	if regions := web.buildRegionsByName()[regionalName("Fortnite")]; !slices.Equal(regions, []string{"US"}) {
		t.Errorf("regions of the game: %v", regions)
	}
}

// The Missing games are chosen by console too, counted before that choice as in the Library: with
// the Switch 2 owned, the games of the Switch 2 missing apart from the ones of the Switch.
func TestMissingGamesByConsole(t *testing.T) {
	web := newTestWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HasSwitch2 = true })
	title := func(id string, name string) *db.SwitchTitle {
		return &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: name, Region: "US", ReleaseDate: 20250605, IconUrl: "https://example.com/" + id + ".jpg"}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": title("0100000000010000", "Moon Runner"),
		"0100000000020": title("0100000000020000", "Moon Runner 2"),
		"0400000000030": title("0400000000030000", "Moon Runner 3"),
	}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})

	for console, want := range map[string][]string{"": {"Moon Runner", "Moon Runner 2", "Moon Runner 3"}, CONSOLE_SWITCH: {"Moon Runner", "Moon Runner 2"}, CONSOLE_SWITCH2: {"Moon Runner 3"}} {
		filter := defaultFilter()
		filter.Console = console
		items, _, facets := web.getMissingGamesWithFacets(filter, "en")
		if got := itemNames(items); !slices.Equal(got, want) {
			t.Errorf("console %q: %v, want %v", console, got, want)
		}
		if facets.Switch1 != 2 || facets.Switch2 != 1 {
			t.Errorf("console %q: counted %d and %d", console, facets.Switch1, facets.Switch2)
		}
	}
}
