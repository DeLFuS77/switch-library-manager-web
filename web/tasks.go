package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// kinds of background tasks
const (
	TASK_SYNC     = "sync"
	TASK_SCAN     = "scan"
	TASK_ORGANIZE = "organize"
	TASK_CLEANUP  = "cleanup"
)

// what started a task
const (
	TRIGGER_MANUAL   = "manual"
	TRIGGER_SCHEDULE = "schedule"
	TRIGGER_WATCHER  = "watcher"
	TRIGGER_SETTINGS = "settings"
	TRIGGER_STARTUP  = "startup"
	TRIGGER_ORGANIZE = "organize"
)

// task results
const (
	TASK_RUNNING = "running"
	TASK_SUCCESS = "success"
	TASK_WARNING = "warning"
	TASK_FAILED  = "failed"
)

// problems reported by tasks, translated by the interface
const (
	NOTE_INTERRUPTED       = "Interrupted: the app stopped while the task was running."
	NOTE_TITLES_DOWNLOAD   = "The titles database could not be downloaded."
	NOTE_TITLES_SAVED_COPY = "The titles database could not be downloaded, the saved copy is used."
	NOTE_SCAN_FAILED       = "The library could not be scanned."
	NOTE_NOTIFY_FAILED     = "The notification could not be sent."
	NOTE_ORGANIZE_FAILED   = "The files could not be organized."
)

var taskNoteTexts = []string{NOTE_INTERRUPTED, NOTE_TITLES_DOWNLOAD, NOTE_TITLES_SAVED_COPY, NOTE_SCAN_FAILED, NOTE_NOTIFY_FAILED, NOTE_ORGANIZE_FAILED}

const (
	TASKS_FILENAME = "tasks.json"
	// finished tasks kept in the history; failed tasks stay until they are dismissed
	maxFinishedTasks = 30
)

// Task is a synchronization, scan or organize run, as shown on the Tasks page.
type Task struct {
	Id       int64      `json:"id"`
	Kind     string     `json:"kind"`
	Trigger  string     `json:"trigger"`
	Status   string     `json:"status"`
	Started  time.Time  `json:"started"`
	Finished time.Time  `json:"finished,omitempty"`
	Current  int        `json:"current"`
	Total    int        `json:"total"`
	Message  string     `json:"message"`
	Error    *TaskNote  `json:"error,omitempty"`
	Warnings []TaskNote `json:"warnings,omitempty"`
	// results, depending on the kind
	Games int `json:"games,omitempty"`
	Files int `json:"files,omitempty"`
}

// TaskNote is a problem of a task: Text is an English sentence translated by the
// interface, Detail the technical reason, shown as is.
type TaskNote struct {
	Text   string `json:"text"`
	Detail string `json:"detail,omitempty"`
}

func (t Task) Running() bool {
	return t.Status == TASK_RUNNING
}

// Percent is the progress of a running task, or -1 when the number of steps is unknown.
func (t Task) Percent() int {
	if t.Total <= 0 || t.Current < 0 {
		return -1
	}
	if t.Current >= t.Total {
		return 100
	}
	return t.Current * 100 / t.Total
}

func (t Task) Duration() time.Duration {
	end := t.Finished
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(t.Started).Round(time.Second)
}

// TaskLog keeps the running and recent tasks and tells subscribers when they change.
type TaskLog struct {
	mutex       sync.Mutex
	path        string
	tasks       []*Task // newest first
	nextId      int64
	version     int64
	subscribers map[chan int64]struct{}
}

// loadTaskLog reads the history saved in the data folder. Tasks that were running when
// the app stopped are marked as failed.
func loadTaskLog(dataFolder string) *TaskLog {
	log := &TaskLog{path: filepath.Join(dataFolder, TASKS_FILENAME), subscribers: map[chan int64]struct{}{}}
	if data, err := os.ReadFile(log.path); err == nil {
		saved := []*Task{}
		if json.Unmarshal(data, &saved) == nil {
			log.tasks = saved
		}
	}
	for _, task := range log.tasks {
		if task.Id >= log.nextId {
			log.nextId = task.Id
		}
		if task.Status == TASK_RUNNING {
			task.Status = TASK_FAILED
			task.Error = &TaskNote{Text: NOTE_INTERRUPTED}
			task.Finished = task.Started
		}
	}
	return log
}

