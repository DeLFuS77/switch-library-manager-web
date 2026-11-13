package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// organizedFile writes a file where Organize put it, and gives the move it made.
func organizedFile(t *testing.T, from string, to string, data string) process.Operation {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return process.Operation{Kind: process.OP_MOVE, From: from, To: to}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(data)
}

// undoNow puts back a run as the page does, holding the scan.
func undoNow(t *testing.T, web *Web, id int64) UndoResponse {
	t.Helper()
	if !web.holdScan() {
		t.Fatal("the scan is free")
	}
	defer web.releaseScan("")
	response, err := web.runUndo(id, "en")
	if err != nil {
		t.Fatal(err)
	}
	return response
}

// A real run of Organize keeps, in the data folder, each file it moved and the folders it made;
// the undo puts every file back and deletes those folders. A cleanup, which only deletes, keeps
// nothing to undo.
func TestOrganizeIsKeptAndUndone(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)
	romDir := localDB.TitlesMap["0100000000010"].File.ExtendedInfo.BaseFolder
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Folder = romDir
		s.OrganizeOptions.CreateFolderPerGame = true
		s.OrganizeOptions.FolderNameTemplate = "{TITLE_NAME}"
		s.OrganizeOptions.RenameFiles = true
		s.OrganizeOptions.UpdatesFolder = "Updates"
	})
	before := map[string]string{}
	entries, _ := os.ReadDir(romDir)
	for _, entry := range entries {
		before[entry.Name()] = readText(t, filepath.Join(romDir, entry.Name()))
	}

	if !web.holdScan() {
		t.Fatal("the scan is free")
	}
	operations, err := web.runOrganizeFiles(ORGANIZE_ACTION_ORGANIZE, nil, tasklog.TRIGGER_MANUAL)
	web.releaseScan("")
	if err != nil {
		t.Fatal(err)
	}
	runs := web.organizeRuns().list()
	if len(runs) != 1 || len(runs[0].Files) == 0 || len(runs[0].Folders) == 0 || runs[0].Action != ORGANIZE_ACTION_ORGANIZE {
		t.Fatalf("the run kept: %+v (operations %+v)", runs, operations)
	}
	for _, file := range runs[0].Files {
		info, err := os.Stat(file.To)
		if err != nil || info.Size() != file.Size || info.ModTime().UnixNano() != file.ModTime {
			t.Fatalf("%s kept with its size and date once moved: %+v", file.To, file)
		}
	}
	// kept on the disk, for after a restart
	var saved []organizeRun
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(web.dataFolder, ORGANIZE_RUNS_FILENAME))), &saved); err != nil || len(saved) != 1 {
		t.Fatalf("saved: %v %v", saved, err)
	}

	response := undoNow(t, web, runs[0].Id)
	if response.Back != len(runs[0].Files) || len(response.Skipped) != 0 {
		t.Fatalf("%+v", response)
	}
	after := map[string]string{}
	entries, _ = os.ReadDir(romDir)
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("the folder %s Organize made goes", entry.Name())
		}
		after[entry.Name()] = readText(t, filepath.Join(romDir, entry.Name()))
	}
	if len(after) != len(before) {
		t.Fatalf("before %v, after %v", before, after)
	}
	for name, data := range before {
		if after[name] != data {
			t.Fatalf("%s is back as it was: %q", name, after[name])
		}
	}
	if len(web.organizeRuns().list()) != 0 {
		t.Fatal("a run put back whole is no longer kept")
	}
	task, _ := web.latestTask(tasklog.TASK_UNDO_ORGANIZE)
	if task.Running() || task.Files != response.Back {
		t.Fatalf("the task: %+v", task)
	}

	if !web.holdScan() {
		t.Fatal("the scan is free")
	}
	web.runOrganizeFiles(ORGANIZE_ACTION_CLEANUP, nil, tasklog.TRIGGER_MANUAL)
	web.releaseScan("")
	if len(web.organizeRuns().list()) != 0 {
		t.Fatal("a cleanup keeps nothing to undo")
	}
}

