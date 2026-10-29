package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The tasks on files run one at a time. One asked for while another works waits in a queue,
// instead of being refused: compress these, then make the packs, then check the files. The
// queue is kept on disk, so it goes on after a restart; the first one starts as soon as the
// files are free (a task on files, Organize or a measure of the server ended), and each one
// can be taken out before it starts.

// QueuedTask is a task waiting for its turn, with what it needs to start.
type QueuedTask struct {
	Id    int64     `json:"id"`
	Kind  string    `json:"kind"`
	Added time.Time `json:"added"`
	// the files, as the library had them; nil for a check of the whole library
	Paths []string `json:"paths,omitempty"`
	// the level of a compression
	Level string `json:"level,omitempty"`
	// the originals are deleted once their new files are checked
	Delete bool `json:"delete,omitempty"`
	// the names of the packs, by the path of the base file of their game
	Names map[string]string `json:"names,omitempty"`
	// a check: the files checked less than this ago are left out (see startVerificationOf)
	Recheck time.Duration `json:"recheck,omitempty"`
	// a check of the signatures only (see startSignatureCheck)
	Signatures bool `json:"signatures,omitempty"`
}

type taskQueue struct {
	mutex  sync.Mutex
	path   string
	items  []QueuedTask
	nextId int64
	// when the run of the queue began: the task working when the first one was queued, or the
	// first one started; zero while the queue is not used. Its end is told by the notifications.
	started time.Time
	// the first task waits for the scan running to end (see readsLibrary)
	waitingForScan bool
}

func (web *Web) queue() *taskQueue {
	web.queueOnce.Do(func() {
		web.tasksQueue = loadTaskQueue(filepath.Join(web.dataFolder, "queue.json"))
	})
	return web.tasksQueue
}

// queueFile is the queue on disk, with the last ID given: a page drawn before a restart must
// never take out a task that got the ID of one it showed.
type queueFile struct {
	LastId  int64        `json:"lastId"`
	Items   []QueuedTask `json:"items"`
	Started time.Time    `json:"started,omitempty"`
}

func loadTaskQueue(path string) *taskQueue {
	queue := &taskQueue{path: path}
	if data, err := os.ReadFile(path); err == nil {
		saved := queueFile{}
		if parseStore(path, data, &saved) == nil {
			queue.items, queue.nextId, queue.started = saved.Items, saved.LastId, saved.Started
		}
	}
	for _, item := range queue.items {
		queue.nextId = max(queue.nextId, item.Id)
	}
	return queue
}

// save keeps the queue; the caller holds the mutex.
func (q *taskQueue) save() {
	data, err := json.Marshal(queueFile{LastId: q.nextId, Items: q.items, Started: q.started})
	if err == nil {
		writeFileAtomic(q.path, data)
	}
}

// add puts a task at the end of the queue and gives it its ID.
func (q *taskQueue) add(task QueuedTask) int64 {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	q.nextId++
	task.Id = q.nextId
	task.Added = time.Now()
	q.items = append(q.items, task)
	q.save()
	return task.Id
}

// position is the place of a task in the queue, from 1; 0 when it is not in it (it started).
func (q *taskQueue) position(id int64) int {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	for i, item := range q.items {
		if item.Id == id {
			return i + 1
		}
	}
	return 0
}

// remove takes tasks out of the queue and tells how many were in it.
func (q *taskQueue) remove(ids map[int64]bool) int {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	kept := q.items[:0]
	removed := 0
	for _, item := range q.items {
		if ids[item.Id] {
			removed++
			continue
		}
		kept = append(kept, item)
	}
	q.items = kept
	if removed > 0 {
		q.save()
	}
	return removed
}

// move puts a task one place earlier (up) or later in the queue, and tells whether it moved.
func (q *taskQueue) move(id int64, up bool) bool {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	for i, item := range q.items {
		if item.Id != id {
			continue
		}
		other := i + 1
		if up {
			other = i - 1
		}
		if other < 0 || other >= len(q.items) {
			return false
		}
		q.items[i], q.items[other] = q.items[other], q.items[i]
		q.save()
		return true
	}
	return false
}