// Start records a new running task.
func (l *TaskLog) Start(kind string, trigger string) int64 {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.nextId++
	task := &Task{Id: l.nextId, Kind: kind, Trigger: trigger, Status: TASK_RUNNING, Started: time.Now(), Total: -1, Current: -1}
	l.tasks = append([]*Task{task}, l.tasks...)
	l.changed(false)
	return task.Id
}

// Progress updates a running task. Progress is not saved to disk.
func (l *TaskLog) Progress(id int64, current int, total int, message string) {
	l.update(id, false, func(task *Task) {
		task.Current, task.Total, task.Message = current, total, message
	})
}

// Warn adds a problem that did not stop the task.
func (l *TaskLog) Warn(id int64, text string, detail string) {
	l.update(id, false, func(task *Task) {
		task.Warnings = append(task.Warnings, TaskNote{Text: text, Detail: detail})
	})
}

// SetResult records what the task found.
func (l *TaskLog) SetResult(id int64, games int, files int) {
	l.update(id, false, func(task *Task) {
		task.Games, task.Files = games, files
	})
}

// Finish ends a task; a failure makes it failed, warnings make it a warning.
func (l *TaskLog) Finish(id int64, failure *TaskNote) {
	l.update(id, true, func(task *Task) {
		task.Finished = time.Now()
		task.Message = ""
		switch {
		case failure != nil:
			task.Status = TASK_FAILED
			task.Error = failure
		case len(task.Warnings) > 0:
			task.Status = TASK_WARNING
		default:
			task.Status = TASK_SUCCESS
		}
	})
}

// Dismiss removes a finished task from the history.
func (l *TaskLog) Dismiss(id int64) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	for i, task := range l.tasks {
		if task.Id == id && task.Status != TASK_RUNNING {
			l.tasks = append(l.tasks[:i], l.tasks[i+1:]...)
			l.changed(true)
			return true
		}
	}
	return false
}

// ClearFinished removes every finished task, failed ones included.
func (l *TaskLog) ClearFinished() {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	running := []*Task{}
	for _, task := range l.tasks {
		if task.Status == TASK_RUNNING {
			running = append(running, task)
		}
	}
	l.tasks = running
	l.changed(true)
}

// Snapshot returns a copy of the tasks, newest first.
func (l *TaskLog) Snapshot() []Task {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	tasks := make([]Task, 0, len(l.tasks))
	for _, task := range l.tasks {
		copied := *task
		copied.Warnings = append([]TaskNote(nil), task.Warnings...)
		tasks = append(tasks, copied)
	}
	return tasks
}

// FailedCount counts the failed tasks that were not dismissed.
func (l *TaskLog) FailedCount() int {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	count := 0
	for _, task := range l.tasks {
		if task.Status == TASK_FAILED {
			count++
		}
	}
	return count
}

// Subscribe returns a channel that receives the new version after every change, and a
// function to stop. Changes are coalesced: a slow reader only gets the latest version.
func (l *TaskLog) Subscribe() (<-chan int64, func()) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	ch := make(chan int64, 1)
	l.subscribers[ch] = struct{}{}
	return ch, func() {
		l.mutex.Lock()
		defer l.mutex.Unlock()
		delete(l.subscribers, ch)
	}
}

func (l *TaskLog) Version() int64 {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.version
}

func (l *TaskLog) update(id int64, save bool, change func(task *Task)) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	for _, task := range l.tasks {
		if task.Id == id {
			change(task)
			l.changed(save)
			return
		}
	}
}

// changed must be called with the mutex held.
func (l *TaskLog) changed(save bool) {
	l.version++
	for ch := range l.subscribers {
		select {
		case <-ch:
		default:
		}
		ch <- l.version
	}
	if save {
		l.trim()
		l.save()
	}
}

// trim drops the oldest finished tasks beyond the limit, but keeps failed ones.
func (l *TaskLog) trim() {
	kept := []*Task{}
	finished := 0
	for _, task := range l.tasks {
		if task.Status != TASK_RUNNING && task.Status != TASK_FAILED {
			finished++
			if finished > maxFinishedTasks {
				continue
			}
		}
		kept = append(kept, task)
	}
	l.tasks = kept
}

func (l *TaskLog) save() {
	if l.path == "" {
		return
	}
	data, err := json.MarshalIndent(l.tasks, "", " ")
	if err != nil {
		return
	}
	tmp := l.path + ".tmp"
	if os.WriteFile(tmp, data, 0644) == nil {
		os.Rename(tmp, l.path)
	}
}
