package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Which installers take a file and whether emulators open it as it is, by how it is stored, as
// NXPress tells it: the extra compression goes before the game merged with its update, which goes
// before the compression; what is not known is not guessed.
func TestWorksWithEachFormat(t *testing.T) {
	for _, test := range []struct {
		storage               string
		merged                bool
		installers, emulators string
		installersOk, emusOk  bool
		help                  string
	}{
		{switchfs.StoragePlain, false, installersAll, emulatorsAsItIs, true, true, worksPlainHelp},
		{switchfs.StorageSolid, false, installersAll, emulatorsDecompress, true, false, worksSolidHelp},
		{switchfs.StorageBlocks, false, installersButAwoo, emulatorsAsItIs, false, true, worksBlocksHelp},
		{switchfs.StorageSparse, false, installersOnlyNXDock, emulatorsNo, false, false, worksExtraHelp},
		{switchfs.StorageArchive, false, installersOnlyNXDock, emulatorsNo, false, false, worksExtraHelp},
		{switchfs.StoragePlain, true, installersSigpatches, emulatorsAsItIs, false, true, worksMergedHelp},
		{switchfs.StorageBlocks, true, installersSigpatches, emulatorsAsItIs, false, true, worksMergedHelp},
		{switchfs.StorageSolid, true, installersSigpatches, emulatorsDecompress, false, false, worksMergedSolidHelp},
		{switchfs.StorageArchive, true, installersOnlyNXDock, emulatorsNo, false, false, worksExtraHelp},
	} {
		works := worksWith(test.storage, test.merged)
		if works.Unknown || works.Installers != test.installers || works.Emulators != test.emulators || works.InstallersOk != test.installersOk ||
			works.EmulatorsOk != test.emusOk || works.Help != test.help {
			t.Errorf("%s, merged %v: %+v", test.storage, test.merged, works)
		}
	}
	for _, merged := range []bool{false, true} {
		if works := worksWith("", merged); !works.Unknown || works.Installers != worksUnknown || works.Emulators != "" || works.Help != worksUnknownHelp {
			t.Errorf("not known, merged %v: %+v", merged, works)
		}
	}
	// a NUP tells what Eglantine and Lichii make of it on its own
	if works := fileWorks(db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Game.nup"}, Metadata: &switchfs.ContentMetaAttributes{}}, false); works != nil {
		t.Fatalf("a NUP: %+v", works)
	}
}

// A game opens in emulators as it is when every file the library keeps of it does: the game, its
// newest update and its DLC; an older update does not count, and a file not known leaves the game
// out of both choices, unless another file tells it does not open.
func TestGameEmulators(t *testing.T) {
	file := func(name string, storage string) db.SwitchFileInfo {
		return db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: name}, Metadata: &switchfs.ContentMetaAttributes{Storage: storage}}
	}
	game := func(base, update, older, dlc string) *db.SwitchGameFiles {
		return &db.SwitchGameFiles{BaseExist: true, File: file("game.nsz", base), LatestUpdate: 131072,
			Updates: map[int]db.SwitchFileInfo{131072: file("update.nsz", update), 65536: file("old.nsz", older)},
			Dlc:     map[string]db.SwitchFileInfo{"0100000000011001": file("dlc.nsz", dlc)}}
	}
	for _, test := range []struct {
		local *db.SwitchGameFiles
		want  string
	}{
		{game(switchfs.StorageBlocks, switchfs.StoragePlain, switchfs.StorageSolid, switchfs.StorageBlocks), EMULATORS_AS_IS},
		{game(switchfs.StorageSolid, switchfs.StoragePlain, switchfs.StoragePlain, switchfs.StoragePlain), EMULATORS_NOT_AS_IS},
		{game(switchfs.StoragePlain, switchfs.StorageArchive, switchfs.StoragePlain, switchfs.StoragePlain), EMULATORS_NOT_AS_IS},
		{game(switchfs.StoragePlain, switchfs.StoragePlain, switchfs.StoragePlain, switchfs.StorageSparse), EMULATORS_NOT_AS_IS},
		{game(switchfs.StoragePlain, "", switchfs.StoragePlain, switchfs.StoragePlain), ""},
		{game("", switchfs.StorageSolid, switchfs.StoragePlain, switchfs.StoragePlain), EMULATORS_NOT_AS_IS},
		{&db.SwitchGameFiles{File: file("game.nsp", switchfs.StoragePlain)}, ""},
		{nil, ""},
	} {
		if got := gameEmulators(test.local); got != test.want {
			t.Errorf("%+v: %q, want %q", test.local, got, test.want)
		}
	}

	// a NUP opens as it is when Eglantine and Lichii read it
	nup := &db.SwitchGameFiles{BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Game.nup"}, Metadata: &switchfs.ContentMetaAttributes{}}}
	if got := gameEmulators(nup); got != "" {
		t.Fatalf("a NUP not read for them: %q", got)
	}
	nup.File.Metadata.Emulators = &switchfs.NupEmulators{}
	if got := gameEmulators(nup); got != EMULATORS_AS_IS {
		t.Fatalf("a NUP they read: %q", got)
	}
	nup.File.Metadata.Emulators = &switchfs.NupEmulators{Refused: []switchfs.NupEmulatorIssue{{Text: "its folders go more than 128 deep"}}}
	if got := gameEmulators(nup); got != EMULATORS_NOT_AS_IS {
		t.Fatalf("a NUP they refuse: %q", got)
	}
}

