package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

func TestPackSourcesTakeTheLatestUpdateAndTheDlc(t *testing.T) {
	file := func(name string) db.SwitchFileInfo {
		return db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: name, BaseFolder: "games"}}
	}
	local := &db.SwitchGameFiles{
		BaseExist:    true,
		File:         file("Game.nsp"),
		Updates:      map[int]db.SwitchFileInfo{65536: file("Update v1.nsp"), 131072: file("Update v2.nsz")},
		LatestUpdate: 131072,
		Dlc:          map[string]db.SwitchFileInfo{"0100000000011001": file("DLC.nsp")},
	}
	sources := packSources(local)
	want := []string{"Game.nsp", "Update v2.nsz", "DLC.nsp"}
	if len(sources) != len(want) {
		t.Fatalf("sources: %v", sources)
	}
	for i, name := range want {
		if sources[i] != filepath.Join("games", name) {
			t.Fatalf("sources: %v", sources)
		}
	}
	if name := packName("Game: The Return", "0100000000010000", local, true); name != "Game The Return [0100000000010000][v131072][+1 DLC] (pack).nsz" {
		t.Fatalf("name: %s", name)
	}

	// one file, or a game split in a folder, has no pack
	if packSources(&db.SwitchGameFiles{BaseExist: true, File: file("Game.nsp")}) != nil {
		t.Fatal("a game alone")
	}
	local.Dlc["0100000000011001"] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "DLC", BaseFolder: "games", IsDir: true}}
	if packSources(local) != nil {
		t.Fatal("a split folder")
	}
}

func TestMakePackFromTheTitlePage(t *testing.T) {
	web, path, _ := compressWebWithLibrary(t)
	web.HandleTitle()
	folder := filepath.Dir(path)
	update := filepath.Join(folder, "Game [0100000000010800][v65536].nsp")
	dlc := filepath.Join(folder, "Game DLC [0100000000011001][v0].nsp")
	if _, err := testnsp.WriteNspOf(update, "the update of the test game "); err != nil {
		t.Fatal(err)
	}
	if _, err := testnsp.WriteNspOf(dlc, "the dlc of the test game "); err != nil {
		t.Fatal(err)
	}
	_, library := web.state.get()
	local := library.TitlesMap["0100000000010"]
	local.Updates[65536] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(update), BaseFolder: folder}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010800", Version: 65536}}
	local.LatestUpdate = 65536
	local.Dlc["0100000000011001"] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(dlc), BaseFolder: folder}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000011001"}}
	web.invalidateDerived()

	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/title/0100000000010000.html", nil))
	if !strings.Contains(page.Body.String(), `data-pack="0100000000010000"`) {
		t.Fatalf("the game page offers the pack: %d", page.Code)
	}
	if response := postForm(web, "/title/pack", url.Values{"id": {"nope"}}); response.Code != http.StatusBadRequest {
		t.Fatalf("an invalid ID: %d", response.Code)
	}
	if response := postForm(web, "/title/pack", url.Values{"id": {"0100000000010000"}}); response.Code != http.StatusBadRequest {
		t.Fatalf("keeping or deleting the files is always chosen: %d", response.Code)
	}
	if response := postForm(web, "/title/pack", url.Values{"id": {"0100000000010000"}, "delete_originals": {"true"}}); response.Code != http.StatusAccepted {
		t.Fatalf("pack: %d %s", response.Code, response.Body.String())
	}
	task := waitForTask(t, web, TASK_PACK)
	if task.Status != TASK_SUCCESS || task.Files != 1 {
		t.Fatalf("one pack made: %+v", task)
	}
	matches, _ := filepath.Glob(filepath.Join(folder, "*(pack).nsp"))
	if len(matches) != 1 || !strings.Contains(matches[0], "[0100000000010000][v65536][+1 DLC]") {
		t.Fatalf("the pack: %v", matches)
	}
	for _, original := range []string{path, update, dlc} {
		if _, err := os.Stat(original); !os.IsNotExist(err) {
			t.Fatalf("%s is deleted once the pack is checked", original)
		}
	}
	// the pack holds the three NCAs, each one matching its content ID
	if err := switchfs.VerifyCompressed(t.Context(), matches[0], nil, nil); err != nil {
		t.Fatal(err)
	}
	if hashes, err := switchfs.HashNcas(t.Context(), matches[0], nil); err != nil || len(hashes) != 3 {
		t.Fatalf("three NCAs: %d %v", len(hashes), err)
	}
}
