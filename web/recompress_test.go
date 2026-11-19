package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// inBlocks tells an NSZ made of blocks compressed on their own: its storage, and the magic of
// the blocks after the section table of its NCZ.
func inBlocks(t *testing.T, path string) bool {
	t.Helper()
	storage, err := switchfs.ReadStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	magic := bytes.Contains(data, []byte("NCZBLOCK"))
	if magic != (storage == switchfs.StorageBlocks) {
		t.Fatalf("the storage %q and the magic NCZBLOCK (%v) disagree", storage, magic)
	}
	return magic
}

// Made for emulators on the Compress page: the NSZ is written in blocks (NCZBLOCK), checked, and
// installers read it as any NSZ; for installers, solid as before (#193).
func TestCompressInBlocksForEmulators(t *testing.T) {
	web, path := compressWeb(t)
	started := postForm(web, "/compress/start", url.Values{"path": {path}, "level": {"fast"}, "blocks": {"true"}})
	if started.Code != http.StatusAccepted {
		t.Fatalf("start: %v %s", started.Code, started.Body.String())
	}
	task := waitForTask(t, web, tasklog.TASK_COMPRESS)
	if task.Status != tasklog.TASK_SUCCESS || task.Files != 1 {
		t.Fatalf("task: %+v", task)
	}
	nsz := switchfs.NszPath(path)
	if !inBlocks(t, nsz) {
		t.Fatal("made for emulators, the NSZ must be in blocks")
	}
	if _, err := switchfs.VerifyFile(context.Background(), nsz, nil); err != nil {
		t.Fatalf("the NSZ in blocks must pass its check: %v", err)
	}

	os.Remove(nsz)
	if _, err := web.compressFile(context.Background(), path, switchfs.LevelFast, false, false, noSteps); err != nil {
		t.Fatal(err)
	}
	if inBlocks(t, nsz) {
		t.Fatal("made for installers, the NSZ must be solid")
	}
}

// The way the NSZ files are made goes with the task: waiting in the queue, and in what it has
// left when the app stops in the middle.
func TestBlocksGoWithTheTask(t *testing.T) {
	data, _ := json.Marshal(QueuedTask{Kind: tasklog.TASK_COMPRESS, Blocks: true})
	var queued QueuedTask
	if err := json.Unmarshal(data, &queued); err != nil || !queued.Blocks {
		t.Fatalf("the queue keeps it: %s", data)
	}
	run := &fileTaskRun{resume: &tasklog.Resume{Level: "fast", Blocks: true}, paths: []string{"a", "b"}, pending: map[int]bool{1: true}}
	if left := run.leftResume(); !left.Blocks || left.Level != "fast" || len(left.Paths) != 1 || left.Paths[0] != "b" {
		t.Fatalf("what is left: %+v", left)
	}
}

// The Compress page marks first what Settings chooses, solid unless it asks for blocks.
func TestCompressPageStartsAsSettingsMakesTheNsz(t *testing.T) {
	web, _ := compressWeb(t)
	checked := func() string {
		page := httptest.NewRecorder()
		web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/compress.html", nil))
		found := regexp.MustCompile(`name="blocks" id="madeFor\w+" value="(\w+)" checked`).FindAllStringSubmatch(page.Body.String(), -1)
		if len(found) != 1 {
			t.Fatalf("one is chosen: %v", found)
		}
		return found[0][1]
	}
	if got := checked(); got != "false" {
		t.Fatalf("solid unless Settings asks for blocks: %q", got)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NszBlocks = true })
	if got := checked(); got != "true" {
		t.Fatalf("blocks as Settings asks: %q", got)
	}
}

// recompressWeb has the NSZ of the test game, solid, in the library, as the scan notes it.
func recompressWeb(t *testing.T) (*Web, string) {
	t.Helper()
	web, path := compressWeb(t)
	nsz := switchfs.CompressedPath(path)
	if _, err := switchfs.CompressGame(context.Background(), path, nsz, switchfs.CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	library := func() {
		info, _ := os.Stat(nsz)
		storage, _ := switchfs.ReadStorage(nsz)
		web.state.set(nil, &db.LocalSwitchFilesDB{
			TitlesMap: map[string]*db.SwitchGameFiles{
				"0100000000010": {
					BaseExist: true,
					File: db.SwitchFileInfo{
						ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(nsz), BaseFolder: filepath.Dir(nsz), Size: info.Size()},
						Metadata:     &switchfs.ContentMetaAttributes{TitleId: "0100000000010000", Storage: storage},
					},
					Updates: map[int]db.SwitchFileInfo{},
					Dlc:     map[string]db.SwitchFileInfo{},
				},
			},
			Skipped: map[db.ExtendedFileInfo]db.SkippedFile{},
		})
		web.invalidateDerived()
	}
	library()
	t.Cleanup(func() { waitForBackgroundWork(web) })
	return web, nsz
}

