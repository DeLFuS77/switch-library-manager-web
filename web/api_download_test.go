package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A game compressed and deleted since the scan is downloaded as its compressed copy, until
// the next scan tells the library.
func TestDownloadOfAGameCompressedSinceTheScan(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	game := db.ExtendedFileInfo{FileName: "Game [0100000000010000][v0].nsp", BaseFolder: dir, Size: 300}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": {BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: game, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"}},
			Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}},
	}})
	web.HandleApi()
	web.HandleArchive()
	get := func(url string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))
		return recorder
	}

	if missing := get("/api/titles/0100000000010000"); missing.Code != http.StatusNotFound {
		t.Fatalf("neither the game nor a copy: %d", missing.Code)
	}
	os.WriteFile(filepath.Join(dir, "Game [0100000000010000][v0].nsz"), []byte("compressed"), 0644)
	download := get("/api/titles/0100000000010000")
	if download.Code != http.StatusOK || download.Body.String() != "compressed" || !strings.Contains(download.Header().Get("Content-Disposition"), "[v0].nsz") {
		t.Fatalf("the compressed copy: %d %q %q", download.Code, download.Body.String(), download.Header().Get("Content-Disposition"))
	}
	if archive := get("/api/titles/0100000000010000/archive.zip"); archive.Code != http.StatusOK || !strings.Contains(archive.Body.String(), "[v0].nsz") {
		t.Fatalf("the archive holds the compressed copy: %d", archive.Code)
	}

	// the original is still there: it is the one served
	os.WriteFile(filepath.Join(dir, game.FileName), []byte("original"), 0644)
	if download := get("/api/titles/0100000000010000"); download.Body.String() != "original" {
		t.Fatalf("the original: %q", download.Body.String())
	}
}
