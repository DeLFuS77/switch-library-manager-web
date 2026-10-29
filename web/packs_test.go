package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
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
	sources := packSources(local, nil)
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
	if packSources(&db.SwitchGameFiles{BaseExist: true, File: file("Game.nsp")}, nil) != nil {
		t.Fatal("a game alone")
	}
	local.Dlc["0100000000011001"] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "DLC", BaseFolder: "games", IsDir: true}}
	if packSources(local, nil) != nil {
		t.Fatal("a split folder")
	}
}

// A game kept both as NSP and as the NSZ the app made of them is packed from the NSZ: the
// library keeps the NSP it found first, and the pack came out as a 20 GB NSP.
func TestPackTakesTheCompressedCopies(t *testing.T) {
	info := func(name string) db.ExtendedFileInfo {
		return db.ExtendedFileInfo{FileName: name, BaseFolder: "games"}
	}
	local := &db.SwitchGameFiles{
		BaseExist:    true,
		File:         db.SwitchFileInfo{ExtendedInfo: info("Game [v0].nsp")},
		Updates:      map[int]db.SwitchFileInfo{131072: {ExtendedInfo: info("Game [v131072].nsp")}},
		LatestUpdate: 131072,
		Dlc:          map[string]db.SwitchFileInfo{"0100000000011001": {ExtendedInfo: info("DLC [v0].nsp")}},
	}
	skipped := map[db.ExtendedFileInfo]db.SkippedFile{
		info("Game [v0].nsz"):       {ReasonCode: db.REASON_DUPLICATE},
		info("Game [v131072].nsz"):  {ReasonCode: db.REASON_DUPLICATE},
		info("DLC [v0].nsz"):        {ReasonCode: db.REASON_DUPLICATE},
		info("Game [v65536].nsz"):   {ReasonCode: db.REASON_OLD_UPDATE},
		info("Other game [v0].nsz"): {ReasonCode: db.REASON_DUPLICATE},
	}
	sources := packSources(local, skipped)
	want := []string{"Game [v0].nsz", "Game [v131072].nsz", "DLC [v0].nsz"}
	if len(sources) != len(want) {
		t.Fatalf("sources: %v", sources)
	}
	for i, name := range want {
		if sources[i] != filepath.Join("games", name) {
			t.Fatalf("sources: %v", sources)
		}
	}
	// only a duplicate of the same name: an old update or another game is never taken
	delete(skipped, info("DLC [v0].nsz"))
	skipped[info("DLC [v0].nsz")] = db.SkippedFile{ReasonCode: db.REASON_OLD_UPDATE}
	if sources := packSources(local, skipped); sources[2] != filepath.Join("games", "DLC [v0].nsp") {
		t.Fatalf("the DLC without a compressed copy: %v", sources)
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

// With "Delete the empty folders" of Organize on, the folders of the update and the DLC a pack
// leaves empty go too, never the library folder; with it off they stay.
func TestPackDeletesTheFoldersLeftEmpty(t *testing.T) {
	for _, deleteEmpty := range []bool{true, false} {
		web, path, _ := compressWebWithLibrary(t)
		folder := filepath.Dir(path)
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			s.Folder = folder
			s.OrganizeOptions.DeleteEmptyFolders = deleteEmpty
		})
		update := filepath.Join(folder, "Updates", "Game [0100000000010800][v65536].nsp")
		dlc := filepath.Join(folder, "DLC", "Game DLC [0100000000011001][v0].nsp")
		for file, content := range map[string]string{update: "the update of the test game ", dlc: "the dlc of the test game "} {
			os.MkdirAll(filepath.Dir(file), 0o755)
			if _, err := testnsp.WriteNspOf(file, content); err != nil {
				t.Fatal(err)
			}
		}
		_, library := web.state.get()
		local := library.TitlesMap["0100000000010"]
		local.Updates[65536] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(update), BaseFolder: filepath.Dir(update)}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010800", Version: 65536}}
		local.LatestUpdate = 65536
		local.Dlc["0100000000011001"] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(dlc), BaseFolder: filepath.Dir(dlc)}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000011001"}}
		web.invalidateDerived()

		if !web.startPacks([]string{path}, nil, true, TRIGGER_MANUAL) {
			t.Fatal("the pack starts")
		}
		if task := waitForTask(t, web, TASK_PACK); task.Status != TASK_SUCCESS {
			t.Fatalf("pack: %+v", task)
		}
		for _, sub := range []string{"Updates", "DLC"} {
			_, err := os.Stat(filepath.Join(folder, sub))
			if deleteEmpty && !os.IsNotExist(err) {
				t.Errorf("the empty folder %s goes", sub)
			}
			if !deleteEmpty && err != nil {
				t.Errorf("the folder %s stays when Organize keeps empty folders", sub)
			}
		}
		if _, err := os.Stat(folder); err != nil {
			t.Fatal("the library folder always stays")
		}
	}
}

