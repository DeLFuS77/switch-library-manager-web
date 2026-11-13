package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/awake"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Undo of Organize: each run of Organize (by hand or by an automation) keeps in the data folder
// what it moved and renamed, with the size and date of each file once moved, and the folders it
// made; the last organizeRunsKept runs are kept. Undo looks at each file first without changing
// anything, and then, as a task, puts back the ones it can, one at a time, looking at each one
// again just before (see process.PutBack): nothing is ever written over, and only the folders the
// run made that are left empty are deleted. The files put back leave the record of their run;
// the ones left (another file at their path of before, or changed since) stay in it, to try
// again once what kept them is sorted out, and the ones no longer there leave it too, as there is
// nothing to put back. A run with no file left is no longer kept. Only administrators see and
// start it (organize.html is a page of administrators, its actions too), and never while
// Organize runs: both hold web.organizing.

const (
	ORGANIZE_RUNS_FILENAME = "organize-undo.json"
	// the runs of Organize that can be undone, the newest first
	organizeRunsKept = 10
	// the files of an undo told one by one in the window before it; the others are counted
	undoFilesShown = 500
)

// the notes of an undo in its task
const (
	NOTE_UNDO_FILE_KEPT = "A file was not put back."
	NOTE_UNDO_MORE      = "More files not put back:"
	// the task, in Tasks and in the activity
	UNDO_ORGANIZE_TASK = "Undo Organize"
)

// why a file is not put back, translated by the interface
var undoReasons = map[string]string{
	process.UNDO_TAKEN:        "Skipped: another file is at “%v” now.",
	process.UNDO_CHANGED_SIZE: "Skipped: it has changed since then (another size).",
	process.UNDO_CHANGED_DATE: "Skipped: it has changed since then (another date).",
	process.UNDO_GONE:         "It is no longer where Organize left it.",
}

// the field of the form an undo tells the activity how many files went back in
const undoActivityFiles = "put_back"

var errUndoRunGone = errorString("This organize is no longer kept: the page is old, open it again.")

// organizeRun is a run of Organize as kept to undo it.
type organizeRun struct {
	Id     int64     `json:"id"`
	Time   time.Time `json:"time"`
	Action string    `json:"action"`
	// in the order they moved
	Files []process.UndoMove `json:"files"`
	// the folders the run made
	Folders []string `json:"folders,omitempty"`
	// an undo put back some files and left these: when
	Undone time.Time `json:"undone,omitzero"`
}

// organizeRuns keeps the runs of Organize, the newest first, in the data folder.
type organizeRuns struct {
	mutex  sync.Mutex
	path   string
	loaded bool
	runs   []organizeRun
}

func (web *Web) organizeRuns() *organizeRuns {
	web.undoRuns.mutex.Lock()
	if web.undoRuns.path == "" {
		web.undoRuns.path = filepath.Join(web.dataFolder, ORGANIZE_RUNS_FILENAME)
	}
	web.undoRuns.mutex.Unlock()
	return &web.undoRuns
}

// load reads the runs kept, once; the mutex is held.
func (o *organizeRuns) load() {
	if o.loaded {
		return
	}
	o.loaded = true
	if data, ok := durable.ReadStore(o.path); ok {
		var runs []organizeRun
		if durable.ParseStore(o.path, data, &runs) == nil {
			o.runs = runs
		}
	}
}

// save writes the runs kept; the mutex is held.
func (o *organizeRuns) save() error {
	data, err := json.Marshal(o.runs)
	if err != nil {
		return err
	}
	return durable.WriteFile(o.path, data)
}

// list gives the runs kept, the newest first.
func (o *organizeRuns) list() []organizeRun {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.load()
	return append([]organizeRun{}, o.runs...)
}

// get gives a run kept by its id.
func (o *organizeRuns) get(id int64) (organizeRun, bool) {
	for _, run := range o.list() {
		if run.Id == id {
			return run, true
		}
	}
	return organizeRun{}, false
}

// add keeps what a run of Organize moved and renamed, and the folders it made; the oldest runs
// past organizeRunsKept go. A run that moved nothing is not kept.
func (o *organizeRuns) add(action string, operations []process.Operation) error {
	run := organizeRun{Time: time.Now(), Action: action, Files: []process.UndoMove{}}
	for _, op := range operations {
		if op.Error != "" {
			continue
		}
		switch op.Kind {
		case process.OP_MOVE:
			if op.From == "" || op.To == "" || op.From == op.To {
				continue
			}
			info, err := os.Lstat(op.To)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			run.Files = append(run.Files, process.UndoMove{From: op.From, To: op.To, Size: info.Size(), ModTime: info.ModTime().UnixNano()})
		case process.OP_MKDIR:
			run.Folders = append(run.Folders, process.MadeFolders(op)...)
		}
	}
	if len(run.Files) == 0 {
		return nil
	}
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.load()
	run.Id = run.Time.UnixNano()
	if len(o.runs) > 0 && run.Id <= o.runs[0].Id {
		run.Id = o.runs[0].Id + 1
	}
	o.runs = append([]organizeRun{run}, o.runs...)
	if len(o.runs) > organizeRunsKept {
		o.runs = o.runs[:organizeRunsKept]
	}
	return o.save()
}

