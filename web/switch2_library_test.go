package web

import (
	"net/http"
	"net/http/httptest"
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

// The console is chosen beside the views of the Library, with the numbers of the view shown, and
// no longer in the panel of filters. It shows with games of the Switch 2 in the view, the Switch 2
// owned, or a console chosen; a console chosen shows as a filter whose removal asks for both
// consoles, over the one remembered.
func TestConsoleBesideTheViews(t *testing.T) {
	set, err := parseTemplates(os.DirFS(".."), "resources/partials/filter.html", "resources/partials/pagination.html")
	if err != nil {
		t.Fatal(err)
	}
	render := func(name string, data any) string {
		var out strings.Builder
		if err := set["es"].ExecuteTemplate(&out, name, data); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	panel := func(filter *TitleItemFilter, facets LibraryFacets) string {
		data := map[string]any{"Filter": filter, "Facets": facets, "Library": true, "Page": "index", "Count": 0}
		return render("filterPanel", data) + render("activeFilters", data)
	}
	if out := panel(defaultFilter(), LibraryFacets{Games: 4, Demos: 1, Switch1: 4, Switch2: 1}); strings.Contains(out, "console=") {
		t.Fatal("the panel of filters no longer offers the console")
	}
	views := LibraryViews{Current: "mine", Mine: 5}.withConsole(defaultFilter(), LibraryFacets{Switch1: 4, Switch2: 1}, false)
	out := render("libraryViews", views)
	for _, want := range []string{`class="library-views console-views"`, ">Todas<", "console=all", "console=switch2", `>Switch 2<span class="library-view-count">1<`} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing in the views: %s", want, out)
		}
	}
	if out := render("libraryViews", LibraryViews{Current: "mine"}.withConsole(defaultFilter(), LibraryFacets{Switch1: 4}, false)); strings.Contains(out, "console-views") {
		t.Fatal("no console to choose without games of the Switch 2 nor the Switch 2 owned")
	}
	if out := render("libraryViews", LibraryViews{Current: "mine"}.withConsole(defaultFilter(), LibraryFacets{Switch1: 4}, true)); !strings.Contains(out, "console-views") {
		t.Fatal("the console to choose with the Switch 2 owned")
	}
	chosen := defaultFilter()
	chosen.Console = CONSOLE_SWITCH2
	if out := panel(chosen, LibraryFacets{Switch2: 1}); !strings.Contains(out, `class="active-filter"`) || !strings.Contains(out, "Switch 2<span class=\"bi bi-x\"") || !strings.Contains(out, "console=all") {
		t.Fatal("the console chosen shows as an active filter, removed by asking for both")
	}
}