// a DLC that needs a newer version of its game than the newest update of the library is told
// in the list to pack
func TestDlcNeedsNewer(t *testing.T) {
	dlc := func(required int) db.SwitchFileInfo {
		return db.SwitchFileInfo{Metadata: &switchfs.ContentMetaAttributes{Type: "DLC", RequiredTitleVersion: required}}
	}
	local := &db.SwitchGameFiles{LatestUpdate: 131072, Dlc: map[string]db.SwitchFileInfo{"a": dlc(65536), "b": dlc(131072)}}
	if need := dlcNeedsNewer(local); need != 0 {
		t.Errorf("every DLC has its update: %d", need)
	}
	local.Dlc["c"] = dlc(262144)
	local.Dlc["d"] = dlc(196608)
	if need := dlcNeedsNewer(local); need != 262144 {
		t.Errorf("the newest version a DLC needs: %d", need)
	}
}

// A pack queued behind a compression that deletes the NSP of the game waits for the scan that
// follows, and is made of the NSZ: not refused for a file gone, nor made of the files of before.
func TestPackQueuedAfterACompressionTakesTheNsz(t *testing.T) {
	web, path, _ := compressWebWithLibrary(t)
	folder := filepath.Dir(path)
	update := filepath.Join(folder, "Game [0100000000010800][v65536].nsp")
	if _, err := testnsp.WriteNspOf(update, "the update of the test game "); err != nil {
		t.Fatal(err)
	}
	_, library := web.state.get()
	local := library.TitlesMap["0100000000010"]
	local.Updates[65536] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(update), BaseFolder: folder}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010800", Version: 65536}}
	local.LatestUpdate = 65536

	// the compression deleted the NSP, and its scan runs
	nsz := switchfs.CompressedPath(path)
	if _, err := web.compressFile(t.Context(), path, switchfs.LevelFast, true, func(int64) func(int64, int64) { return func(int64, int64) {} }); err != nil {
		t.Fatal(err)
	}
	if !web.state.startSync() {
		t.Fatal("no scan")
	}
	web.scanStarted.Store(time.Now().UnixNano())
	web.queue().add(QueuedTask{Kind: TASK_PACK, Paths: []string{path}, Delete: false})
	web.runQueue()
	if len(web.queue().list()) != 1 || web.compressionRunning() {
		t.Fatal("the pack waits for the scan")
	}
	// the scan found the NSZ
	local.File.ExtendedInfo = db.ExtendedFileInfo{FileName: filepath.Base(nsz), BaseFolder: folder}
	web.state.set(nil, library)
	web.state.endSync()
	web.afterScan()
	deadline := time.Now().Add(20 * time.Second)
	for len(web.queue().list()) > 0 || web.compressionRunning() {
		if time.Now().After(deadline) {
			t.Fatal("the pack did not run after the scan")
		}
		time.Sleep(20 * time.Millisecond)
	}
	task := waitForTask(t, web, TASK_PACK)
	if task.Status != TASK_SUCCESS || task.Files != 1 {
		t.Fatalf("the pack is made of the NSZ: %+v %+v", task, task.Warnings)
	}
}

// A pack asked for a game compressed since, its NSP kept, is made of what the library has as
// the game now.
func TestPackFindsTheGameCompressedSince(t *testing.T) {
	web, path, _ := compressWebWithLibrary(t)
	_, library := web.state.get()
	local := library.TitlesMap["0100000000010"]
	local.File.ExtendedInfo.FileName = filepath.Base(switchfs.CompressedPath(path))
	if found, id := web.packGame(path); found != local || id != "0100000000010000" {
		t.Fatalf("the game of the NSP is the one of its NSZ: %v %s", found, id)
	}
}
