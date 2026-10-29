package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// splitWebWithSuperNsp is a library with a SuperNSP: a game, its update and a DLC in one file.
func splitWebWithSuperNsp(t *testing.T) (*Web, string) {
	t.Helper()
	web, path, _ := compressWebWithLibrary(t)
	folder := filepath.Dir(path)
	super := filepath.Join(folder, "Super Game [0100000000020000].nsp")
	nsp, err := testnsp.WriteMultiTitleNsp(super, []testnsp.Title{
		{Id: 0x0100000000020000, Type: switchfs.ContentMetaType_Application, Content: "the super game "},
		{Id: 0x0100000000020800, Type: switchfs.ContentMetaType_Patch, Version: 65536, Content: "its update "},
		{Id: 0x0100000000021001, Type: switchfs.ContentMetaType_AddOnContent, Content: "its dlc "},
	})
	if err != nil {
		t.Fatal(err)
	}
	info := db.ExtendedFileInfo{FileName: filepath.Base(super), BaseFolder: folder, Size: int64(len(nsp))}
	_, library := web.state.get()
	library.TitlesMap["0100000000020"] = &db.SwitchGameFiles{
		BaseExist:    true,
		MultiContent: true,
		File:         db.SwitchFileInfo{ExtendedInfo: info, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000020000"}},
		Updates:      map[int]db.SwitchFileInfo{65536: {ExtendedInfo: info, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000020800", Version: 65536}}},
		LatestUpdate: 65536,
		Dlc:          map[string]db.SwitchFileInfo{"0100000000021001": {ExtendedInfo: info, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000021001"}}},
	}
	web.invalidateDerived()
	return web, super
}

func TestSplitASuperNspFromCompress(t *testing.T) {
	web, super := splitWebWithSuperNsp(t)
	folder := filepath.Dir(super)

	candidates := web.splitCandidates()
	if len(candidates) != 1 || candidates[0].Path != super || candidates[0].Titles != 3 || candidates[0].Name != "Super Game" {
		t.Fatalf("the SuperNSP alone: %+v", candidates)
	}
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/compress.html", nil))
	if !strings.Contains(page.Body.String(), `id="splitForm"`) || !strings.Contains(page.Body.String(), `/compress/split-list`) {
		t.Fatalf("Compress offers the split: %d", page.Code)
	}
	list := httptest.NewRecorder()
	web.router.ServeHTTP(list, httptest.NewRequest("GET", "/compress/split-list", nil))
	if body := list.Body.String(); !strings.Contains(body, `id="split0"`) || !strings.Contains(body, "3 titles") {
		t.Fatalf("the list: %s", body)
	}

	if response := postForm(web, "/split/start", url.Values{"path": {"elsewhere.nsp"}}); response.Code != http.StatusBadRequest {
		t.Fatalf("only files of the list: %d", response.Code)
	}
	if response := postForm(web, "/split/start", url.Values{"rest": {"all"}, "delete_originals": {"true"}}); response.Code != http.StatusAccepted {
		t.Fatalf("split: %d %s", response.Code, response.Body.String())
	}
	task := waitForTask(t, web, TASK_SPLIT)
	if task.Status != TASK_SUCCESS || task.Files != 1 {
		t.Fatalf("one file split: %+v", task)
	}
	if _, err := os.Stat(super); !os.IsNotExist(err) {
		t.Fatal("the original is deleted once its parts are checked")
	}
	for id, version := range map[string]string{"0100000000020000": "v0", "0100000000020800": "v65536", "0100000000021001": "v0"} {
		part := filepath.Join(folder, "Super Game ["+id+"]["+version+"].nsp")
		contents, err := switchfs.ReadNspMetadata(part)
		if err != nil || len(contents) != 1 || contents[id] == nil {
			t.Fatalf("%s: %v %v", part, contents, err)
		}
		if _, err := switchfs.VerifyFile(context.Background(), part, nil); err != nil {
			t.Fatalf("%s: %v", part, err)
		}
		if hashes, err := switchfs.HashNcas(context.Background(), part, nil); err != nil || len(hashes) != 4 {
			t.Fatalf("the Meta NCA, the program, the ticket and the certificate of %s: %d %v", id, len(hashes), err)
		}
	}
	if leftovers, _ := filepath.Glob(filepath.Join(folder, ".*.tmp")); len(leftovers) != 0 {
		t.Fatalf("nothing half written: %v", leftovers)
	}
}

// A split stopped in the middle goes on: the parts written whole are kept as they are.
func TestSplitKeepsThePartsAlreadyWritten(t *testing.T) {
	web, super := splitWebWithSuperNsp(t)
	report := func(int64) func(int64, int64) { return func(int64, int64) {} }
	if err := web.splitFile(context.Background(), super, "Super Game", false, report); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(super); err != nil {
		t.Fatal("the original is kept when asked")
	}
	update := filepath.Join(filepath.Dir(super), "Super Game [0100000000020800][v65536].nsp")
	before, _ := os.Stat(update)
	os.Remove(filepath.Join(filepath.Dir(super), "Super Game [0100000000021001][v0].nsp"))
	if err := web.splitFile(context.Background(), super, "Super Game", true, report); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(update); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the update was written again")
	}
	if _, err := os.Stat(super); !os.IsNotExist(err) {
		t.Fatal("deleted once every part is there")
	}

	// a different file with the name of a part is never replaced
	web2, super2 := splitWebWithSuperNsp(t)
	os.WriteFile(filepath.Join(filepath.Dir(super2), "Super Game [0100000000020000][v0].nsp"), []byte("something else"), 0o644)
	if err := web2.splitFile(context.Background(), super2, "Super Game", true, report); err == nil || compressNote(TASK_SPLIT, err) != NOTE_SPLIT_EXISTS {
		t.Fatalf("a file in the way: %v", err)
	}
	if _, err := os.Stat(super2); err != nil {
		t.Fatal("the original is kept")
	}
}

// A part already there with the size it would have counts only once it is found sound: one
// damaged since keeps the original; one the app did not check yet is checked first.
func TestSplitChecksThePartsFoundWritten(t *testing.T) {
	web, super := splitWebWithSuperNsp(t)
	report := func(int64) func(int64, int64) { return func(int64, int64) {} }
	if err := web.splitFile(context.Background(), super, "Super Game", false, report); err != nil {
		t.Fatal(err)
	}
	update := filepath.Join(filepath.Dir(super), "Super Game [0100000000020800][v65536].nsp")
	data, _ := os.ReadFile(update)
	data[len(data)/2] ^= 0xFF
	os.WriteFile(update, data, 0o644)
	if err := web.splitFile(context.Background(), super, "Super Game", true, report); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(super); err != nil {
		t.Fatal("a damaged part keeps the original")
	}

	// sound, but never checked by the app: checked now, and the original goes
	data[len(data)/2] ^= 0xFF
	os.WriteFile(update, data, 0o644)
	web.verifications().follow([]process.Operation{{Kind: process.OP_DELETE, From: update}})
	if err := web.splitFile(context.Background(), super, "Super Game", true, report); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(super); !os.IsNotExist(err) {
		t.Fatal("deleted once every part is found sound")
	}
}

func TestMakePacksInBulk(t *testing.T) {
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
	web.invalidateDerived()

	candidates := web.packCandidates()
	if len(candidates) != 1 || candidates[0].Path != path || candidates[0].Files != 2 {
		t.Fatalf("the game and its update: %+v", candidates)
	}
	list := httptest.NewRecorder()
	web.router.ServeHTTP(list, httptest.NewRequest("GET", "/compress/pack-list", nil))
	if body := list.Body.String(); !strings.Contains(body, `id="pack0"`) || !strings.Contains(body, "2 files") {
		t.Fatalf("the list: %s", body)
	}
	if response := postForm(web, "/packs/start", url.Values{}); response.Code != http.StatusBadRequest {
		t.Fatalf("nothing chosen: %d", response.Code)
	}
	if response := postForm(web, "/packs/start", url.Values{"path": {path}}); response.Code != http.StatusAccepted {
		t.Fatalf("packs: %d %s", response.Code, response.Body.String())
	}
	task := waitForTask(t, web, TASK_PACK)
	if task.Status != TASK_SUCCESS || task.Files != 1 {
		t.Fatalf("one pack made: %+v", task)
	}
	matches, _ := filepath.Glob(filepath.Join(folder, "*(pack).nsp"))
	if len(matches) != 1 || !strings.Contains(matches[0], "[0100000000010000][v65536]") {
		t.Fatalf("the pack: %v", matches)
	}
	for _, original := range []string{path, update} {
		if _, err := os.Stat(original); err != nil {
			t.Fatalf("%s is kept unless asked", original)
		}
	}
	// a pack made is not offered again
	library.Skipped[db.ExtendedFileInfo{FileName: filepath.Base(matches[0]), BaseFolder: folder}] = db.SkippedFile{}
	web.invalidateDerived()
	if candidates := web.packCandidates(); len(candidates) != 0 {
		t.Fatalf("already packed: %+v", candidates)
	}
}

// A game card with several games is listed to split, one with a game and its update is not,
// and each game of a card is named after itself.
func TestSplitCandidatesWithGameCards(t *testing.T) {
	web := newTestWeb(t)
	folder := t.TempDir()
	collection := db.ExtendedFileInfo{FileName: "Collection [0100000000030000].xcz", BaseFolder: folder, Size: 900}
	single := db.ExtendedFileInfo{FileName: "Single [0100000000050000].xci", BaseFolder: folder, Size: 500}
	game := func(id string, file db.ExtendedFileInfo) *db.SwitchGameFiles {
		return &db.SwitchGameFiles{BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: file, Metadata: &switchfs.ContentMetaAttributes{TitleId: id}},
			Updates: map[int]db.SwitchFileInfo{65536: {ExtendedInfo: file, Metadata: &switchfs.ContentMetaAttributes{TitleId: id[:13] + "800", Version: 65536}}}, LatestUpdate: 65536, Dlc: map[string]db.SwitchFileInfo{}}
	}
	library := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000030": game("0100000000030000", collection),
		"0100000000040": game("0100000000040000", collection),
		"0100000000050": game("0100000000050000", single),
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}
	known := map[string]*db.SwitchTitle{}
	for prefix, name := range map[string]string{"0100000000030": "First", "0100000000040": "Second", "0100000000050": "Single"} {
		known[prefix] = &db.SwitchTitle{Attributes: db.TitleAttributes{Name: name}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: known}, library)

	candidates := web.splitCandidates()
	if len(candidates) != 1 || candidates[0].Path != filepath.Join(folder, collection.FileName) || candidates[0].Titles != 4 || candidates[0].Games != 2 {
		t.Fatalf("only the card with several games: %+v", candidates)
	}
	part := switchfs.SplitPart{TitleId: "0100000000040000", Type: "BASE", Version: 65536, Cartridge: true, Compressed: true}
	if name := web.splitPartName(part, "First"); name != "Second [0100000000040000][v65536].xcz" {
		t.Fatalf("a game of a card is named after itself: %s", name)
	}
	part.Compressed = false
	if name := web.splitPartName(part, "First"); !strings.HasSuffix(name, ".xci") {
		t.Fatalf("an XCI: %s", name)
	}
}
