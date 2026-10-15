package web

import (
	"encoding/json"
	"hash/fnv"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// kinds of background tasks
const (
	TASK_SYNC       = "sync"
	TASK_SCAN       = "scan"
	TASK_ORGANIZE   = "organize"
	TASK_CLEANUP    = "cleanup"
	TASK_COMPRESS   = "compress"
	TASK_DECOMPRESS = "decompress"
	TASK_VERIFY     = "verify"
	TASK_CONVERT    = "convert"
	// background work after a scan
	TASK_COVERS     = "covers"
	TASK_THUMBNAILS = "thumbnails"
)

// what started a task
const (
	TRIGGER_MANUAL     = "manual"
	TRIGGER_SCHEDULE   = "schedule"
	TRIGGER_WATCHER    = "watcher"
	TRIGGER_SETTINGS   = "settings"
	TRIGGER_STARTUP    = "startup"
	TRIGGER_ORGANIZE   = "organize"
	TRIGGER_COMPRESS   = "compress"
	TRIGGER_SCAN       = "scan"
	TRIGGER_AUTOMATION = "automation"
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
	NOTE_PAUSED_FOR_SCAN   = "Paused for a scan; it continues afterwards."
	NOTE_PAUSED_FOR_HOURS  = "Paused outside the background hours; it continues when they begin."
)

var taskNoteTexts = []string{NOTE_SD_COPY_FAILED, NOTE_INTERRUPTED, NOTE_TITLES_DOWNLOAD, NOTE_TITLES_SAVED_COPY, NOTE_SCAN_FAILED, NOTE_NOTIFY_FAILED, NOTE_ORGANIZE_FAILED, NOTE_PAUSED_FOR_SCAN, NOTE_PAUSED_FOR_HOURS}

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
	// what the message names, when it is "... %v"
	MessageArg string `json:"messageArg,omitempty"`
	// the files of a task on files: the one it reached, out of how many
	FileNumber int `json:"fileNumber,omitempty"`
	FileCount  int `json:"fileCount,omitempty"`
	Error    *TaskNote  `json:"error,omitempty"`
	Warnings []TaskNote `json:"warnings,omitempty"`
	// results, depending on the kind
	Games int `json:"games,omitempty"`
	Files int `json:"files,omitempty"`
	// bytes saved by a compression, out of the size of the files compressed
	Saved    int64 `json:"saved,omitempty"`
	Original int64 `json:"original,omitempty"`
	// the size of each file before and after a compression
	Results []TaskResult `json:"results,omitempty"`
	// damaged files found by a verification, and why its files are checked
	Damaged   int              `json:"damaged,omitempty"`
	Breakdown *VerifyBreakdown `json:"breakdown,omitempty"`
	// work done at the same time: the files being worked on, each with its progress, and
	// how many workers or threads the task uses
	Lanes   []TaskLane `json:"lanes,omitempty"`
	Workers int        `json:"workers,omitempty"`
	Threads int        `json:"threads,omitempty"`
}

