package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func uploadWeb(t *testing.T) (*Web, string) {
	t.Helper()
	web := newTestWeb(t)
	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	web.localDbManager = manager
	folder := t.TempDir()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Folder = folder })
	web.HandleUploads()
	// the scan after an upload must finish before the folders are removed
	t.Cleanup(func() {
		for deadline := time.Now().Add(10 * time.Second); web.state.IsSynchronizing() && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		manager.Close()
	})
	return web, folder
}

func uploadAnswer(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	answer := map[string]any{}
	json.Unmarshal(response.Body.Bytes(), &answer)
	return answer
}

func putPiece(web *Web, id string, offset int, data []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPut, "/uploads/chunk?id="+url.QueryEscape(id)+"&offset="+strconv.Itoa(offset), bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	web.router.ServeHTTP(response, request)
	return response
}

func TestUploadInPieces(t *testing.T) {
	web, folder := uploadWeb(t)
	content := []byte(strings.Repeat("switch game ", 100))
	start := func() map[string]any {
		response := postForm(web, "/uploads/start", url.Values{"name": {"Game [0100000000010000][v0].nsp"}, "size": {strconv.Itoa(len(content))}})
		if response.Code != http.StatusOK {
			t.Fatalf("start: %d %s", response.Code, response.Body.String())
		}
		return uploadAnswer(t, response)
	}
	started := start()
	id := started["id"].(string)

	if response := putPiece(web, id, 0, content[:500]); response.Code != http.StatusOK || uploadAnswer(t, response)["received"].(float64) != 500 {
		t.Fatalf("first piece: %d %s", response.Code, response.Body.String())
	}
	// a piece sent again, or from another place, tells where the server is
	if response := putPiece(web, id, 0, content[:500]); response.Code != http.StatusConflict || uploadAnswer(t, response)["received"].(float64) != 500 {
		t.Fatalf("a piece from the wrong place: %d %s", response.Code, response.Body.String())
	}
	// the page opened again: the same file goes on where it was
	if again := start(); again["id"] != id || again["received"].(float64) != 500 {
		t.Fatalf("the same file goes on: %v", again)
	}
	if response := postForm(web, "/uploads/finish", url.Values{"id": {id}}); response.Code != http.StatusConflict {
		t.Fatalf("not whole yet: %d", response.Code)
	}
	// nothing past the size of the file
	if response := putPiece(web, id, 500, append(append([]byte{}, content[500:]...), 'x')); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("more than the file: %d %s", response.Code, response.Body.String())
	}
	if response := putPiece(web, id, len(content), nil); response.Code != http.StatusOK {
		t.Fatalf("an empty piece at the end: %d", response.Code)
	}
	hidden, _ := filepath.Glob(filepath.Join(folder, uploadPrefix+"*"))
	if len(hidden) != 1 {
		t.Fatalf("the pieces go to one hidden file: %v", hidden)
	}
	if response := postForm(web, "/uploads/finish", url.Values{"id": {id}}); response.Code != http.StatusOK {
		t.Fatalf("finish: %d %s", response.Code, response.Body.String())
	}
	final := filepath.Join(folder, "Game [0100000000010000][v0].nsp")
	if data, err := os.ReadFile(final); err != nil || !bytes.Equal(data, content) {
		t.Fatalf("the file has its name and its content: %v", err)
	}
	if hidden, _ := filepath.Glob(filepath.Join(folder, uploadPrefix+"*")); len(hidden) != 0 {
		t.Fatalf("no hidden file is left: %v", hidden)
	}
	if web.uploads.get(id) != nil {
		t.Fatal("the upload is forgotten")
	}

	// the same name again
	if response := postForm(web, "/uploads/start", url.Values{"name": {"Game [0100000000010000][v0].nsp"}, "size": {"10"}}); response.Code != http.StatusConflict {
		t.Fatalf("a name already in the folder: %d", response.Code)
	}
}

