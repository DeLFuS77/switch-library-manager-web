package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// zeroBasedNupGame writes a game in a NUP whose metadata is counted from 0 and puts it in the
// library as a scan reads it, and gives its path.
func zeroBasedNupGame(t *testing.T, web *Web) string {
	t.Helper()
	nup := testnup.Build([]testnup.Package{{TitleId: 0x0400486024DEC000, Kind: testnup.Base, Version: 65536,
		Meta: []testnup.Record{{Type: 0, Payload: []byte("META npdm")}, {Type: 2, Payload: testnup.Jpeg},
			{Type: 3, Payload: []byte("1.0.0")}, {Type: 4, Payload: []byte("Made-up Quest")}, {Type: 5, Payload: []byte("Nobody Games")}},
		Files: []testnup.File{{Path: "exefs/main", Data: []byte("code")}, {Path: "exefs/main.npdm", Data: []byte("META npdm")}}}})
	folder := t.TempDir()
	path := filepath.Join(folder, "Made-up Quest [0400486024DEC000][v65536].nup")
	if err := os.WriteFile(path, nup, 0o644); err != nil {
		t.Fatal(err)
	}
	metadata, err := switchfs.ReadNupMetadata(path)
	if err != nil || metadata["0400486024dec000"] == nil || !metadata["0400486024dec000"].Emulators.ZeroBased {
		t.Fatalf("%v %+v", err, metadata)
	}
	_, localDB := web.state.get()
	localDB.TitlesMap["0400486024dec"] = &db.SwitchGameFiles{BaseExist: true,
		File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(path), BaseFolder: folder, Size: int64(len(nup))},
			Metadata: metadata["0400486024dec000"]},
		Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	return path
}

func postNupFix(t *testing.T, web *Web, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/title/nup-fix", strings.NewReader(url.Values{"path": {path}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	web.router.ServeHTTP(recorder, request)
	return recorder
}

// The button of the page of a game writes its NUP again as a task: counted from 1, read again by
// the library, its check kept, and no copy left beside it.
func TestNupFixFromTheGamePage(t *testing.T) {
	web, _ := compressWeb(t)
	web.handleNupFix()
	path := zeroBasedNupGame(t, web)
	web.verifications().remember(path, switchfs.FileCheck{})

	recorder := postNupFix(t, web, path)
	answer := map[string]int{}
	if recorder.Code != http.StatusAccepted || json.Unmarshal(recorder.Body.Bytes(), &answer) != nil || answer["started"] != 1 {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
	task := waitForTask(t, web, tasklog.TASK_NUP_FIX)
	if task.Status != tasklog.TASK_SUCCESS || len(task.Tiles) != 1 || task.Tiles[0].Failed || task.Error != nil {
		t.Fatalf("%+v", task)
	}
	container, err := switchfs.ReadNup(path)
	if err != nil || container.Emulators().ZeroBased || !container.Emulators().Ok() {
		t.Fatalf("not fixed: %v %+v", err, container)
	}
	if _, err := os.Stat(switchfs.NupMetaBackup(path)); !os.IsNotExist(err) {
		t.Errorf("copy left beside it: %v", err)
	}
	// its files are as they were: it stays checked
	info, _ := os.Stat(path)
	if record, ok := web.verifications().current(path, info); !ok || !record.OK {
		t.Errorf("the check is lost: %+v", record)
	}
	// it is in the names of the kinds of tasks, in the queue and in the failures told
	if taskKindNames[tasklog.TASK_NUP_FIX] != NUP_FIX_ACTION || !queueKinds[tasklog.TASK_NUP_FIX] {
		t.Error("the kind is not known everywhere")
	}
}

// Only a NUP of the library whose metadata is counted from 0 is written: any other path, a file
// that is not one, or a NUP counted from 1, is refused before a task starts.
func TestNupFixTakesOnlyANupCountedFromZero(t *testing.T) {
	web, nsp := compressWeb(t)
	web.handleNupFix()
	path := zeroBasedNupGame(t, web)
	outside := filepath.Join(t.TempDir(), "Other.nup")
	os.WriteFile(outside, []byte("not a NUP"), 0o644)
	_, localDB := web.state.get()
	for name, requested := range map[string]string{"none": "", "an NSP": nsp, "not in the library": outside,
		"a folder": filepath.Dir(path), "elsewhere": filepath.Join(filepath.Dir(path), "..", "x", filepath.Base(path))} {
		if recorder := postNupFix(t, web, requested); recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, recorder.Code, recorder.Body.String())
		}
	}
	// counted from 1 already, as the last scan read it
	localDB.TitlesMap["0400486024dec"].File.Metadata.Emulators = &switchfs.NupEmulators{}
	if recorder := postNupFix(t, web, path); recorder.Code != http.StatusBadRequest {
		t.Errorf("counted from 1: %d", recorder.Code)
	}
	for _, task := range web.taskLog().Snapshot() {
		if task.Kind == tasklog.TASK_NUP_FIX {
			t.Fatalf("a task started: %+v", task)
		}
	}
	// a path as the page sends it, not cleaned, is the same file
	localDB.TitlesMap["0400486024dec"].File.Metadata.Emulators = &switchfs.NupEmulators{ZeroBased: true}
	if got := web.zeroBasedNup(filepath.Join(filepath.Dir(path), ".", filepath.Base(path))); got != path {
		t.Errorf("%q", got)
	}
}