// begin marks the start of a run of the queue, when it is not marked yet.
func (q *taskQueue) begin(at time.Time) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if q.started.IsZero() {
		q.started = at
		q.save()
	}
}

// follow puts the files of the tasks waiting where resolve says they are now: before the
// moves of Organize are forgotten.
func (q *taskQueue) follow(resolve func(string) string) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	changed := false
	for i, item := range q.items {
		for j, path := range item.Paths {
			if now := resolve(path); now != path {
				q.items[i].Paths[j], changed = now, true
			}
		}
		if len(item.Names) > 0 {
			names := make(map[string]string, len(item.Names))
			for path, name := range item.Names {
				now := resolve(path)
				changed = changed || now != path
				names[now] = name
			}
			q.items[i].Names = names
		}
	}
	if changed {
		q.save()
	}
}

// list is the queue in its order.
func (q *taskQueue) list() []QueuedTask {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	return append([]QueuedTask(nil), q.items...)
}

// startQueued starts a task of the queue, as the user asked for it; false when the files are
// busy.
func (web *Web) startQueued(task QueuedTask) bool {
	switch task.Kind {
	case TASK_COMPRESS:
		return web.startCompressionBy(task.Paths, task.Level, task.Delete, TRIGGER_MANUAL)
	case TASK_DECOMPRESS:
		return web.startDecompressionBy(task.Paths, task.Delete, TRIGGER_MANUAL)
	case TASK_CONVERT:
		return web.startConversionBy(task.Paths, task.Delete, TRIGGER_MANUAL)
	case TASK_PACK:
		return web.startPacks(task.Paths, task.Names, task.Delete, TRIGGER_MANUAL)
	case TASK_SPLIT:
		return web.startSplitBy(task.Paths, task.Delete, TRIGGER_MANUAL)
	case TASK_VERIFY:
		if task.Signatures {
			return web.startSignatureCheck(task.Recheck == verifyRecheckAll, TRIGGER_MANUAL)
		}
		return web.startVerificationOf(task.Paths, task.Recheck, TRIGGER_MANUAL)
	}
	// a kind this version does not know: dropped, not kept waiting for good
	return true
}

// readsLibrary tells a task that works on the library as the last scan found it: a pack takes
// the files of its game from it, a check of the whole library its list of files. After a task
// that changed the files, it waits for the scan that follows, or it would work on the files of
// before (an NSP the compression deleted for its NSZ) or on a library found only in part.
func readsLibrary(task QueuedTask) bool {
	return task.Kind == TASK_PACK || task.Kind == TASK_VERIFY && task.Paths == nil
}

// queueAfterScan starts the task of the queue that waited for the scan that just ended.
func (web *Web) queueAfterScan() {
	queue := web.queue()
	queue.mutex.Lock()
	waiting := queue.waitingForScan && !web.rescanPending.Load()
	if waiting {
		queue.waitingForScan = false
	}
	queue.mutex.Unlock()
	if waiting {
		go web.runQueue()
	}
}

// runQueue starts the first task of the queue when the files are free. It is called when a
// task on files, Organize or a measure ends, and every minute by the scheduler, so the queue
// never stays still.
func (web *Web) runQueue() {
	queue := web.queue()
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	if len(queue.items) == 0 {
		// the last task of a run of the queue ended: told once, with what each task did
		if !queue.started.IsZero() && !web.filesBusy() {
			since := queue.started
			queue.started = time.Time{}
			queue.save()
			go web.notifyQueueDone(since)
		}
		return
	}
	if readsLibrary(queue.items[0]) && (web.state.IsSynchronizing() || web.rescanPending.Load()) {
		queue.waitingForScan = true
		return
	}
	now := time.Now()
	if !web.startQueued(queue.items[0]) {
		return
	}
	if queue.started.IsZero() {
		queue.started = now
	}
	queue.items = queue.items[1:]
	queue.save()
	web.taskLog().Touch()
}