// undone keeps of a run only the files left, which can be tried again, and the folders not
// deleted; a run with none left goes.
func (o *organizeRuns) undone(id int64, left []process.UndoMove, deleted []string) error {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.load()
	gone := map[string]bool{}
	for _, folder := range deleted {
		gone[folder] = true
	}
	for i, run := range o.runs {
		if run.Id != id {
			continue
		}
		if len(left) == 0 {
			o.runs = append(o.runs[:i:i], o.runs[i+1:]...)
		} else {
			folders := []string{}
			for _, folder := range run.Folders {
				if !gone[filepath.Clean(folder)] {
					folders = append(folders, folder)
				}
			}
			o.runs[i].Files, o.runs[i].Folders, o.runs[i].Undone = left, folders, time.Now()
		}
		return o.save()
	}
	return nil
}

// UndoRunRow is a run of Organize in the list of the page.
type UndoRunRow struct {
	Id     int64
	Time   time.Time
	Files  int
	Action string
	Undone bool
}

// undoRows are the runs kept, for the page.
func (web *Web) undoRows() []UndoRunRow {
	rows := []UndoRunRow{}
	for _, run := range web.organizeRuns().list() {
		rows = append(rows, UndoRunRow{Id: run.Id, Time: run.Time, Files: len(run.Files), Action: run.Action, Undone: !run.Undone.IsZero()})
	}
	return rows
}

// UndoFile is a file of an undo in its window, with how it stands.
type UndoFile struct {
	Path  string `json:"path"`
	State string `json:"state"`
	// what is said of it
	Text string `json:"text"`
}

// UndoCheckResponse is what an undo would do, looked at without changing anything.
type UndoCheckResponse struct {
	Id      int64      `json:"id"`
	Title   string     `json:"title"`
	Back    int        `json:"back"`
	Skipped int        `json:"skipped"`
	Gone    int        `json:"gone"`
	Files   []UndoFile `json:"files"`
	// past undoFilesShown, how many are not listed
	More int `json:"more"`
	// the chips and the button, in the language of the page
	BackText    string `json:"backText"`
	SkippedText string `json:"skippedText"`
	GoneText    string `json:"goneText"`
	ButtonText  string `json:"buttonText"`
}

// undoText is what is said of a file of an undo.
func undoText(lang string, libraryFolder string, move process.UndoMove, state string, err error) string {
	switch state {
	case process.UNDO_BACK:
		return i18n.Translatef(lang, "← goes back to %v", displayPath(libraryFolder, move.From))
	case process.UNDO_DONE:
		return i18n.Translatef(lang, "← already back at %v", displayPath(libraryFolder, move.From))
	case process.UNDO_TAKEN:
		return i18n.Translatef(lang, undoReasons[state], displayPath(libraryFolder, move.From))
	case process.UNDO_FAILED:
		if err != nil {
			return i18n.Translate(lang, "Could not be put back:") + " " + err.Error()
		}
	}
	return i18n.Translate(lang, undoReasons[state])
}

// checkUndo looks at each file of a run, the last one moved first, as the undo puts them back.
func (web *Web) checkUndo(run organizeRun, lang string) UndoCheckResponse {
	libraryFolder := settings.ReadSettings(web.dataFolder).Folder
	response := UndoCheckResponse{Id: run.Id, Files: []UndoFile{},
		Title: i18n.Translatef(lang, "Undo the organize of %v", i18n.FormatDateTime(lang, run.Time))}
	states := process.CheckUndoAll(run.Files)
	for i := len(run.Files) - 1; i >= 0; i-- {
		move, state := run.Files[i], states[i]
		switch {
		case process.UndoReturns(state):
			response.Back++
		case state == process.UNDO_GONE:
			response.Gone++
		default:
			response.Skipped++
		}
		if len(response.Files) < undoFilesShown {
			response.Files = append(response.Files, UndoFile{Path: displayPath(libraryFolder, move.To), State: state, Text: undoText(lang, libraryFolder, move, state, nil)})
		} else {
			response.More++
		}
	}
	response.BackText = i18n.Translatef(lang, "%v go back", response.Back)
	response.SkippedText = i18n.Translatef(lang, "%v skipped", response.Skipped)
	response.GoneText = i18n.Translatef(lang, "%v no longer there", response.Gone)
	response.ButtonText = i18n.Translatef(lang, "Put back %v files", response.Back)
	return response
}

// UndoResponse is how an undo went.
type UndoResponse struct {
	Back    int        `json:"back"`
	Skipped []UndoFile `json:"skipped"`
	Folders int        `json:"folders"`
	Message string     `json:"message"`
}

