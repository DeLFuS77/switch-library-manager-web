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
	"github.com/dtrunk90/switch-library-manager-web/process"
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
	for _, name := range []string{"notes.txt", ".nsp", "../.nsp", "", "game.nsp.exe"} {
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

func TestUploadNamesWithDots(t *testing.T) {
	if uploadName("Wait... What [0100000000010000][v0].nsp") == "" {
		t.Fatal("dots in a name are fine")
	}
	for _, name := range []string{"..", "../..", `..\`} {
		if uploadName(name) != "" {
			t.Fatalf("%q is refused", name)
		}
	}
	// a game whose name starts with a dot is saved without it, not hidden
	for _, name := range []string{".cat [0100000000010000][v0].nsz", "../.cat [0100000000010000][v0].nsz", ". cat [0100000000010000][v0].nsz"} {
		if got := uploadName(name); got != "cat [0100000000010000][v0].nsz" {
			t.Fatalf("%q is saved as %q", name, got)
		}
	}
}

// A name too long for a disk that keeps names decomposed (ZFS on Unraid) is shortened before
// its title ID, instead of failing when the file takes its name.
func TestUploadNameTooLongForTheDisk(t *testing.T) {
	name := "SDガンダム ジージェネレーション クロスレイズ プレミアムGサウンドエディション [換金オプションパーツ「GREEN INGOT」（プレミアムGサウンドエディション用）][010022900D3ED020][JP][v0].nsp"
	got := uploadName(name)
	if got == "" || got == name || process.NameBytes(got) > uploadNameBytes || !strings.HasSuffix(got, "[010022900D3ED020][JP][v0].nsp") {
		t.Fatalf("%q (%d bytes)", got, process.NameBytes(got))
	}
}

func uploadTaskOf(web *Web, id int64) *Task {
	for _, task := range web.taskLog().Snapshot() {
		if task.Id == id {
			return &task
		}
	}
	return nil
}

// An upload shows in Tasks while pieces come, ends with the file, and ends as stopped when
// nothing comes any more; the same file chosen again shows again.
func TestUploadsInTasks(t *testing.T) {
	web, _ := uploadWeb(t)
	content := make([]byte, 3<<20)
	response := postForm(web, "/uploads/start", url.Values{"name": {"Game.nsp"}, "size": {strconv.Itoa(len(content))}})
	upload := web.uploads.get(uploadAnswer(t, response)["id"].(string))
	first := upload.taskId
	if task := uploadTaskOf(web, first); task == nil || task.Kind != TASK_UPLOAD || !task.Running() || task.Message != "Game.nsp" {
		t.Fatalf("the upload is a task: %+v", task)
	}
	putPiece(web, upload.Id, 0, content[:2<<20])
	if task := uploadTaskOf(web, first); task.Current != 2 || task.Total != 3 {
		t.Fatalf("its progress: %d of %d", task.Current, task.Total)
	}

	// nothing comes: the task ends, the file is kept
	upload.touched = time.Now().Add(-uploadStalled - time.Second)
	web.endStalledUploads()
	if task := uploadTaskOf(web, first); task.Running() || task.Error == nil || task.Error.Text != NOTE_UPLOAD_STOPPED {
		t.Fatalf("a stopped upload: %+v", task)
	}
	// the same file again goes on, in a new task
	again := uploadAnswer(t, postForm(web, "/uploads/start", url.Values{"name": {"Game.nsp"}, "size": {strconv.Itoa(len(content))}}))
	if again["received"].(float64) != 2<<20 || upload.taskId == 0 || upload.taskId == first {
		t.Fatalf("goes on in a new task: %v %d", again, upload.taskId)
	}
	second := upload.taskId
	putPiece(web, upload.Id, 2<<20, content[2<<20:])
	if response := postForm(web, "/uploads/finish", url.Values{"id": {upload.Id}}); response.Code != http.StatusOK {
		t.Fatalf("finish: %d", response.Code)
	}
	if task := uploadTaskOf(web, second); task.Running() || task.Status != TASK_SUCCESS || task.Subject != "Game.nsp" || task.Files != 1 || task.Saved != int64(len(content)) {
		t.Fatalf("a finished upload: %+v", task)
	}

	// cancelled
	response = postForm(web, "/uploads/start", url.Values{"name": {"Other.nsp"}, "size": {"10"}})
	other := web.uploads.get(uploadAnswer(t, response)["id"].(string))
	id := other.taskId
	postForm(web, "/uploads/cancel", url.Values{"id": {other.Id}})
	if task := uploadTaskOf(web, id); task.Running() || task.Error == nil || task.Error.Text != NOTE_UPLOAD_CANCELED {
		t.Fatalf("a cancelled upload: %+v", task)
	}
	for _, text := range []string{NOTE_UPLOAD_CANCELED, NOTE_UPLOAD_STOPPED, "Upload", "Upload to"} {
		if translate("es", text) == text {
			t.Errorf("not translated: %q", text)
		}
	}
}

// the folder asked for must be one of the library
func TestUploadToAnotherFolderOfTheLibrary(t *testing.T) {
	web, main := uploadWeb(t)
	second := t.TempDir()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.ScanFolders = []string{second} })
	elsewhere := t.TempDir()
	start := func(name, folder string) string {
		response := postForm(web, "/uploads/start", url.Values{"name": {name}, "size": {"10"}, "folder": {folder}})
		return filepath.Dir(web.uploads.get(uploadAnswer(t, response)["id"].(string)).final)
	}
	if got := start("A.nsp", second); got != second {
		t.Fatalf("a folder of the library: %s", got)
	}
	if got := start("B.nsp", elsewhere); got != main {
		t.Fatalf("any other folder is the main one: %s", got)
	}
	if folders := web.uploadFolders(); len(folders) != 2 || folders[0] != main {
		t.Fatalf("the folders offered: %v", folders)
	}
}
