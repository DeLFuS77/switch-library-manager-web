package web

import (
	"bytes"
	"context"
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

func compressWeb(t *testing.T) (*Web, string) {
	web, path, _ := compressWebWithLibrary(t)
	return web, path
}

// compressWebWithLibrary also returns a function that puts the test library back, as
// the rescan after a compression replaces it.
func compressWebWithLibrary(t *testing.T) (*Web, string, func()) {
	t.Helper()
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	if err := testnsp.WriteKeys(web.dataFolder); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { settings.InitSwitchKeys(t.TempDir()) })

	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	web.localDbManager = manager

	folder := t.TempDir()
	path := filepath.Join(folder, "Game [0100000000010000][v0].nsp")
	nsp, err := testnsp.WriteNsp(path)
	if err != nil {
		t.Fatal(err)
	}
	library := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {
				BaseExist: true,
				File: db.SwitchFileInfo{
					ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(path), BaseFolder: folder, Size: int64(len(nsp))},
					Metadata:     &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"},
				},
				Updates: map[int]db.SwitchFileInfo{},
				Dlc:     map[string]db.SwitchFileInfo{},
			},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{},
	}
	web.state.set(nil, library)
	web.HandleCompress()
	return web, path, func() { web.state.set(nil, library) }
}

func postForm(web *Web, path string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, request)
	return recorder
}

func waitForCompression(t *testing.T, web *Web) Task {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for web.compressionRunning() {
		if time.Now().After(deadline) {
			t.Fatal("the compression did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, task := range web.taskLog().Snapshot() {
		if task.Kind == TASK_COMPRESS {
			return task
		}
	}
	t.Fatal("no compression task")
	return Task{}
}

func TestCompressPageAndRun(t *testing.T) {
	web, path := compressWeb(t)

	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/compress.html", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), filepath.Base(path)) {
		t.Fatalf("the NSP must be listed: %v", page.Code)
	}

	if code := postForm(web, "/compress/start", url.Values{"path": {"/etc/passwd"}}).Code; code != http.StatusBadRequest {
		t.Fatalf("files outside the library must be refused: %v", code)
	}

	started := postForm(web, "/compress/start", url.Values{"path": {path}, "level": {"fast"}, "delete_originals": {"true"}})
	if started.Code != http.StatusAccepted {
		t.Fatalf("start: %v %s", started.Code, started.Body.String())
	}
	task := waitForCompression(t, web)
	if task.Status != TASK_SUCCESS || task.Files != 1 || task.Saved <= 0 {
		t.Fatalf("task: %+v", task)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the original must be deleted after the verification")
	}
	if _, err := os.Stat(switchfs.NszPath(path)); err != nil {
		t.Fatal("the NSZ must exist")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatal("no temporary file may be left")
		}
	}
}

func TestCompressKeepsOriginalsWhenAsked(t *testing.T) {
	web, path, restore := compressWebWithLibrary(t)
	if code := postForm(web, "/compress/start", url.Values{"path": {path}}).Code; code != http.StatusAccepted {
		t.Fatalf("start: %v", code)
	}
	if task := waitForCompression(t, web); task.Status != TASK_SUCCESS {
		t.Fatalf("task: %+v", task)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the original must be kept")
	}
	// wait for the rescan that follows the compression
	for deadline := time.Now().Add(10 * time.Second); web.state.IsSynchronizing() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}

	restore()
	// a file with a compressed copy is not offered again
	if code := postForm(web, "/compress/start", url.Values{"path": {path}}).Code; code != http.StatusBadRequest {
		t.Fatalf("a file that already has an NSZ must not be compressed again: %v", code)
	}
	if len(web.uncompressedCandidates()) != 0 || len(web.compressCandidates()) != 1 {
		t.Fatal("the compressed original is only listed on the Space page")
	}
}

func TestDamagedFilesAreNotCompressed(t *testing.T) {
	web, path := compressWeb(t)
	data, _ := os.ReadFile(path)
	data[len(data)-100] ^= 0xFF
	os.WriteFile(path, data, 0o644)

	postForm(web, "/compress/start", url.Values{"path": {path}, "delete_originals": {"true"}})
	task := waitForCompression(t, web)
	if task.Status != TASK_FAILED || len(task.Warnings) != 1 || task.Warnings[0].Text != NOTE_COMPRESS_DAMAGED {
		t.Fatalf("a damaged file must be reported: %+v", task)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a damaged original must be kept")
	}
}

func TestCompressNotesAreTranslated(t *testing.T) {
	for _, text := range compressNoteTexts {
		if _, ok := translations["es"][text]; !ok {
			t.Errorf("no Spanish translation for %q", text)
		}
	}
}

func TestDecompressFromThePage(t *testing.T) {
	web, path := compressWeb(t)
	original, _ := os.ReadFile(path)
	nsz := switchfs.CompressedPath(path)
	if _, err := switchfs.CompressGame(context.Background(), path, nsz, switchfs.CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	info, _ := os.Stat(nsz)
	web.state.set(nil, &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {
				BaseExist: true,
				File: db.SwitchFileInfo{
					ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(nsz), BaseFolder: filepath.Dir(nsz), Size: info.Size()},
					Metadata:     &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"},
				},
				Updates: map[int]db.SwitchFileInfo{},
				Dlc:     map[string]db.SwitchFileInfo{},
			},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{},
	})

	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/compress.html", nil))
	if !strings.Contains(page.Body.String(), "decompressForm") || !strings.Contains(page.Body.String(), `data-file-list="/compress/nsz-list"`) {
		t.Fatal("the Decompress section must be shown")
	}
	// the NSZ files are loaded when the section is opened, filtered on the server
	list := httptest.NewRecorder()
	web.router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/compress/nsz-list?q=game", nil))
	if !strings.Contains(list.Body.String(), filepath.Base(nsz)) {
		t.Fatalf("the NSZ must be offered for decompression: %s", list.Body.String())
	}
	none := httptest.NewRecorder()
	web.router.ServeHTTP(none, httptest.NewRequest(http.MethodGet, "/compress/nsz-list?q=nothing-like-this", nil))
	if strings.Contains(none.Body.String(), filepath.Base(nsz)) {
		t.Fatal("the filter is applied on the server")
	}

	if code := postForm(web, "/decompress/start", url.Values{"path": {nsz}, "delete_compressed": {"true"}}).Code; code != http.StatusAccepted {
		t.Fatalf("start: %v", code)
	}
	deadline := time.Now().Add(20 * time.Second)
	for web.compressionRunning() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	var task Task
	for _, candidate := range web.taskLog().Snapshot() {
		if candidate.Kind == TASK_DECOMPRESS {
			task = candidate
			break
		}
	}
	if task.Status != TASK_SUCCESS || task.Files != 1 {
		t.Fatalf("task: %+v", task)
	}
	restored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(restored, original) {
		t.Fatal("the NSP must be restored byte for byte")
	}
	if _, err := os.Stat(nsz); !os.IsNotExist(err) {
		t.Fatal("the NSZ must be deleted after the check")
	}
}