// runUndo puts back the files of a run of Organize as a task, the last one moved first. The
// caller holds the synchronization (holdScan), and rescans after.
func (web *Web) runUndo(id int64, lang string) (response UndoResponse, err error) {
	run, ok := web.organizeRuns().get(id)
	if !ok {
		return response, errUndoRunGone
	}
	// files are moved: no Organize, compression, conversion, pack or check may run meanwhile
	if !web.organizing.CompareAndSwap(false, true) {
		return response, errOrganizeBusy
	}
	defer func() {
		web.organizing.Store(false)
		go web.runQueue()
	}()
	if web.compressionRunning() || web.measuring.Load() {
		return response, errOrganizeBusy
	}
	defer awake.Keep().Release()
	taskId := web.startTask(tasklog.TASK_UNDO_ORGANIZE, tasklog.TRIGGER_MANUAL)
	var failure *tasklog.Note
	defer func() {
		if recovered := recover(); recovered != nil {
			failure = web.crashed(tasklog.TASK_UNDO_ORGANIZE, recovered)
			err = errors.New(failure.Detail)
		}
		web.finishTask(taskId, failure)
	}()

	appSettings := settings.ReadSettings(web.dataFolder)
	libraryFolder := appSettings.Folder
	// the notes of the task are in the language of the app, as the ones of Organize
	taskLang := appSettings.Language
	if !i18n.IsSupportedLanguage(taskLang) {
		taskLang = i18n.DEFAULT_LANGUAGE
	}
	response.Skipped = []UndoFile{}
	left := []process.UndoMove{}
	moved := []process.Operation{}
	told := 0
	total := len(run.Files) + 1
	for i := len(run.Files) - 1; i >= 0; i-- {
		move := run.Files[i]
		step := len(run.Files) - i
		web.UpdateProgress(step, total, filepath.Base(move.To))
		state, putErr := process.PutBack(move, func(written int64, size int64) {
			web.UpdateProgress(step, total, process.CopyingMessage(filepath.Base(move.To), written, size))
		})
		if process.UndoReturns(state) {
			response.Back++
			if state == process.UNDO_BACK {
				moved = append(moved, process.Operation{Kind: process.OP_MOVE, From: move.To, To: move.From})
				web.sugarLogger.Infof("Undo of Organize: %v -> %v", move.To, move.From)
			}
			continue
		}
		if state != process.UNDO_GONE {
			// to try again once what kept it is sorted out; saved in the order they moved
			left = append([]process.UndoMove{move}, left...)
		}
		file := UndoFile{Path: displayPath(libraryFolder, move.To), State: state, Text: undoText(lang, libraryFolder, move, state, putErr)}
		response.Skipped = append(response.Skipped, file)
		if told < organizeNotesShown {
			told++
			web.taskLog().Warn(taskId, NOTE_UNDO_FILE_KEPT, file.Path+": "+undoText(taskLang, libraryFolder, move, state, putErr))
		}
	}
	if untold := len(response.Skipped) - told; untold > 0 {
		web.taskLog().Warn(taskId, NOTE_UNDO_MORE, strconv.Itoa(untold))
	}
	web.UpdateProgress(total, total, "Deleting empty folders...")
	deleted := process.UndoFolders(run.Folders)
	response.Folders = len(deleted)
	// the tasks and the records of the files follow them to where they are now
	web.verifications().follow(moved)
	web.uncompressible().follow(moved)
	web.moves.record(moved)
	web.followKeptChoices(moved)
	if err := web.organizeRuns().undone(run.Id, left, deleted); err != nil {
		web.sugarLogger.Warnf("Failed to keep what is left of the organize: %v", err)
	}
	web.taskLog().SetResult(taskId, 0, response.Back)
	response.Message = i18n.Translatef(lang, "%v files put back, %v skipped.", response.Back, len(response.Skipped))
	return response, nil
}

func (web *Web) handleOrganizeUndo() {
	runId := func(r *http.Request) (organizeRun, bool) {
		id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
		if err != nil {
			return organizeRun{}, false
		}
		return web.organizeRuns().get(id)
	}
	web.router.HandleFunc("/organize/undo/check", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		run, ok := runId(r)
		if !ok {
			writeGlobalError(w, http.StatusNotFound, lang, string(errUndoRunGone))
			return
		}
		writeJSON(w, http.StatusOK, web.checkUndo(run, lang))
	}).Methods("POST")

	web.router.HandleFunc("/organize/undo/run", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		run, ok := runId(r)
		if !ok {
			writeGlobalError(w, http.StatusNotFound, lang, string(errUndoRunGone))
			return
		}
		// never move files while the library is being scanned
		if !web.holdScan() {
			writeGlobalError(w, http.StatusConflict, lang, "A synchronization is running, try again when it has finished.")
			return
		}
		response, err := web.runUndo(run.Id, lang)
		if err != nil {
			web.releaseScan("")
			writeGlobalError(w, http.StatusConflict, lang, err.Error())
			return
		}
		// the library is read again, as after Organize
		web.releaseScan(tasklog.TRIGGER_ORGANIZE)
		// for the activity, which reads the same form once the request is answered
		if r.Form != nil {
			r.Form.Set(undoActivityFiles, strconv.Itoa(response.Back))
		}
		writeJSON(w, http.StatusOK, response)
	}).Methods("POST")
}