// The console chosen is kept in a cookie for the next pages of the Library, and both consoles
// asked for clear it.
func TestConsoleIsRemembered(t *testing.T) {
	ask := func(query string, cookie string) (*TitleItemFilter, string) {
		request := httptest.NewRequest("GET", "/index.html"+query, nil)
		if cookie != "" {
			request.AddCookie(&http.Cookie{Name: consoleCookie, Value: cookie})
		}
		response := httptest.NewRecorder()
		filter := defaultFilter()
		filter.Console = request.URL.Query().Get("console")
		filter.Normalize()
		rememberConsole(response, request, filter)
		kept := ""
		for _, set := range response.Result().Cookies() {
			if set.Name == consoleCookie {
				kept = set.Value
			}
		}
		return filter, kept
	}
	if filter, kept := ask("?console=switch2", ""); filter.Console != CONSOLE_SWITCH2 || kept != CONSOLE_SWITCH2 {
		t.Fatalf("chosen: %q, kept %q", filter.Console, kept)
	}
	if filter, kept := ask("", CONSOLE_SWITCH2); filter.Console != CONSOLE_SWITCH2 || kept != "" {
		t.Fatalf("remembered: %q, kept %q", filter.Console, kept)
	}
	if filter, kept := ask("?console=all", CONSOLE_SWITCH2); filter.Console != "" || kept != CONSOLE_ALL {
		t.Fatalf("both: %q, kept %q", filter.Console, kept)
	}
	if filter, _ := ask("", CONSOLE_ALL); filter.Console != "" {
		t.Fatalf("both remembered: %q", filter.Console)
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

// The page of a game in a NUP with its update lists the file once, with what else it holds, and
// tells the update one has when no list of updates is known.
func TestNupGamePageListsTheFileOnce(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	withSwitch2Game(t, localDB)
	game := localDB.TitlesMap["0400486024dec"]
	update := db.SwitchFileInfo{ExtendedInfo: game.File.ExtendedInfo, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0400486024dec800", Version: 131072, Type: "Update"}}
	game.Updates[131072] = update
	game.LatestUpdate = 131072
	web.state.set(switchDB, localDB)
	web.embedFS = os.DirFS("..")
	web.HandleTitle()

	detail, ok := web.getTitleDetail("0400486024DEC000", "en")
	if !ok || len(detail.Updates) != 1 || !detail.Updates[0].InGameFile || len(detail.Base.AlsoHolds) != 1 || detail.Base.AlsoHolds[0] != "update v131072" {
		t.Fatalf("detail: %+v, updates %+v", detail.Base, detail.Updates)
	}

	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/title/0400486024DEC000.html", nil))
	page := recorder.Body.String()
	if recorder.Code != http.StatusOK || strings.Count(page, "Ocarina [0400486024DEC000][v131072].nup</div>") != 1 {
		t.Fatalf("the file is listed once: %d, %d times", recorder.Code, strings.Count(page, "Ocarina [0400486024DEC000][v131072].nup</div>"))
	}
	for _, want := range []string{"Also in this file: update v131072", "You have v131072. There is no list of the updates of this game"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page misses %q", want)
		}
	}
}

// A game of the Switch 2 is not told up to date: no list knows its updates. It is left out of the
// games up to date and of their share, as a game without a file is.
func TestSwitch2GamesAreNotUpToDate(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	withSwitch2Game(t, localDB)
	web.state.set(switchDB, localDB)

	items, _, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	for _, item := range items {
		if item.VersionsUnknown != item.IsSwitch2() {
			t.Errorf("%s: versions unknown %v", item.Id, item.VersionsUnknown)
		}
	}
	filter := defaultFilter()
	filter.Status = STATUS_COMPLETE
	filter.Normalize()
	upToDate, _, _ := web.getLibraryWithFacets(filter, "en")
	if hasSwitch2Item(upToDate) || len(upToDate) != facets.Complete {
		t.Fatalf("up to date: %+v, facets %d", upToDate, facets.Complete)
	}
	stats := web.buildStatistics("en")
	if stats.GamesWithFile != len(items)-1 || stats.GamesUpToDate > stats.GamesWithFile {
		t.Fatalf("the share of the games up to date leaves the game of the Switch 2 out: %d of %d (%d games)", stats.GamesUpToDate, stats.GamesWithFile, len(items))
	}
}

// The page of a game in a NUP lists the parts of the file, with what each takes of it, instead
// of the line of what else the file holds; the name of a DLC goes without the name of its game.
func TestNupGamePageListsItsParts(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	withSwitch2Game(t, localDB)
	game := localDB.TitlesMap["0400486024dec"]
	game.File.Metadata.StoredSize = 7 << 30
	game.Updates[131072] = db.SwitchFileInfo{ExtendedInfo: game.File.ExtendedInfo,
		Metadata: &switchfs.ContentMetaAttributes{TitleId: "0400486024dec800", Version: 131072, Type: "Update", StoredSize: 300 << 20}}
	game.Dlc["0400486024ded001"] = db.SwitchFileInfo{ExtendedInfo: game.File.ExtendedInfo,
		Metadata: &switchfs.ContentMetaAttributes{TitleId: "0400486024ded001", Type: "DLC", StoredSize: 700 << 20}}
	game.LatestUpdate = 131072
	switchDB.TitlesMap["0400486024dec"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0400486024DEC000", Name: "Made-up Quest"},
		Dlc: map[string]db.TitleAttributes{"0400486024ded001": {Id: "0400486024DED001", Name: "Made-up Quest: Island Pack"}}}
	web.state.set(switchDB, localDB)
	web.embedFS = os.DirFS("..")
	web.HandleTitle()

	detail, ok := web.getTitleDetail("0400486024DEC000", "en")
	if !ok || detail.Base.AlsoHolds != nil || len(detail.Base.Parts) != 3 {
		t.Fatalf("parts: %+v, also %v", detail.Base.Parts, detail.Base.AlsoHolds)
	}
	want := []TitleFilePart{{Kind: PART_GAME, Detail: "v65536", Size: 7 << 30}, {Kind: PART_UPDATE, Detail: "v131072", Size: 300 << 20},
		{Kind: PART_DLC, Detail: "Island Pack", Size: 700 << 20}}
	for i, part := range want {
		if detail.Base.Parts[i] != part {
			t.Fatalf("part %d: %+v, want %+v", i, detail.Base.Parts[i], part)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/title/0400486024DEC000.html", nil)
	request.Header.Set("Accept-Language", "es")
	web.router.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if !strings.Contains(body, "Contenido del archivo") || !strings.Contains(body, "300,0 MB") || !strings.Contains(body, "<small>Island Pack</small>") ||
		strings.Contains(body, "También en este archivo") {
		t.Fatalf("the page lists the parts:\n%s", body)
	}

	// a NUP read before the sizes were told keeps the line of what else it holds
	game.File.Metadata.StoredSize = 0
	if detail, _ := web.getTitleDetail("0400486024DEC000", "en"); detail.Base.Parts != nil || len(detail.Base.AlsoHolds) == 0 {
		t.Fatalf("no sizes: %+v %v", detail.Base.Parts, detail.Base.AlsoHolds)
	}
}

func TestDlcShortName(t *testing.T) {
	for name, want := range map[string]string{
		"Donkey Kong Bananza: DK Island & Emerald Rush": "DK Island & Emerald Rush",
		"Donkey Kong Bananza - Extra":                   "Extra",
		"Donkey Kong Bananzas Pack":                     "Donkey Kong Bananzas Pack",
		"Donkey Kong Bananza":                           "Donkey Kong Bananza",
		"Season Pass":                                   "Season Pass",
	} {
		if got := dlcShortName(name, "Donkey Kong Bananza"); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}