// The last ten runs are kept, the newest first; the older ones go, and a run that moved nothing
// is not kept.
func TestOrganizeRunsKeepTheLastTen(t *testing.T) {
	web := newTestWeb(t)
	root := t.TempDir()
	for i := 0; i < organizeRunsKept+2; i++ {
		name := strconv.Itoa(i) + ".nsp"
		op := organizedFile(t, filepath.Join(root, "loose", name), filepath.Join(root, "Games", name), "x")
		if err := web.organizeRuns().add(ORGANIZE_ACTION_NEW, []process.Operation{op}); err != nil {
			t.Fatal(err)
		}
	}
	web.organizeRuns().add(ORGANIZE_ACTION_ORGANIZE, []process.Operation{{Kind: process.OP_SKIP, From: filepath.Join(root, "a.nsp")}})
	runs := web.organizeRuns().list()
	if len(runs) != organizeRunsKept {
		t.Fatalf("%d runs", len(runs))
	}
	for i, run := range runs {
		if want := strconv.Itoa(organizeRunsKept+1-i) + ".nsp"; filepath.Base(run.Files[0].To) != want {
			t.Fatalf("run %d: %s, want %s", i, run.Files[0].To, want)
		}
		if i > 0 && run.Id >= runs[i-1].Id {
			t.Fatal("each run has its own id, the newest the highest")
		}
	}
	// read again from the disk, as after a restart, with no temporary file left beside it
	again := &organizeRuns{path: filepath.Join(web.dataFolder, ORGANIZE_RUNS_FILENAME)}
	if reloaded := again.list(); len(reloaded) != organizeRunsKept || reloaded[0].Id != runs[0].Id {
		t.Fatalf("reloaded %d runs", len(reloaded))
	}
	entries, _ := os.ReadDir(web.dataFolder)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("left %s", entry.Name())
		}
	}
}

// The undo puts the files back the last one moved first, so a file that took the place another
// one left goes back before that one; it never writes over a file, and leaves the ones taken,
// changed or gone, each with why. The ones that can be tried again stay kept, and only the
// folders Organize made that are empty go.
func TestUndoSkipsWhatItCannotPutBack(t *testing.T) {
	web := newTestWeb(t)
	root := t.TempDir()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Folder = root })
	// B left its place for A in the same run
	movedB := organizedFile(t, filepath.Join(root, "b.nsp"), filepath.Join(root, "B", "b.nsp"), "game b")
	movedA := organizedFile(t, filepath.Join(root, "old", "a.nsp"), filepath.Join(root, "b.nsp"), "game a")
	taken := organizedFile(t, filepath.Join(root, "c.nsp"), filepath.Join(root, "C", "c.nsp"), "game c")
	changed := organizedFile(t, filepath.Join(root, "d.nsz"), filepath.Join(root, "D", "d.nsz"), "game d")
	gone := organizedFile(t, filepath.Join(root, "e.nsp"), filepath.Join(root, "E", "e.nsp"), "game e")
	made := []process.Operation{}
	for _, folder := range []string{"B", "C", "D", "E"} {
		made = append(made, process.Operation{Kind: process.OP_MKDIR, To: filepath.Join(root, folder)})
	}
	if err := web.organizeRuns().add(ORGANIZE_ACTION_ORGANIZE, append(made, movedB, movedA, taken, changed, gone)); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(taken.From, []byte("another file"), 0o644)
	os.WriteFile(changed.To, []byte("game d, compressed again"), 0o644)
	os.Remove(gone.To)
	id := web.organizeRuns().list()[0].Id

	check := web.checkUndo(web.organizeRuns().list()[0], "en")
	if check.Back != 2 || check.Skipped != 2 || check.Gone != 1 || check.ButtonText != "Put back 2 files" {
		t.Fatalf("the look: %+v", check)
	}
	if check.Files[0].Path != filepath.Join("E", "e.nsp") {
		t.Fatalf("the last one moved first, its path in the library folder: %+v", check.Files[0])
	}
	if readText(t, taken.From) != "another file" {
		t.Fatal("the look changes nothing")
	}

	response := undoNow(t, web, id)
	if response.Back != 2 || len(response.Skipped) != 3 {
		t.Fatalf("%+v", response)
	}
	states := map[string]string{}
	for _, file := range response.Skipped {
		states[filepath.Base(file.Path)] = file.State
	}
	if states["c.nsp"] != process.UNDO_TAKEN || states["d.nsz"] != process.UNDO_CHANGED_SIZE || states["e.nsp"] != process.UNDO_GONE {
		t.Fatalf("why each one stays: %v", states)
	}
	if readText(t, movedA.From) != "game a" || readText(t, movedB.From) != "game b" {
		t.Fatal("A and B are back in their places, in order")
	}
	if readText(t, taken.From) != "another file" || readText(t, taken.To) != "game c" || readText(t, changed.To) != "game d, compressed again" {
		t.Fatal("nothing is written over, and the files left stay as they are")
	}
	for folder, stays := range map[string]bool{"B": false, "E": false, "C": true, "D": true, "old": true} {
		if _, err := os.Stat(filepath.Join(root, folder)); (err == nil) != stays {
			t.Errorf("%s stays %v: %v", folder, stays, err)
		}
	}

	// the ones taken and changed stay, to try again; the one gone does not
	runs := web.organizeRuns().list()
	if len(runs) != 1 || len(runs[0].Files) != 2 || runs[0].Undone.IsZero() || len(runs[0].Folders) != 2 {
		t.Fatalf("what is left: %+v", runs)
	}
	if rows := web.undoRows(); len(rows) != 1 || !rows[0].Undone || rows[0].Files != 2 {
		t.Fatalf("the row: %+v", rows)
	}
	// once the place of before is free, it goes back too
	os.Remove(taken.From)
	if response := undoNow(t, web, id); response.Back != 1 || len(response.Skipped) != 1 {
		t.Fatalf("again: %+v", response)
	}
	if readText(t, taken.From) != "game c" {
		t.Fatal("put back once its place was free")
	}
	if _, err := os.Stat(filepath.Join(root, "C")); err == nil {
		t.Fatal("its folder, left empty, goes")
	}
}