// TaskLane is a file a running task is working on, with its own progress in bytes.
type TaskLane struct {
	Name  string `json:"name"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	// what is being done to the file, an English sentence translated by the interface, as
	// the step out of the steps of the file
	Phase string `json:"phase,omitempty"`
	Step  int    `json:"step,omitempty"`
	Steps int    `json:"steps,omitempty"`
}

// laneTones are the colours of the library overview, one for each file being worked on.
var laneTones = []string{"red", "yellow", "cyan", "green"}

// Tone is the colour of the bar of the file, always the same for the same name.
func (l TaskLane) Tone() string {
	hash := fnv.New32a()
	hash.Write([]byte(l.Name))
	return laneTones[hash.Sum32()%uint32(len(laneTones))]
}

// Percent is the progress of the file.
func (l TaskLane) Percent() int {
	if l.Total <= 0 {
		return 0
	}
	if l.Done >= l.Total {
		return 100
	}
	return int(l.Done * 100 / l.Total)
}

// VerifyBreakdown is why the files of a verification are checked: never checked before,
// changed since, checked again once because an older version of the check may have got them
// wrong, or checked again as asked (everything, or the files checked longest ago).
type VerifyBreakdown struct {
	New      int `json:"new"`
	Changed  int `json:"changed"`
	Improved int `json:"improved"`
	Again    int `json:"again"`
}

// TaskResult is a file a task finished, with its size before and after.
type TaskResult struct {
	Name   string `json:"name"`
	Before int64  `json:"before"`
	After  int64  `json:"after"`
}

// maxTaskResults is how many files a task lists, the last ones.
const maxTaskResults = 200

// SavedBytes is what the compression of the file saved.
func (r TaskResult) SavedBytes() int64 { return r.Before - r.After }

// KeptWidth and SavedWidth are the shares of the size before that the file keeps and
// saved, as CSS widths in percent.
func (r TaskResult) KeptWidth() string  { return keptWidth(r.After, r.Before) }
func (r TaskResult) SavedWidth() string { return savedWidth(r.After, r.Before) }

func keptWidth(after int64, before int64) string {
	if before <= 0 {
		return "100"
	}
	share := float64(after) * 100 / float64(before)
	return fmt.Sprintf("%.2f", max(0, min(100, share)))
}

func savedWidth(after int64, before int64) string {
	if before <= 0 {
		return "0"
	}
	share := 100 - float64(after)*100/float64(before)
	return fmt.Sprintf("%.2f", max(0, min(100, share)))
}

// Kept is the size of the files compressed after their compression.
func (t Task) Kept() int64 { return t.Original - t.Saved }

// KeptWidth and SavedWidth are the shares of the files compressed kept and saved.
func (t Task) KeptWidth() string  { return keptWidth(t.Original-t.Saved, t.Original) }
func (t Task) SavedWidth() string { return savedWidth(t.Original-t.Saved, t.Original) }

// SavedPercent is the share of the file saved, as "3.0 %".
func (r TaskResult) SavedPercent() string {
	return savedPercent(r.Before-r.After, r.Before)
}

func savedPercent(saved int64, original int64) string {
	if original <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f %%", float64(saved)*100/float64(original))
}

// SavedPercent is the share of the files compressed that was saved, or "" when unknown.
func (t Task) SavedPercent() string {
	return savedPercent(t.Saved, t.Original)
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

// Speed returns the megabytes checked per second by a running verification, or 0.
func (t Task) Speed() int {
	return t.speedAt(time.Now())
}

func (t Task) speedAt(now time.Time) int {
	elapsed := now.Sub(t.Started).Seconds()
	if (t.Kind != TASK_VERIFY && t.Kind != TASK_COPY) || t.Current <= 0 || elapsed < 10 {
		return 0
	}
	return int(float64(t.Current) / elapsed)
}

// Remaining estimates the time left of a running verification, or 0.
func (t Task) Remaining() time.Duration {
	return t.remainingAt(time.Now())
}

func (t Task) remainingAt(now time.Time) time.Duration {
	speed := t.speedAt(now)
	if speed <= 0 || t.Total <= t.Current {
		return 0
	}
	return (time.Duration(t.Total-t.Current) / time.Duration(speed) * time.Second).Round(time.Minute)
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
	l.ProgressArg(id, current, total, message, "")
}

// ProgressArg is Progress with a message "... %v" and what it names apart, so the message is
// translated.
func (l *TaskLog) ProgressArg(id int64, current int, total int, message string, arg string) {
	l.update(id, false, func(task *Task) {
		task.Current, task.Total, task.Message, task.MessageArg = current, total, message, arg
	})
}

// ProgressLanes is Progress with the files the task works on, in one update.
func (l *TaskLog) ProgressLanes(id int64, current int, total int, message string, lanes []TaskLane) {
	l.update(id, false, func(task *Task) {
		task.Current, task.Total, task.Message, task.MessageArg, task.Lanes = current, total, message, "", lanes
	})
}

// ProgressFiles is ProgressLanes for a task on files, with the file it reached out of how
// many, shown in a place of their own instead of after the name of the file.
func (l *TaskLog) ProgressFiles(id int64, current int, total int, message string, number int, count int, lanes []TaskLane) {
	l.update(id, false, func(task *Task) {
		task.Current, task.Total, task.Message, task.MessageArg, task.Lanes = current, total, message, "", lanes
		task.FileNumber, task.FileCount = number, count
	})
}

// Lanes updates the files a running task is working on at the same time. Not saved to disk.
func (l *TaskLog) Lanes(id int64, lanes []TaskLane) {
	l.update(id, false, func(task *Task) {
		task.Lanes = lanes
	})
}

// SetWorkers tells how many files a task works on at the same time, and with how many
// threads each one.
func (l *TaskLog) SetWorkers(id int64, workers int, threads int) {
	l.update(id, false, func(task *Task) {
		task.Workers, task.Threads = workers, threads
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

// SetCompressResult records the files compressed so far and the bytes saved.
func (l *TaskLog) SetCompressResult(id int64, files int, saved int64) {
	l.update(id, false, func(task *Task) {
		task.Files, task.Saved = files, saved
	})
}

// AddFileResult records a file compressed, with its size before and after.
func (l *TaskLog) AddFileResult(id int64, name string, before int64, after int64) {
	l.update(id, false, func(task *Task) {
		task.Original += before
		task.Results = append(task.Results, TaskResult{Name: name, Before: before, After: after})
		if len(task.Results) > maxTaskResults {
			task.Results = task.Results[len(task.Results)-maxTaskResults:]
		}
	})
}

// SetBreakdown records why the files of a verification are checked.
func (l *TaskLog) SetBreakdown(id int64, breakdown VerifyBreakdown) {
	l.update(id, false, func(task *Task) {
		task.Breakdown = &breakdown
	})
}

// SetVerifyResult records the files checked so far and the damaged ones.
func (l *TaskLog) SetVerifyResult(id int64, checked int, damaged int) {
	l.update(id, false, func(task *Task) {
		task.Files, task.Damaged = checked, damaged
	})
}

// Finish ends a task; a failure makes it failed, warnings make it a warning.
func (l *TaskLog) Finish(id int64, failure *TaskNote) {
	l.update(id, true, func(task *Task) {
		task.Finished = time.Now()
		task.Message = ""
		task.Lanes = nil
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

// RunningCount is the number of tasks that are running, background work included.
func (l *TaskLog) RunningCount() int {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	count := 0
	for _, task := range l.tasks {
		if task.Status == TASK_RUNNING {
			count++
		}
	}
	return count
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
	if os.WriteFile(tmp, data, 0600) == nil {
		os.Rename(tmp, l.path)
	}
}
