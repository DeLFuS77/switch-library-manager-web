package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
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
}

type taskQueue struct {
	mutex  sync.Mutex
	path   string
	items  []QueuedTask
	nextId int64
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
	LastId int64        `json:"lastId"`
	Items  []QueuedTask `json:"items"`
}

func loadTaskQueue(path string) *taskQueue {
	queue := &taskQueue{path: path}
	if data, err := os.ReadFile(path); err == nil {
		saved := queueFile{}
		if parseStore(path, data, &saved) == nil {
			queue.items, queue.nextId = saved.Items, saved.LastId
		}
	}
	for _, item := range queue.items {
		queue.nextId = max(queue.nextId, item.Id)
	}
	return queue
}

// save keeps the queue; the caller holds the mutex.
func (q *taskQueue) save() {
	data, err := json.Marshal(queueFile{LastId: q.nextId, Items: q.items})
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
		return web.startVerificationOf(task.Paths, task.Recheck, TRIGGER_MANUAL)
	}
	// a kind this version does not know: dropped, not kept waiting for good
	return true
}

// runQueue starts the first task of the queue when the files are free. It is called when a
// task on files, Organize or a measure ends, and every minute by the scheduler, so the queue
// never stays still.
func (web *Web) runQueue() {
	queue := web.queue()
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	if len(queue.items) == 0 || !web.startQueued(queue.items[0]) {
		return
	}
	queue.items = queue.items[1:]
	queue.save()
	web.taskLog().Touch()
}

// startOrQueue starts a task now when nothing works on the files, or puts it at the end of
// the queue, and answers which: {"started": files} or {"queued": its place}.
func (web *Web) startOrQueue(w http.ResponseWriter, task QueuedTask) {
	id := web.queue().add(task)
	web.runQueue()
	if position := web.queue().position(id); position > 0 {
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
}