// A copy of the metadata of another NUP beside it stops the fix: the task fails, says to remove
// the copy, and the NUP and the copy are left as they were.
func TestNupFixStopsOnTheCopyOfAnotherNup(t *testing.T) {
	web, _ := compressWeb(t)
	web.handleNupFix()
	path := zeroBasedNupGame(t, web)
	original, _ := os.ReadFile(path)
	other := append([]byte("NXPMETA1"), make([]byte, 64)...)
	os.WriteFile(switchfs.NupMetaBackup(path), other, 0o644)

	if recorder := postNupFix(t, web, path); recorder.Code != http.StatusAccepted {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
	task := waitForTask(t, web, tasklog.TASK_NUP_FIX)
	if task.Status != tasklog.TASK_FAILED || task.Error == nil || task.Error.Text != NOTE_NUP_FIX_FAILED ||
		len(task.Warnings) != 1 || task.Warnings[0].Text != NOTE_NUP_FIX_OTHER_COPY ||
		!strings.HasPrefix(task.Warnings[0].Detail, filepath.Base(path)+": ") || len(task.Tiles) != 1 || !task.Tiles[0].Failed {
		t.Fatalf("%+v", task)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, original) {
		t.Error("the NUP was written")
	}
	if now, _ := os.ReadFile(switchfs.NupMetaBackup(path)); !bytes.Equal(now, other) {
		t.Error("the copy of the other NUP was touched")
	}
}

// A fix the app was stopped in the middle of goes on after the restart: the blocks it kept beside
// the NUP are put back first, then it writes them again.
func TestNupFixGoesOnAfterARestart(t *testing.T) {
	web, _ := compressWeb(t)
	path := zeroBasedNupGame(t, web)
	original, _ := os.ReadFile(path)
	copied := filepath.Join(t.TempDir(), "copy.nup")
	os.WriteFile(copied, original, 0o644)
	if _, err := switchfs.FixNupZeroBased(copied); err != nil {
		t.Fatal(err)
	}
	fixed, _ := os.ReadFile(copied)
	// what a fix leaves when it stops in the middle of its block: the first type written, the
	// others not, and the block as it was beside the NUP
	first, last := -1, -1
	for i := range original {
		if original[i] != fixed[i] {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || first == last {
		t.Fatalf("one type fixed at most: %d %d", first, last)
	}
	half := bytes.Clone(original)
	half[first] = fixed[first]
	backup := []byte("NXPMETA1")
	backup = appendLE(backup, uint64(len(original)), 8)
	backup = append(backup, original[0x18:0x38]...)
	backup = appendLE(backup, 1, 4)
	backup = appendLE(backup, uint64(first), 8)
	backup = appendLE(backup, uint64(last+1-first), 4)
	backup = append(backup, original[first:last+1]...)
	os.WriteFile(path, half, 0o644)
	os.WriteFile(switchfs.NupMetaBackup(path), backup, 0o644)

	saved := tasklog.Load(web.dataFolder)
	id := saved.Start(tasklog.TASK_NUP_FIX, tasklog.TRIGGER_MANUAL)
	saved.SetResume(id, &tasklog.Resume{Paths: []string{path}})
	web.resumeInterrupted()
	task := waitForTask(t, web, tasklog.TASK_NUP_FIX)
	if task.Trigger != tasklog.TRIGGER_RESUME || task.Status != tasklog.TASK_SUCCESS {
		t.Fatalf("%+v", task)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, fixed) {
		t.Error("not as fixed at once")
	}
	if _, err := os.Stat(switchfs.NupMetaBackup(path)); !os.IsNotExist(err) {
		t.Errorf("copy left beside it: %v", err)
	}
}

func appendLE(b []byte, v uint64, size int) []byte {
	for i := 0; i < size; i++ {
		b = append(b, byte(v>>(8*i)))
	}
	return b
}

// The page of a game whose NUP is counted from 0 has the button under the orange warning, for
// administrators, with its help; once fixed, the button is gone.
func TestNupFixButtonOnTheGamePage(t *testing.T) {
	web, _ := compressWeb(t)
	path := zeroBasedNupGame(t, web)
	web.HandleTitle()
	page := func(lang string) string {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/title/0400486024DEC000.html", nil)
		request.Header.Set("Accept-Language", lang)
		web.router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%d", recorder.Code)
		}
		return recorder.Body.String()
	}
	body := page("es")
	button := `<button type="button" class="btn btn-sm btn-outline-warning rounded-pill help-name" data-nup-fix="` + strings.ReplaceAll(path, `\`, `\\`)
	if !strings.Contains(body, `<div class="nup-fix admin-only" data-hover-help>`) || !strings.Contains(body, "Arreglar para emuladores</button>") ||
		!strings.Contains(body, "Reescribe en el propio NUP solo el número de tipo") {
		t.Fatalf("no button, in Spanish:\n%s", body)
	}
	if !strings.Contains(body, button) && !strings.Contains(body, `data-nup-fix="`+path+`"`) {
		t.Errorf("the button does not send its file:\n%s", body)
	}
	// the help is the one the tests check in every language
	template, _ := os.ReadFile("../resources/pages/title.html")
	if !strings.Contains(string(template), `{{t "`+NUP_FIX_HELP+`"}}`) || !strings.Contains(string(template), `{{t "`+NUP_FIX_ACTION+`"}}`) {
		t.Error("the page and NUP_FIX_HELP or NUP_FIX_ACTION differ")
	}
	// counted from 1: no button
	_, localDB := web.state.get()
	localDB.TitlesMap["0400486024dec"].File.Metadata.Emulators = &switchfs.NupEmulators{}
	web.invalidateDerived()
	if body := page("en"); strings.Contains(body, "data-nup-fix") || !strings.Contains(body, "nup-emulators") {
		t.Errorf("a button on a NUP counted from 1:\n%s", body)
	}
}

// What the fix tells is in every language of the app.
func TestNupFixTextsAreTranslated(t *testing.T) {
	for _, lang := range i18n.SupportedLanguages {
		if lang == i18n.DEFAULT_LANGUAGE {
			continue
		}
		for _, text := range []string{NUP_FIX_ACTION, NUP_FIX_HELP, NOTE_NUP_FIX_FAILED, NOTE_NUP_FIX_OTHER_COPY, string(errNupFixNotFound)} {
			if i18n.Translate(lang, text) == text {
				t.Errorf("%s: %q", lang, text)
			}
		}
	}
}