// queueKinds are the kinds of tasks the queue runs.
var queueKinds = map[string]bool{TASK_COMPRESS: true, TASK_DECOMPRESS: true, TASK_CONVERT: true, TASK_PACK: true, TASK_SPLIT: true, TASK_VERIFY: true}

// notifyQueueDone tells by the channels of the notifications that the queue is done, with each
// task on files that ran since it began and how it ended.
func (web *Web) notifyQueueDone(since time.Time) {
	if isDemoMode() {
		return
	}
	settingsObj := settings.ReadSettings(web.dataFolder)
	options := settingsObj.Notifications
	if !notificationsConfigured(options) || options.QueueDoneQuiet {
		return
	}
	lang := settingsObj.Language
	if !isSupportedLanguage(lang) {
		lang = DEFAULT_LANGUAGE
	}
	tasks := []Task{}
	for _, task := range web.taskLog().Snapshot() {
		if queueKinds[task.Kind] && !task.Running() && !task.Started.Before(since) {
			tasks = append(tasks, task)
		}
	}
	if len(tasks) == 0 {
		return
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Started.Before(tasks[j].Started) })
	lines := []string{}
	items := []any{}
	for _, task := range tasks {
		kind := translate(lang, taskKindNames[task.Kind])
		if task.Error != nil {
			lines = append(lines, "✗ "+translatef(lang, "%v failed: %v", kind, translate(lang, task.Error.Text)))
		} else {
			lines = append(lines, "✓ "+translatef(lang, "%v: done", kind))
		}
		items = append(items, map[string]any{"kind": task.Kind, "id": task.Id, "status": task.Status})
	}
	if err := sendMessage(options, translate(lang, "The queue of tasks is done"), strings.Join(lines, "\n"), items); err != nil {
		web.sugarLogger.Warnf("Failed to send the notification of the queue: %v", err)
	}
}

// startOrQueue starts a task now when nothing works on the files, or puts it at the end of
// the queue, and answers which: {"started": files} or {"queued": its place}.
func (web *Web) startOrQueue(w http.ResponseWriter, task QueuedTask) {
	id := web.queue().add(task)
	web.runQueue()
	if position := web.queue().position(id); position > 0 {
		// the run of the queue begins with the task working now
		begun := time.Now()
		for _, running := range web.taskLog().Snapshot() {
			if running.Running() && running.Started.Before(begun) {
				begun = running.Started
			}
		}
		web.queue().begin(begun)
		web.taskLog().Touch()
		writeJSON(w, http.StatusAccepted, map[string]any{"queued": position})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": len(task.Paths)})
}

// QueuedView is a task of the queue as the page of the tasks shows it.
type QueuedView struct {
	QueuedTask
	Position int
	// the files it works on; 0 for a check of the whole library
	Files int
}

func (web *Web) queuedViews() []QueuedView {
	views := []QueuedView{}
	for i, item := range web.queue().list() {
		views = append(views, QueuedView{QueuedTask: item, Position: i + 1, Files: len(item.Paths)})
	}
	return views
}

func (web *Web) handleQueue() {
	// the tasks marked on the page of the tasks, taken out of the queue before they start
	web.router.HandleFunc("/api/queue/remove", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		ids := map[int64]bool{}
		for _, value := range r.Form["id"] {
			for _, part := range strings.Split(value, ",") {
				if id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil {
					ids[id] = true
				}
			}
		}
		removed := web.queue().remove(ids)
		web.taskLog().Touch()
		writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
	}).Methods("POST")

	// a task one place earlier or later in the queue
	web.router.HandleFunc("/api/queue/move", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		moved := web.queue().move(id, r.FormValue("dir") == "up")
		web.taskLog().Touch()
		writeJSON(w, http.StatusOK, map[string]any{"moved": moved})
	}).Methods("POST")
}