func TestUploadRefusesOtherFiles(t *testing.T) {
	web, folder := uploadWeb(t)
	for _, name := range []string{"notes.txt", ".hidden.nsp", "../.hidden.nsp", "", "game.nsp.exe"} {
		if response := postForm(web, "/uploads/start", url.Values{"name": {name}, "size": {"10"}}); response.Code != http.StatusBadRequest {
			t.Errorf("%q is refused: %d", name, response.Code)
		}
	}
	// a path keeps only its name, inside the library folder
	response := postForm(web, "/uploads/start", url.Values{"name": {`..\..\Game.XCI`}, "size": {"10"}})
	if response.Code != http.StatusOK {
		t.Fatalf("a name with folders: %d %s", response.Code, response.Body.String())
	}
	upload := web.uploads.get(uploadAnswer(t, response)["id"].(string))
	if upload == nil || upload.final != filepath.Join(folder, "Game.XCI") {
		t.Fatalf("saved in the library folder: %+v", upload)
	}
	if response := postForm(web, "/uploads/start", url.Values{"name": {"Big.nsz"}, "size": {strconv.FormatInt(1<<60, 10)}}); response.Code != http.StatusInsufficientStorage {
		t.Fatalf("no space for it: %d", response.Code)
	}
	if response := putPiece(web, "unknown", 0, []byte("x")); response.Code != http.StatusNotFound {
		t.Fatalf("an unknown upload: %d", response.Code)
	}
}

func TestUploadCancel(t *testing.T) {
	web, folder := uploadWeb(t)
	response := postForm(web, "/uploads/start", url.Values{"name": {"Game.nsz"}, "size": {"100"}})
	id := uploadAnswer(t, response)["id"].(string)
	putPiece(web, id, 0, make([]byte, 40))
	if !web.uploads.writing() || !web.filesBusy() {
		t.Fatal("an upload being written keeps the folders busy")
	}
	if response := postForm(web, "/uploads/cancel", url.Values{"id": {id}}); response.Code != http.StatusOK {
		t.Fatalf("cancel: %d", response.Code)
	}
	if entries, _ := os.ReadDir(folder); len(entries) != 0 {
		t.Fatalf("nothing is left: %v", entries)
	}
	if web.uploads.get(id) != nil {
		t.Fatal("the upload is forgotten")
	}
}

func TestUploadsGivenUpAfterADay(t *testing.T) {
	web, folder := uploadWeb(t)
	response := postForm(web, "/uploads/start", url.Values{"name": {"Game.nsp"}, "size": {"100"}})
	id := uploadAnswer(t, response)["id"].(string)
	upload := web.uploads.get(id)
	upload.touched = time.Now().Add(-uploadIdle - time.Minute)
	// one cut before the app was started
	orphan := filepath.Join(folder, uploadPrefix+"old"+uploadSuffix)
	os.WriteFile(orphan, []byte("x"), 0o644)
	old := time.Now().Add(-uploadIdle - time.Hour)
	os.Chtimes(orphan, old, old)
	web.forgetIdleUploads(folder)
	if web.uploads.get(id) != nil {
		t.Fatal("an idle upload is given up")
	}
	if entries, _ := os.ReadDir(folder); len(entries) != 0 {
		t.Fatalf("their files are removed: %v", entries)
	}
}

// the pieces the browser sends are bigger than a form
func TestUploadPieceGetsPastTheBodyLimit(t *testing.T) {
	read := int64(0)
	handler := withBodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, _ = io.Copy(io.Discard, r.Body)
	}))
	piece := 16 << 20
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/uploads/chunk?id=x&offset=0", bytes.NewReader(make([]byte, piece))))
	if read != int64(piece) {
		t.Fatalf("a piece is read whole: %d", read)
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/settings.html", bytes.NewReader(make([]byte, piece))))
	if read >= int64(piece) {
		t.Fatal("other requests keep their limit")
	}
}
