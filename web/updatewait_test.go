package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// quickUpdateWait makes the update look at the work often, for the tests.
func quickUpdateWait(t *testing.T) {
	t.Helper()
	every := updateWaitEvery
	updateWaitEvery = 5 * time.Millisecond
	t.Cleanup(func() { updateWaitEvery = every })
}

// updateEnded waits for the update asked to stop waiting: installed, failed or cancelled.
func updateEnded(t *testing.T, web *Web) {
	t.Helper()
	eventually(t, func() bool {
		web.updateWait.mutex.Lock()
		defer web.updateWait.mutex.Unlock()
		return !web.updateWait.waiting
	}, "the update still waits")
}

// readyUpdate is a web with a signed new version to install, and its program of now.
func readyUpdate(t *testing.T) (*Web, string, *atomic.Bool) {
	t.Helper()
	archive := tarArchive(t, map[string]string{"switch-library-manager-web/switch-library-manager-web": "new"})
	sum := sha256.Sum256(archive)
	return updateTestWeb(t, archive, hex.EncodeToString(sum[:]))
}

// blockingWork is each kind of work an update waits for: how it begins, and how it ends.
var blockingWork = []struct {
	kind  string
	begin func(web *Web) (end func())
}{
	{"a task running (Tasks)", func(web *Web) func() {
		id := web.taskLog().Start(tasklog.TASK_VERIFY, tasklog.TRIGGER_MANUAL)
		return func() { web.taskLog().Finish(id, nil) }
	}},
	{"a task waiting in the queue", func(web *Web) func() {
		id := web.queue().add(QueuedTask{Kind: tasklog.TASK_COMPRESS, Paths: []string{"a.nsp"}})
		return func() { web.queue().remove(map[int64]bool{id: true}) }
	}},
	{"files waiting for the automations", func(web *Web) func() {
		queue := web.automation()
		queue.mutex.Lock()
		queue.Paths = []string{"a.nsp"}
		queue.mutex.Unlock()
		return func() {
			queue.mutex.Lock()
			queue.Paths = nil
			queue.mutex.Unlock()
		}
	}},
	{"a measure of the server", func(web *Web) func() {
		web.measuring.Store(true)
		return func() { web.measuring.Store(false) }
	}},
	{"a task on the files, paused", func(web *Web) func() {
		web.compressor.mutex.Lock()
		web.compressor.cancel, web.compressor.paused = func() {}, true
		web.compressor.mutex.Unlock()
		return func() {
			web.compressor.mutex.Lock()
			web.compressor.cancel, web.compressor.paused = nil, false
			web.compressor.mutex.Unlock()
		}
	}},
	{"organizing", func(web *Web) func() {
		web.organizing.Store(true)
		return func() { web.organizing.Store(false) }
	}},
	{"a scan", func(web *Web) func() {
		web.state.startSync()
		return web.state.endSync
	}},
	{"the work after a scan", func(web *Web) func() {
		web.backgroundWork.Add(1)
		return func() { web.backgroundWork.Add(-1) }
	}},
	{"the covers", func(web *Web) func() {
		web.covers.mutex.Lock()
		web.covers.running = true
		web.covers.mutex.Unlock()
		return func() {
			web.covers.mutex.Lock()
			web.covers.running = false
			web.covers.mutex.Unlock()
		}
	}},
	{"IGDB", func(web *Web) func() {
		web.timeToBeatRunning.Store(true)
		return func() { web.timeToBeatRunning.Store(false) }
	}},
	{"the reasons of the files that do not compress", func(web *Web) func() {
		web.uncompressible().looking.Store(true)
		return func() { web.uncompressible().looking.Store(false) }
	}},
	{"the cheats", func(web *Web) func() {
		web.cheatsFile.mutex.Lock()
		web.cheatsFile.fetching = true
		web.cheatsFile.mutex.Unlock()
		return func() {
			web.cheatsFile.mutex.Lock()
			web.cheatsFile.fetching = false
			web.cheatsFile.mutex.Unlock()
		}
	}},
	{"an upload", func(web *Web) func() {
		web.uploads.lastWrite.Store(time.Now().UnixNano())
		return func() { web.uploads.lastWrite.Store(0) }
	}},
}

