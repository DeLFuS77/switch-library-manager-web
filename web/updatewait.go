package web

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
)

// The update restarts the app only once every work has ended: Update now, Go back and the
// install at night wait while a task runs or waits in the queue of Tasks, files wait for the
// automations, the server is measured, a game is uploaded, or the app works by itself (a scan,
// what follows it, the covers, IGDB). Once none goes on, the update holds the files and the
// scans (see holdFiles): from then on no task starts, the tasks asked meanwhile wait in the
// queue for the new version, and what changes the files by a request (an upload, the
// quarantine, a restore of a backup) is refused until the app has started again.

// busyWork is the work an update waits for.
type busyWork struct {
	// the tasks running (paused ones too: one paused waits for the user), waiting in the queue,
	// the files waiting for the automations, a measure of the server
	Tasks, Paused int
	// work the app does by itself, without a task of its own to show
	Background bool
}

func (b busyWork) idle() bool {
	return b.Tasks == 0 && !b.Background
}

// pausedOnly tells the update waits for a task paused alone, which waits for the user.
func (b busyWork) pausedOnly() bool {
	return b.Tasks == 1 && b.Paused == 1
}

// work tells the work going on. While the update holds the files and the scans itself (held),
// those holds are not work.
func (web *Web) work(held bool) busyWork {
	b := busyWork{Tasks: web.taskLog().RunningCount() + len(web.queue().list())}
	if web.automationPending() > 0 {
		b.Tasks++
	}
	if web.measuring.Load() {
		b.Tasks++
	}
	web.compressor.mutex.Lock()
	// a task on the files still ending: its task is finished, its files not released yet
	if web.compressor.cancel != nil {
		b.Background = true
		if web.compressor.paused {
			b.Paused = 1
		}
	}
	web.compressor.mutex.Unlock()
	if !held && (web.organizing.Load() || web.state.IsSynchronizing()) {
		b.Background = true
	}
	web.covers.mutex.Lock()
	covers := web.covers.running
	web.covers.mutex.Unlock()
	web.cheatsFile.mutex.Lock()
	cheats := web.cheatsFile.fetching
	web.cheatsFile.mutex.Unlock()
	if covers || cheats || web.rescanPending.Load() || web.backgroundWork.Load() > 0 || web.timeToBeatRunning.Load() || web.uncompressible().looking.Load() || web.uploads.writing() {
		b.Background = true
	}
	return b
}

var (
	errUpdateBusy       = errorString("Tasks are still running: the update starts once they have finished.")
	errUpdateCancelled  = errorString("The update was cancelled.")
	errUpdateInstalling = errorString("An update is being installed: try again once the app has started again.")
)

// holdFiles keeps the files of the library and the scans still while the program is replaced:
// nothing may run, nor start, meanwhile. It is taken first and the work looked at then, so no
// task can start in between; with work going on, it is given back (errUpdateBusy). The function
// given ends it.
func (web *Web) holdFiles() (func(), error) {
	if !installing.CompareAndSwap(false, true) {
		return nil, errUpdateBusy
	}
	if !web.organizing.CompareAndSwap(false, true) {
		installing.Store(false)
		return nil, errUpdateBusy
	}
	// the scans asked meanwhile wait: for the end of the update, or for the new version
	if !web.state.startSync() {
		web.organizing.Store(false)
		installing.Store(false)
		return nil, errUpdateBusy
	}
	web.updateHold.Store(true)
	end := func() {
		web.updateHold.Store(false)
		web.organizing.Store(false)
		installing.Store(false)
		web.releaseScan("")
	}
	if !web.work(true).idle() {
		end()
		return nil, errUpdateBusy
	}
	return end, nil
}

// pendingUpdate is the update asked that waits for the work to end, or is being installed.
type pendingUpdate struct {
	mutex   sync.Mutex
	waiting bool
	cancel  chan struct{}
	// why the last update asked did not happen, for the page that waits
	failure string
	// the work it waits for, for the page
	work atomic.Pointer[busyWork]
}

// how often an update that waits looks at the work, and how many times in a row it must find
// none: the end of a task starts the next one, or a scan, a moment later
var (
	updateWaitEvery = 2 * time.Second
	updateSettle    = 2
)

// requestUpdate does the update asked (action: install it, or go back) once no work goes on,
// in the background; the page follows it by /update/progress. It tells the work it waits for.
// An update asked already is not asked twice.
func (web *Web) requestUpdate(action func() error) busyWork {
	work := web.work(false)
	if cancel, asked := web.askUpdate(); asked {
		go web.updateWhenIdle(action, cancel, time.Time{})
	}
	return work
}