// An undo the app was stopped in the middle of goes on where it stopped: the files it put back
// are taken as back, and the others go back.
func TestUndoCutHalfwayGoesOn(t *testing.T) {
	web := newTestWeb(t)
	root := t.TempDir()
	operations := []process.Operation{}
	for _, name := range []string{"a", "b", "c", "d"} {
		operations = append(operations, organizedFile(t, filepath.Join(root, name+".nsp"), filepath.Join(root, "Games", strings.ToUpper(name)+".nsp"), name))
	}
	web.organizeRuns().add(ORGANIZE_ACTION_ORGANIZE, operations)
	run := web.organizeRuns().list()[0]
	// the last two moved went back before the stop
	for _, move := range run.Files[2:] {
		if state, err := process.PutBack(move, nil); state != process.UNDO_BACK || err != nil {
			t.Fatalf("%s %v", state, err)
		}
	}
	if check := web.checkUndo(run, "en"); check.Back != 4 || check.Skipped != 0 || check.Gone != 0 {
		t.Fatalf("the look: %+v", check)
	}
	if response := undoNow(t, web, run.Id); response.Back != 4 || len(response.Skipped) != 0 {
		t.Fatalf("%+v", response)
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		if readText(t, filepath.Join(root, name+".nsp")) != name {
			t.Fatalf("%s is back", name)
		}
	}
	if len(web.organizeRuns().list()) != 0 {
		t.Fatal("nothing left to undo")
	}
}

// An undo never runs while Organize or another task works on the files, and Organize never runs
// while an undo does: both hold the same lock.
func TestUndoWaitsForTheOtherTasks(t *testing.T) {
	web := newTestWeb(t)
	root := t.TempDir()
	op := organizedFile(t, filepath.Join(root, "a.nsp"), filepath.Join(root, "A", "a.nsp"), "a")
	web.organizeRuns().add(ORGANIZE_ACTION_ORGANIZE, []process.Operation{op})
	id := web.organizeRuns().list()[0].Id
	web.organizing.Store(true)
	if _, err := web.runUndo(id, "en"); err != errOrganizeBusy {
		t.Fatalf("while Organize runs: %v", err)
	}
	web.organizing.Store(false)
	if _, err := os.Stat(op.To); err != nil {
		t.Fatal("nothing moved")
	}
	if _, err := web.runUndo(id+1, "en"); err != errUndoRunGone {
		t.Fatalf("a run not kept: %v", err)
	}
}

// The window asks the server what the undo would do: counts, each file with its state and text
// in the language of the page; a run not kept is told.
func TestUndoCheckOverHttp(t *testing.T) {
	web := newTestWeb(t)
	web.handleOrganizeUndo()
	root := t.TempDir()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Folder = root })
	op := organizedFile(t, filepath.Join(root, "loose", "a.nsp"), filepath.Join(root, "A", "a.nsp"), "a")
	web.organizeRuns().add(ORGANIZE_ACTION_ORGANIZE, []process.Operation{op})
	id := web.organizeRuns().list()[0].Id

	post := func(id string) *httptest.ResponseRecorder {
		form := url.Values{"id": {id}}
		request := httptest.NewRequest(http.MethodPost, "/organize/undo/check", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Accept-Language", "es")
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, request)
		return recorder
	}
	recorder := post(strconv.FormatInt(id, 10))
	var check UndoCheckResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &check); err != nil || recorder.Code != http.StatusOK {
		t.Fatalf("%d %s", recorder.Code, recorder.Body)
	}
	if check.Back != 1 || len(check.Files) != 1 || check.Files[0].State != process.UNDO_BACK || !strings.Contains(check.Files[0].Text, "vuelve a") || check.ButtonText != "Devolver 1 archivos" {
		t.Fatalf("%+v", check)
	}
	if recorder := post("12"); recorder.Code != http.StatusNotFound {
		t.Fatalf("a run not kept: %d", recorder.Code)
	}
}