// Recompress lists the solid NSZ to write them in blocks, writes each one again in place, checked,
// and back to solid the other way, with no file left beside it (#193).
func TestRecompressBothWays(t *testing.T) {
	web, nsz := recompressWeb(t)
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/compress.html", nil))
	if !strings.Contains(page.Body.String(), `id="tabRecompress"`) || !strings.Contains(page.Body.String(), `data-file-list="/compress/recompress-list?to=blocks"`) {
		t.Fatal("the Recompress section must be offered, to write the solid NSZ in blocks")
	}
	list := func(to string) string {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/compress/recompress-list?to="+to+"&q=game", nil))
		return recorder.Body.String()
	}
	if !strings.Contains(list("blocks"), filepath.Base(nsz)) || strings.Contains(list("solid"), filepath.Base(nsz)) {
		t.Fatal("a solid NSZ is listed to be written in blocks only")
	}
	if code := postForm(web, "/recompress/start", url.Values{"path": {nsz}, "to": {"solid"}}).Code; code != http.StatusBadRequest {
		t.Fatalf("a solid NSZ is not written solid again: %v", code)
	}
	if code := postForm(web, "/recompress/start", url.Values{"path": {"/etc/passwd"}, "to": {"blocks"}}).Code; code != http.StatusBadRequest {
		t.Fatalf("files outside the library must be refused: %v", code)
	}

	started := postForm(web, "/recompress/start", url.Values{"path": {nsz}, "to": {"blocks"}})
	if started.Code != http.StatusAccepted {
		t.Fatalf("start: %v %s", started.Code, started.Body.String())
	}
	task := waitForTask(t, web, tasklog.TASK_RECOMPRESS)
	if task.Status != tasklog.TASK_SUCCESS || task.Files != 1 {
		t.Fatalf("task: %+v", task)
	}
	if !inBlocks(t, nsz) {
		t.Fatal("the NSZ must be in blocks now")
	}
	if _, err := switchfs.VerifyFile(context.Background(), nsz, nil); err != nil {
		t.Fatalf("the NSZ written again must pass its check: %v", err)
	}

	if _, err := web.recompressFile(context.Background(), nsz, false, noSteps); err != nil {
		t.Fatal(err)
	}
	if inBlocks(t, nsz) {
		t.Fatal("the NSZ must be solid again")
	}
	// one written so already is left as it is
	before, _ := os.Stat(nsz)
	if saved, err := web.recompressFile(context.Background(), nsz, false, noSteps); err != nil || saved != 0 {
		t.Fatalf("written so already: %v %v", saved, err)
	}
	if after, _ := os.Stat(nsz); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("a file written so already must not be written again")
	}
	entries, _ := os.ReadDir(filepath.Dir(nsz))
	if len(entries) != 1 {
		t.Fatalf("only the NSZ must be in its folder: %v", entries)
	}
}

// With the quarantine keeping originals, the old NSZ goes there, whole, as written again.
func TestRecompressKeepsTheOldOneInTheQuarantine(t *testing.T) {
	web, nsz := recompressWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Quarantine = settings.QuarantineOptions{Keep: QUARANTINE_WEEK, Originals: true}
	})
	old, _ := os.ReadFile(nsz)
	if _, err := web.recompressFile(context.Background(), nsz, true, noSteps); err != nil {
		t.Fatal(err)
	}
	if !inBlocks(t, nsz) {
		t.Fatal("the NSZ must be in blocks now")
	}
	items := web.quarantine().list()
	if len(items) != 1 || items[0].Path != nsz || items[0].Reason != QUARANTINE_RECOMPRESSED {
		t.Fatalf("the old NSZ must be in the quarantine: %+v", items)
	}
	if kept, err := os.ReadFile(items[0].Stored); err != nil || !bytes.Equal(kept, old) {
		t.Fatal("the quarantine must keep the old NSZ whole")
	}
}

// Recompress offers the NSZ by how they are stored: not a game card (XCZ, always in blocks), not
// the extra compression of NXPress, which no emulator opens, nor a file the scan did not read.
func TestRecompressCandidates(t *testing.T) {
	web := newTestWeb(t)
	file := func(name string, storage string) db.SwitchFileInfo {
		return db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: name, BaseFolder: "/games", Size: 10},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010000", Storage: storage}}
	}
	web.state.set(nil, &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {
				BaseExist: true,
				File:      file("solid.nsz", switchfs.StorageSolid),
				Updates:   map[int]db.SwitchFileInfo{65536: file("blocks.nsz", switchfs.StorageBlocks), 131072: file("card.xcz", switchfs.StorageBlocks)},
				Dlc: map[string]db.SwitchFileInfo{"a": file("archive.nsz", switchfs.StorageArchive), "b": file("sparse.nsz", switchfs.StorageSparse),
					"c": file("unread.nsz", ""), "d": file("plain.nsp", switchfs.StoragePlain)},
			},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{},
	})
	names := func(blocks bool) string {
		found := []string{}
		for _, candidate := range web.recompressCandidates(blocks) {
			found = append(found, filepath.Base(candidate.Path))
		}
		return strings.Join(found, " ")
	}
	if got := names(true); got != "solid.nsz" {
		t.Fatalf("to write in blocks: %q", got)
	}
	if got := names(false); got != "blocks.nsz" {
		t.Fatalf("to write solid: %q", got)
	}
}

func TestRecompressTextsAreTranslated(t *testing.T) {
	for _, text := range append(recompressNoteTexts, quarantineReasons[QUARANTINE_RECOMPRESSED], taskKindNames[tasklog.TASK_RECOMPRESS], "Select the files to recompress.") {
		for _, lang := range i18n.SupportedLanguages {
			if _, ok := i18n.Translations[lang][text]; !ok && lang != i18n.DEFAULT_LANGUAGE {
				t.Errorf("no %s translation for %q", lang, text)
			}
		}
	}
}