// The page of a game tells under each file which installers take it and whether emulators open
// it as it is, a dot in green or orange, with the case on hover; a file the library did not note
// is read for the page, and one that cannot be read says it is not known.
func TestGamePageTellsWhatEachFileWorksWith(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	game := localDB.TitlesMap["0100000000010"]
	update := game.Updates[65536]
	update.Metadata.Storage = switchfs.StorageSolid
	game.Updates[65536] = update
	dlc := game.Dlc["0100000000011001"]
	dlc.Metadata.Storage = switchfs.StorageArchive
	game.Dlc["0100000000011001"] = dlc
	web.state.set(switchDB, localDB)
	web.embedFS = os.DirFS("..")
	web.HandleTitle()
	page := func(lang string) string {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/title/0100000000010000.html", nil)
		request.Header.Set("Accept-Language", lang)
		web.router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("the page: %d", recorder.Code)
		}
		return recorder.Body.String()
	}

	// the file of the game is not a package: not known
	body := page("en")
	for _, text := range []string{
		`<div class="works-with is-unknown" data-hover-help>`, "Installers and emulators: unknown", worksUnknownHelp,
		`<span class="works-part is-ok"><span class="works-dot" aria-hidden="true"></span>Installers: all</span> · <span class="works-part is-limit"><span class="works-dot" aria-hidden="true"></span>Emulators: decompress it first</span>`,
		worksSolidHelp,
		`<span class="works-part is-limit"><span class="works-dot" aria-hidden="true"></span>Installers: only NXDock</span> · <span class="works-part is-limit"><span class="works-dot" aria-hidden="true"></span>Emulators: no</span>`,
		worksExtraHelp,
	} {
		if !strings.Contains(body, text) {
			t.Errorf("the page lacks %q", text)
		}
	}

	// a file the library did not note, read for the page: not compressed
	if _, err := testnsp.WriteNsp(filepath.Join(game.File.ExtendedInfo.BaseFolder, game.File.ExtendedInfo.FileName)); err != nil {
		t.Fatal(err)
	}
	body = page("es")
	if strings.Contains(body, "is-unknown") || !strings.Contains(body, "Instaladores: todos</span> · <span class=\"works-part is-ok\"><span class=\"works-dot\" aria-hidden=\"true\"></span>Emuladores: tal cual") ||
		!strings.Contains(body, "Hecho con la compresión extra de NXPress.") || !strings.Contains(body, "Emuladores: descomprímelo antes") {
		t.Fatalf("read for the page, in Spanish:\n%s", body)
	}
	if game.File.Metadata.Storage != "" {
		t.Fatal("what the page reads is not kept in the library")
	}

	// the game merged with its update
	game.File.Metadata.Version = 131072
	game.File.Metadata.Storage = switchfs.StorageBlocks
	if body := page("en"); !strings.Contains(body, "Installers: NXDock or DBI, with sigpatches") || !strings.Contains(body, worksMergedHelp) {
		t.Fatal("merged with its update")
	}

	// a NUP keeps its own lines
	nup := db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Game.nup"}, Metadata: &switchfs.ContentMetaAttributes{Storage: switchfs.StoragePlain}}
	if works := fileWorks(nup, false); works != nil {
		t.Fatalf("a NUP: %+v", works)
	}
}

