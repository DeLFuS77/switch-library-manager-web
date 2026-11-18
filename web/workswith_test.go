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
