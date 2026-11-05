package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// withSwitch2Game adds to the library a game of the Switch 2 with a file of its own, as a NUP
// gives it.
func withSwitch2Game(t *testing.T, localDB *db.LocalSwitchFilesDB) {
	t.Helper()
	localDB.TitlesMap["0400486024dec"] = &db.SwitchGameFiles{BaseExist: true, MultiContent: true,
		File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Ocarina [0400486024DEC000][v131072].nup", BaseFolder: t.TempDir(), Size: 4},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: "0400486024dec000", Version: 65536, Type: "Base"}},
		Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
}

// The games of the Switch 2 the library has are shown always, and the filter Console shows the
// ones of each console; it is offered when the library has both.
func TestLibraryConsoleFilter(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	withSwitch2Game(t, localDB)
	web.state.set(switchDB, localDB)

	items, _, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	if facets.Switch2 != 1 || facets.Switch1 != len(items)-1 || facets.Switch1 == 0 {
		t.Fatalf("console facets: %+v of %d games", facets, len(items))
	}

	filter := defaultFilter()
	filter.Console = CONSOLE_SWITCH2
	filter.Normalize()
	if items, _, facets := web.getLibraryWithFacets(filter, "en"); len(items) != 1 || !items[0].IsSwitch2() || facets.Switch1 == 0 {
		t.Fatalf("Switch 2 only: %+v, facets %+v", items, facets)
	}
	filter.Console = CONSOLE_SWITCH
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) == 0 || hasSwitch2Item(items) {
		t.Fatalf("Switch only: %+v", items)
	}
	if filter.SecondaryCount() != 1 || filter.query().Get("console") != CONSOLE_SWITCH {
		t.Fatalf("the console filter counts and stays in the links: %d %v", filter.SecondaryCount(), filter.query())
	}

	invalid := &TitleItemFilter{Console: "wii"}
	invalid.Normalize()
	if invalid.Console != "" {
		t.Fatalf("an unknown console is dropped: %+v", invalid)
	}
}

func hasSwitch2Item(items []TitleItem) bool {
	for _, item := range items {
		if item.IsSwitch2() {
			return true
		}
	}
	return false
}

// The panel of filters offers Console after Type, only when the library has games of both, and
// a console chosen shows as a filter that can be removed.
func TestConsoleFilterInThePanel(t *testing.T) {
	set, err := parseTemplates(os.DirFS(".."), "resources/partials/filter.html", "resources/partials/pagination.html")
	if err != nil {
		t.Fatal(err)
	}
	render := func(filter *TitleItemFilter, facets LibraryFacets) string {
		var out strings.Builder
		data := map[string]any{"Filter": filter, "Facets": facets, "Library": true, "Page": "index"}
		if err := set["es"].ExecuteTemplate(&out, "filterPanel", data); err != nil {
			t.Fatal(err)
		}
		if err := set["es"].ExecuteTemplate(&out, "activeFilters", data); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	both := render(defaultFilter(), LibraryFacets{Games: 4, Demos: 1, Switch1: 4, Switch2: 1})
	console, kind := strings.Index(both, "Todas las consolas"), strings.Index(both, "Solo demos")
	if console < 0 || kind < 0 || console < kind || !strings.Contains(both, `console=switch2`) {
		t.Fatalf("Console after Type: %d, %d", console, kind)
	}
	if one := render(defaultFilter(), LibraryFacets{Games: 4, Switch1: 4}); strings.Contains(one, "Todas las consolas") {
		t.Fatal("Console is offered with games of one console only")
	}
	chosen := defaultFilter()
	chosen.Console = CONSOLE_SWITCH2
	if out := render(chosen, LibraryFacets{Switch2: 1}); !strings.Contains(out, `class="active-filter"`) || !strings.Contains(out, "Switch 2<span class=\"bi bi-x\"") {
		t.Fatal("the console chosen shows as an active filter")
	}
}

// Settings tells under "I have a Switch 2", while it is off, how many games of the Switch 2 the
// folders have.
func TestSettingsTellTheSwitch2GamesFound(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)
	if web.switch2Games() != 0 {
		t.Fatal("no game of the Switch 2 yet")
	}
	withSwitch2Game(t, localDB)
	localDB.TitlesMap["04000000000a1"] = &db.SwitchGameFiles{BaseExist: false}
	if web.switch2Games() != 1 {
		t.Fatalf("one game of the Switch 2 with its file: %d", web.switch2Games())
	}

	set, err := parseTemplates(os.DirFS(".."), "resources/layout.html", "resources/partials/*.html", "resources/pages/settings.html")
	if err != nil {
		t.Fatal(err)
	}
	render := func(on bool) string {
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HasSwitch2 = on })
		var out strings.Builder
		if err := set["es"].ExecuteTemplate(&out, "layout", web.settingsPageData()); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := render(false); !strings.Contains(out, "Hay 1 juego de Switch 2 en tus carpetas.") || !strings.Contains(out, "switch2-hint") {
		t.Fatal("the notice is missing while the setting is off")
	}
	if out := render(true); strings.Contains(out, "switch2-hint") {
		t.Fatal("the notice shows while the setting is on")
	}
}

// The check of a folder in the wizard counts its NUP files, so the wizard can turn on "I have a
// Switch 2".
func TestWizardCountsNupFiles(t *testing.T) {
	folder := t.TempDir()
	os.MkdirAll(filepath.Join(folder, "sub"), 0755)
	for _, name := range []string{"a.nsp", "sub/Ocarina.NUP", "b.nup"} {
		os.WriteFile(filepath.Join(folder, name), []byte("x"), 0644)
	}
	if result := checkWizardFolder(folder, "en"); !result.Ok || result.Games != 3 || result.Switch2 != 2 {
		t.Fatalf("three games, two of them NUP: %+v", result)
	}
}

// A game of the Switch 2 the folders have a file of is asked to IGDB for its cover and details
// also while "I have a Switch 2" is off: the Library shows it always.
func TestSwitch2GamesWithAFileAreLookedUp(t *testing.T) {
	web := newTestWeb(t)
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0400486024dec": {Attributes: db.TitleAttributes{Id: "0400486024DEC000", Name: "The Legend of Zelda: Ocarina of Time"}, FromSwitch2List: true},
		"0400000000060": {Attributes: db.TitleAttributes{Id: "0400000000060000", Name: "Comet Rally"}, FromSwitch2List: true},
	}}
	localDB := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}
	withSwitch2Game(t, localDB)
	web.state.set(switchDB, localDB)
	pending := web.switch2TitlesToLookUp(time.Now())
	if len(pending) != 1 || pending["0400486024dec"] != "The Legend of Zelda: Ocarina of Time" {
		t.Fatalf("only the game with a file: %v", pending)
	}
}

// A NUP shows as NUP, its format, also when it holds updates or DLC with the game.
func TestNupFilesShowAsNup(t *testing.T) {
	game := &db.SwitchGameFiles{MultiContent: true, File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Ocarina.NUP"}}}
	if kind := getType(game); kind != "nup" {
		t.Fatalf("a NUP with its update: %q", kind)
	}
	game.File.ExtendedInfo.FileName = "Game.xci"
	if kind := getType(game); kind != "multi-content" {
		t.Fatalf("a cartridge with its update: %q", kind)
	}
}