// The Library is filtered by emulators, in Filters › The file: the games whose files all open as
// they are, or those with one to decompress first, each counted; the choice is kept in the
// address, in smart collections and in the Catalogue, and counts in the number of games.
func TestEmulatorsFilter(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	switchDB, localDB := testDatabases(t)
	known := localDB.TitlesMap["0100000000010"]
	known.File.Metadata.Storage = switchfs.StorageBlocks
	update := known.Updates[65536]
	update.Metadata.Storage = switchfs.StoragePlain
	dlc := known.Dlc["0100000000011001"]
	dlc.Metadata.Storage = switchfs.StorageSolid
	split := localDB.TitlesMap["0100000000030"]
	split.File.Metadata.Storage = switchfs.StoragePlain
	web.state.set(switchDB, localDB)

	items, p, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	if facets.EmulatorsAsIs != 1 || facets.EmulatorsNotAsIs != 1 || len(items) != 2 || p.NumItems != 2 {
		t.Fatalf("counted: %d as is, %d not, %d games", facets.EmulatorsAsIs, facets.EmulatorsNotAsIs, p.NumItems)
	}
	filter := defaultFilter()
	filter.Emulators = " NOT_AS_IS "
	filter.Normalize()
	items, p, facets = web.getLibraryWithFacets(filter, "en")
	if filter.Emulators != EMULATORS_NOT_AS_IS || len(items) != 1 || items[0].Name != "Known Game" || p.NumItems != 1 || facets.EmulatorsAsIs != 1 {
		t.Fatalf("the games to decompress: %+v %d", items, p.NumItems)
	}
	if !filter.Active() || filter.SecondaryCount() != 1 || filter.query().Get("emulators") != EMULATORS_NOT_AS_IS {
		t.Fatalf("the filter counted and kept in the address: %+v", filter.query())
	}
	filter.Emulators = EMULATORS_AS_IS
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) != 1 || items[0].Id != "0100000000030000" {
		t.Fatalf("the games that open as they are: %+v", items)
	}
	for _, value := range []string{"yes", "as is", "<b>"} {
		other := defaultFilter()
		other.Emulators = value
		if other.Normalize(); other.Emulators != "" {
			t.Fatalf("%q kept", value)
		}
	}
	if query := cleanSmartQuery("emulators=as_is&per_page=48"); !strings.Contains(query, "emulators=as_is") || strings.Contains(query, "per_page") {
		t.Fatalf("a smart collection keeps it: %q", query)
	}
	catalogue := defaultFilter()
	catalogue.Emulators = EMULATORS_NOT_AS_IS
	if data := web.catalogue(catalogue, "en"); len(data.Games) != 1 || data.Games[0].Name != "Known Game" || !strings.Contains(data.BackUrl, "emulators=not_as_is") {
		t.Fatalf("the Catalogue: %+v %s", data.Games, data.BackUrl)
	}

	web.HandleIndex()
	web.HandleCatalogue()
	get := func(url string) string {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, url, nil)
		request.Header.Set("Accept-Language", "es")
		web.router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d", url, recorder.Code)
		}
		return recorder.Body.String()
	}
	body := get("/index.html?missing=0&emulators=not_as_is")
	for _, text := range []string{"Emuladores", "Los abren tal cual", "No los abren tal cual", `emulators=as_is`, "active-filter", "Emuladores: descomprímelo antes"} {
		if !strings.Contains(body, text) {
			t.Errorf("the Library lacks %q", text)
		}
	}
	if body := get("/catalogue.html?missing=0&emulators=as_is"); !strings.Contains(body, "Emuladores: tal cual") {
		t.Error("the Catalogue tells the filter")
	}
}