// askUpdate marks an update as asked, with what cancels its wait; false when one was asked
// already.
func (web *Web) askUpdate() (chan struct{}, bool) {
	web.updateWait.mutex.Lock()
	defer web.updateWait.mutex.Unlock()
	if web.updateWait.waiting {
		return nil, false
	}
	web.updateWait.waiting = true
	web.updateWait.failure = ""
	web.updateWait.cancel = make(chan struct{})
	return web.updateWait.cancel, true
}

// cancelUpdate stops waiting for the work to end; an update being downloaded goes on.
func (web *Web) cancelUpdate() bool {
	web.updateWait.mutex.Lock()
	defer web.updateWait.mutex.Unlock()
	if !web.updateWait.waiting || web.updateWait.cancel == nil || installing.Load() {
		return false
	}
	close(web.updateWait.cancel)
	web.updateWait.cancel = nil
	return true
}

// updateWhenIdle waits for the work to end, then does the update; until, when given, is when it
// gives up (the end of the night). The update that finds work again once it holds the files goes
// back to waiting. What kept it from happening is kept for the page.
func (web *Web) updateWhenIdle(action func() error, cancel <-chan struct{}, until time.Time) error {
	err := web.waitAndUpdate(action, cancel, until)
	web.updateWait.mutex.Lock()
	web.updateWait.waiting = false
	web.updateWait.cancel = nil
	if err != nil && err != errUpdateCancelled {
		web.updateWait.failure = err.Error()
	}
	web.updateWait.mutex.Unlock()
	web.updateWait.work.Store(nil)
	if err != nil {
		updateProgress.step.Store("")
	}
	return err
}

func (web *Web) waitAndUpdate(action func() error, cancel <-chan struct{}, until time.Time) error {
	quiet := 0
	for {
		work := web.work(false)
		web.updateWait.work.Store(&work)
		if work.idle() {
			quiet++
		} else {
			quiet = 0
		}
		if quiet >= updateSettle {
			err := action()
			if err != errUpdateBusy {
				return err
			}
			quiet = 0
		} else {
			updateProgress.step.Store(UPDATE_STEP_WAIT)
		}
		if !until.IsZero() && !time.Now().Before(until) {
			return errUpdateBusy
		}
		select {
		case <-cancel:
			return errUpdateCancelled
		case <-time.After(updateWaitEvery):
		}
	}
}

// heldForUpdate refuses what changes the files by a request while an update holds them: an
// upload, the quarantine, a restore of a backup. The tasks asked meanwhile wait in the queue.
func (web *Web) heldForUpdate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if web.updateHold.Load() && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && heldPath(r.URL.Path) {
			writeGlobalError(w, http.StatusServiceUnavailable, web.requestLanguage(r), errUpdateInstalling.Error())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// heldPath tells the requests that change the files and start no task of the queue.
func heldPath(path string) bool {
	for _, prefix := range []string{"/uploads/start", "/uploads/chunk", "/uploads/finish", "/quarantine/", "/backup/restore", "/backup/auto"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// updateWaitText is what the page says while the update waits, in the language of the page.
func updateWaitText(lang string, work busyWork) string {
	switch {
	case work.pausedOnly():
		return i18n.Translate(lang, "Waiting for the paused task: resume it or cancel it, and the update starts after it.")
	case work.Tasks == 1:
		return i18n.Translate(lang, "Waiting for 1 task to finish: the update starts after it.")
	case work.Tasks > 1:
		return i18n.Translatef(lang, "Waiting for %v tasks to finish: the update starts after them.", work.Tasks)
	}
	return i18n.Translate(lang, "Waiting for the work in the background to finish: the update starts after it.")
}

// how long the server waits, as it closes for an update, for the requests going on to end
var serverCloseWait = 30 * time.Second

// closingPages is closed as the server closes for an update: the pages that stream end then.
func (web *Web) closingPages() <-chan struct{} {
	web.closingOnce.Do(func() { web.closing = make(chan struct{}) })
	return web.closing
}

// closeServer stops taking requests and lets the ones going on end (a save sent to the vault, a
// change of the settings), serverCloseWait at most; the pages that stream end at once.
func (web *Web) closeServer() {
	web.closingPages()
	web.closedOnce.Do(func() { close(web.closing) })
	if web.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), serverCloseWait)
	defer cancel()
	if err := web.server.Shutdown(ctx); err != nil {
		web.server.Close()
	}
}