// No kind of work lets the update restart the app: it waits, and it installs once the work has
// ended (#B: every process ends first).
func TestUpdateWaitsForEveryWork(t *testing.T) {
	quickUpdateWait(t)
	for _, work := range blockingWork {
		t.Run(work.kind, func(t *testing.T) {
			web, program, started := readyUpdate(t)
			end := work.begin(web)
			if err := web.installUpdate(); err != errUpdateBusy {
				t.Fatalf("installed during %s: %v", work.kind, err)
			}
			if web.updateHold.Load() || web.organizing.Load() && work.kind != "organizing" || installing.Load() {
				t.Fatal("the update did not give back the files it held")
			}
			if waiting := web.requestUpdate(web.installUpdate); waiting.idle() {
				t.Fatalf("%s is not work", work.kind)
			}
			time.Sleep(20 * updateWaitEvery)
			if data, _ := os.ReadFile(program); string(data) != "old" || started.Load() {
				t.Fatalf("the app restarted during %s", work.kind)
			}
			end()
			eventually(t, func() bool { return started.Load() }, "the update did not happen once %s ended", work.kind)
			updateEnded(t, web)
			if data, _ := os.ReadFile(program); string(data) != "new" {
				t.Fatalf("the program: %q", data)
			}
		})
	}
}

// A scan asked while none could run is done before the update: the update that finds it gives
// the scans back, the scan runs, and the update installs after it.
func TestUpdateDoesTheScanAskedFirst(t *testing.T) {
	quickUpdateWait(t)
	web, _, started := readyUpdate(t)
	web.rescanPending.Store(true)
	if err := web.installUpdate(); err != errUpdateBusy {
		t.Fatalf("installed with a scan asked: %v", err)
	}
	web.requestUpdate(web.installUpdate)
	eventually(t, func() bool { return started.Load() }, "the update did not happen after the scan")
	updateEnded(t, web)
	if web.rescanPending.Load() {
		t.Fatal("the update happened before the scan asked")
	}
	scanned := false
	for _, task := range web.taskLog().Snapshot() {
		scanned = scanned || task.Kind == tasklog.TASK_SCAN
	}
	if !scanned {
		t.Fatal("the scan asked was not done")
	}
}

// The page is told what the update waits for, in its words.
func TestUpdateWaitText(t *testing.T) {
	for want, work := range map[string]busyWork{
		"Waiting for 3 tasks to finish: the update starts after them.":                         {Tasks: 3},
		"Waiting for 1 task to finish: the update starts after it.":                            {Tasks: 1},
		"Waiting for the paused task: resume it or cancel it, and the update starts after it.": {Tasks: 1, Paused: 1},
		"Waiting for the work in the background to finish: the update starts after it.":        {Background: true},
	} {
		if got := updateWaitText("en", work); got != want {
			t.Errorf("%+v: %q", work, got)
		}
	}
	if got := updateWaitText("es", busyWork{Tasks: 2}); got == updateWaitText("en", busyWork{Tasks: 2}) {
		t.Errorf("not translated: %q", got)
	}
}

// While the update holds the files, no task starts: the tasks on files wait in the queue, no scan
// starts, nor anything that changes the files by a request.
func TestNothingStartsWhileInstalling(t *testing.T) {
	web, _, _ := readyUpdate(t)
	end, err := web.holdFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !web.filesBusy() {
		t.Fatal("the files are free during the update")
	}
	web.compressor.mutex.Lock()
	taken := web.filesTaken()
	web.compressor.mutex.Unlock()
	if !taken {
		t.Fatal("a task on the files can start during the update")
	}
	if web.state.startSync() {
		t.Fatal("a scan can start during the update")
	}
	if web.organizing.CompareAndSwap(false, true) {
		t.Fatal("Organize can start during the update")
	}
	handler := web.heldForUpdate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	for _, path := range []string{"/uploads/start", "/uploads/chunk", "/quarantine/restore", "/quarantine/empty", "/backup/restore", "/backup/auto/x/restore"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Errorf("%s during the update: %d", path, recorder.Code)
		}
	}
	for _, request := range []*http.Request{httptest.NewRequest(http.MethodPost, "/settings", nil), httptest.NewRequest(http.MethodGet, "/uploads/status", nil)} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Errorf("%s %s during the update: %d", request.Method, request.URL.Path, recorder.Code)
		}
	}
	if _, err := web.holdFiles(); err != errUpdateBusy {
		t.Fatalf("a second update holds the files: %v", err)
	}
	end()
	if web.filesBusy() || web.state.IsSynchronizing() {
		t.Fatal("the files are still held after the update")
	}
}

// The update waiting for the work can be cancelled: nothing is installed.
func TestUpdateWaitCancelled(t *testing.T) {
	quickUpdateWait(t)
	web, program, started := readyUpdate(t)
	web.measuring.Store(true)
	web.requestUpdate(web.installUpdate)
	if !web.cancelUpdate() {
		t.Fatal("the wait was not cancelled")
	}
	updateEnded(t, web)
	web.measuring.Store(false)
	time.Sleep(20 * updateWaitEvery)
	if data, _ := os.ReadFile(program); string(data) != "old" || started.Load() {
		t.Fatal("a cancelled update was installed")
	}
	if web.cancelUpdate() {
		t.Fatal("an update not waiting is cancelled")
	}
}

// At night, work going on puts the update off: it waits for it a while, and else tries again at
// the next look; once no work goes on, it installs.
func TestInstallAtNightWaitsForTheWork(t *testing.T) {
	quickUpdateWait(t)
	wait := nightWaitFor
	nightWaitFor = 50 * time.Millisecond
	t.Cleanup(func() { nightWaitFor = wait })
	web, _, started := readyUpdate(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.AutoInstallUpdates = true })
	if !selfUpdatable() {
		t.Skip("the folder of the test program cannot be written")
	}
	night := time.Date(2026, 10, 27, 3, 30, 0, 0, time.Local)
	id := web.taskLog().Start(tasklog.TASK_COMPRESS, tasklog.TRIGGER_SCHEDULE)
	if web.installAtNight(night) || started.Load() {
		t.Fatal("installed at night during a task")
	}
	web.taskLog().Finish(id, nil)
	if !web.installAtNight(night.Add(15*time.Minute)) || !started.Load() {
		t.Fatal("not installed at night once the task ended")
	}
}

// The page follows the wait by /update/progress: what it waits for, then why it did not happen.
func TestUpdateProgressTellsTheWait(t *testing.T) {
	quickUpdateWait(t)
	web, _, _ := readyUpdate(t)
	web.HandleSelfUpdate()
	web.measuring.Store(true)
	web.requestUpdate(web.installUpdate)
	eventually(t, func() bool {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/update/progress", nil))
		return strings.Contains(recorder.Body.String(), "Waiting for 1 task to finish")
	}, "the page is not told the update waits")
	web.cancelUpdate()
	updateEnded(t, web)
	web.measuring.Store(false)
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/update/progress", nil))
	if strings.Contains(recorder.Body.String(), `"asked":true`) || strings.Contains(recorder.Body.String(), "error") {
		t.Fatalf("a cancelled update: %s", recorder.Body.String())
	}
}

func eventually(t *testing.T, wanted func() bool, format string, args ...any) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !wanted(); {
		if time.Now().After(deadline) {
			t.Fatalf(format, args...)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
