package web

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	TRIGGER_MANUAL   = "manual"
	TRIGGER_SCHEDULE = "schedule"
	TRIGGER_WATCHER  = "watcher"
	TRIGGER_SETTINGS = "settings"
	TRIGGER_STARTUP  = "startup"
	TRIGGER_ORGANIZE = "organize"
	TRIGGER_COMPRESS = "compress"
	// a game uploaded from the browser
	TRIGGER_UPLOAD     = "upload"
	TRIGGER_SCAN       = "scan"
	TRIGGER_AUTOMATION = "automation"
	// a task on files the app was stopped in the middle of goes on once it is started again
	TRIGGER_RESUME = "resume"
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
	// no server of the titles database answered, as when the server has no connection
	NOTE_TITLES_OFFLINE      = "Offline: the titles database was not downloaded, the saved copy is used."
	NOTE_TITLES_OFFLINE_NONE = "Offline: the titles database was not downloaded."
	NOTE_COVERS_OFFLINE      = "Offline: the covers left are downloaded after the next scan."
	NOTE_SCAN_FAILED         = "The library could not be scanned."
	NOTE_NOTIFY_FAILED       = "The notification could not be sent."
	NOTE_ORGANIZE_FAILED     = "The files could not be organized."
	// a file Organize could not change, and one it left as it is, each with why
	NOTE_ORGANIZE_FILE_FAILED  = "A file could not be changed."
	NOTE_ORGANIZE_FILE_KEPT    = "A file was left as it is."
	NOTE_ORGANIZE_FILE_RENAMED = "A file was renamed in its folder."
	NOTE_ORGANIZE_MORE         = "More files left as they are, all listed in the preview of Organize:"
	NOTE_PAUSED_FOR_SCAN       = "Paused for a scan; it continues afterwards."
	NOTE_PAUSED_FOR_HOURS      = "Paused outside the background hours; it continues when they begin."
)

var taskNoteTexts = []string{NOTE_SD_COPY_FAILED, NOTE_INTERRUPTED, NOTE_TITLES_DOWNLOAD, NOTE_TITLES_SAVED_COPY, NOTE_TITLES_OFFLINE, NOTE_TITLES_OFFLINE_NONE, NOTE_COVERS_OFFLINE, NOTE_SCAN_FAILED, NOTE_NOTIFY_FAILED, NOTE_ORGANIZE_FAILED, NOTE_PAUSED_FOR_SCAN, NOTE_PAUSED_FOR_HOURS}

const (
	TASKS_FILENAME = "tasks.json"
	// finished tasks kept in the history; failed tasks stay until they are dismissed
	maxFinishedTasks = 30
)

// formatChanges are the formats a task on files turns a file into, by kind and extension.
var formatChanges = map[string]map[string]string{
	TASK_COMPRESS:   {".nsp": "NSP → NSZ", ".xci": "XCI → XCZ"},
	TASK_DECOMPRESS: {".nsz": "NSZ → NSP", ".xcz": "XCZ → XCI"},
	TASK_CONVERT:    {".xci": "XCI → NSP", ".xcz": "XCZ → NSZ"},
}

// formatChange is the change of format of a file in a task on files, e.g. "NSP → NSZ", or ""
// when the task does not change it.
func formatChange(kind string, name string) string {
	return formatChanges[kind][strings.ToLower(filepath.Ext(name))]
}

// TaskResume is what a task on files still has to do and how, saved as it goes: a task the
// app was stopped in the middle of (a restart, an update, a backup of the server at night)
// goes on with it once the app is started again.
type TaskResume struct {
	Paths  []string `json:"paths"`
	Level  string   `json:"level,omitempty"`
	Delete bool     `json:"delete,omitempty"`
}

// Task is a synchronization, scan or organize run, as shown on the Tasks page.
type Task struct {
	Id int64 `json:"id"`
	// the files a compression, decompression or conversion has left, while it runs
	Resume   *TaskResume `json:"resume,omitempty"`
	Kind     string      `json:"kind"`
	Trigger  string      `json:"trigger"`
	Status   string      `json:"status"`
	Started  time.Time   `json:"started"`
	Finished time.Time   `json:"finished,omitempty"`
	Current  int         `json:"current"`
	Total    int         `json:"total"`
	Message  string      `json:"message"`
	// what the message names, when it is "... %v"
	MessageArg string `json:"messageArg,omitempty"`
	// the file a task is about, kept once it ended (an upload)
	Subject string `json:"subject,omitempty"`
	// the files of a task on files: the one it reached, out of how many
	FileNumber int        `json:"fileNumber,omitempty"`
	FileCount  int        `json:"fileCount,omitempty"`
	Error      *TaskNote  `json:"error,omitempty"`
	Warnings   []TaskNote `json:"warnings,omitempty"`
	// results, depending on the kind
	Games int `json:"games,omitempty"`
	Files int `json:"files,omitempty"`
	// bytes saved by a compression, out of the size of the files compressed
	Saved    int64 `json:"saved,omitempty"`
	Original int64 `json:"original,omitempty"`
	// the size of each file before and after a compression
	Results []TaskResult `json:"results,omitempty"`
	// how each file of a task on files ended, for the view of the Tasks page by covers
	Tiles []TaskTile `json:"tiles,omitempty"`
	// every file of a running task on files, in its order, waiting or ended: the view by covers
	// shows them all, as NXPress does. Not saved to disk.
	Plan []TaskTile `json:"-"`
	// on the Tasks page only: the files of the plan left out after those shown
	HiddenAfter int `json:"-"`
	// damaged files found by a verification, and why its files are checked
	Damaged   int              `json:"damaged,omitempty"`
	Breakdown *VerifyBreakdown `json:"breakdown,omitempty"`
	// work done at the same time: the files being worked on, each with its progress, and
	// how many workers or threads the task uses
	Lanes []TaskLane `json:"lanes,omitempty"`
	// a running compression held by the user until it is resumed, since when, and how long
	// it was held before: the speed counts only the time it worked
	Paused    bool          `json:"paused,omitempty"`
	PausedAt  time.Time     `json:"pausedAt,omitempty"`
	PausedFor time.Duration `json:"pausedFor,omitempty"`
	Workers   int           `json:"workers,omitempty"`
	Threads   int           `json:"threads,omitempty"`
	// a task on files estimates its time left from the speed of each of its steps, as of
	// LeftAt: Left is 0 while a step it still has to do was not measured
	Estimated bool          `json:"estimated,omitempty"`
	Left      time.Duration `json:"left,omitempty"`
	LeftAt    time.Time     `json:"leftAt,omitempty"`
	// on the Tasks page only: the next file a task on files will take, and its cover
	Next *TaskLane `json:"-"`
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
	// what the file holds, from its name: Base, Update or DLC by the title ID, Pack for a
	// complete pack ("" unknown)
	Kind string `json:"kind,omitempty"`
	// on the Tasks page only: the cover of the game of the file
	Cover string `json:"-"`
}

// laneTones are the colours of the library overview, one for each file being worked on.
var laneTones = []string{"red", "yellow", "cyan", "green"}

// the colours of the steps of a file: the hashes, the work itself, the check
var phaseTones = map[string]string{PHASE_HASHING: "cyan", PHASE_VERIFYING: "green"}

// the colours of what a file holds, as in the library overview
var kindTones = map[string]string{"Base": "red", "Update": "yellow", "DLC": "cyan", "Pack": "green"}

// Tone is the colour of the bar of the file: the step it is in for a task in steps
// (compression, decompression...), what it holds for a check (game, update or DLC), or else
// always the same for the same name.
func (l TaskLane) Tone() string {
	if l.Phase != "" {
		if tone, ok := phaseTones[l.Phase]; ok {
			return tone
		}
		return "yellow"
	}
	if tone, ok := kindTones[l.Kind]; ok {
		return tone
	}
	hash := fnv.New32a()
	hash.Write([]byte(l.Name))
	return laneTones[hash.Sum32()%uint32(len(laneTones))]
}

// laneKind is what a file holds for its bar: a complete pack, named after its base game, is a
// pack and not a game.
func laneKind(name string) string {
	if strings.Contains(strings.ToLower(name), "(pack)") {
		return "Pack"
	}
	return contentKind(name)
}

// KindName is what the file holds, to translate, shown while it is checked: "Game", "Update"
// or "DLC"; "" when unknown or when the file goes through steps.
func (l TaskLane) KindName() string {
	if l.Phase != "" {
		return ""
	}
	switch l.Kind {
	case "Base":
		return "Game"
	case "Pack":
		return "Complete pack"
	case "Update", "DLC":
		return l.Kind
	}
	return ""
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

// StepTotal is the size of the file, read once in each step: its progress counts every step,
// so 13 GB read three times showed as 39 GB, which is not the size anyone sees in the folder.
func (l TaskLane) StepTotal() int64 {
	if l.Steps <= 1 {
		return l.Total
	}
	return l.Total / int64(l.Steps)
}

// StepDone is how far the step being done is, out of StepTotal.
func (l TaskLane) StepDone() int64 {
	if l.Steps <= 1 {
		return l.Done
	}
	total := l.StepTotal()
	return min(max(l.Done-int64(l.Step-1)*total, 0), total)
}

// StepPercent is the progress of the step being done.
func (l TaskLane) StepPercent() int {
	return TaskLane{Done: l.StepDone(), Total: l.StepTotal()}.Percent()
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

// TaskTile is how a file of a task on files ended: done, or failed and why.
type TaskTile struct {
	Name   string `json:"name"`
	Failed bool   `json:"failed,omitempty"`
	// why it failed, an English sentence translated by the interface
	Note string `json:"note,omitempty"`
	// in the plan of a running task: the file is not done yet
	Waiting bool `json:"-"`
	// on the Tasks page only: the cover of the game of the file, and, for a file at work, its
	// progress
	Cover   string `json:"-"`
	Working bool   `json:"-"`
	Percent int    `json:"-"`
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
// PercentExact is Percent with two decimals, for the width of the bar: a long task shows
// that it moved before its first whole percent.
func (t Task) PercentExact() string {
	if t.Total <= 0 || t.Current < 0 {
		return "0"
	}
	if t.Current >= t.Total {
		return "100"
	}
	return strconv.FormatFloat(float64(t.Current)*100/float64(t.Total), 'f', 2, 64)
}

func (t Task) Percent() int {
	if t.Total <= 0 || t.Current < 0 {
		return -1
	}
	if t.Current >= t.Total {
		return 100
	}
	return t.Current * 100 / t.Total
}

// Speed returns the megabytes per second of a running task that reads files (a check, a copy
// to the SD card, a compression and the like), or 0.
func (t Task) Speed() int {
	return t.speedAt(time.Now())
}

func (t Task) speedAt(now time.Time) int {
	if t.Paused {
		return 0
	}
	elapsed := (now.Sub(t.Started) - t.PausedFor).Seconds()
	if !measuredInMegabytes(t.Kind) || t.Current <= 0 || elapsed < 10 {
		return 0
	}
	return int(float64(t.Current) / elapsed)
}

// measuredInMegabytes tells the tasks whose progress counts megabytes of files.
func measuredInMegabytes(kind string) bool {
	switch kind {
	case TASK_VERIFY, TASK_COPY, TASK_COMPRESS, TASK_DECOMPRESS, TASK_CONVERT, TASK_PACK, TASK_SPLIT:
		return true
	}
	return false
}

// RemainingText is the time left of a running task and about when it ends ("2h 15m · 18:40"),
// or "" while it is not known yet.
func (t Task) RemainingText() string {
	return t.remainingTextAt(time.Now())
}

func (t Task) remainingTextAt(now time.Time) string {
	left := t.remainingAt(now)
	if left <= 0 {
		return ""
	}
	end := now.Add(left)
	clock := end.Format("15:04")
	// a task that ends another day says which
	if end.Format("2006-01-02") != now.Format("2006-01-02") {
		clock = end.Format("02/01 15:04")
	}
	return formatDuration(left) + " · " + clock
}

// Remaining estimates the time left of a running task that reads files, or 0.
func (t Task) Remaining() time.Duration {
	return t.remainingAt(time.Now())
}

func (t Task) remainingAt(now time.Time) time.Duration {
	// the steps of a task on files go at very different speeds (hashes at hundreds of MB/s,
	// the max compression at a few): the average of all of them said 2 minutes for an hour
	if t.Estimated {
		if t.Paused || t.Left <= 0 {
			return 0
		}
		return max(t.Left-now.Sub(t.LeftAt), time.Minute).Round(time.Minute)
	}
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
	// told of each task that fails, in the background
	onFailed func(Task)
}

// loadTaskLog reads the history saved in the data folder. Tasks that were running when
// the app stopped are marked as failed.
func loadTaskLog(dataFolder string) *TaskLog {
	log := &TaskLog{path: filepath.Join(dataFolder, TASKS_FILENAME), subscribers: map[chan int64]struct{}{}}
	// when the history was saved last: the app was still working then
	var saved time.Time
	if info, err := os.Stat(log.path); err == nil {
		saved = info.ModTime()
	}
	if data, err := os.ReadFile(log.path); err == nil {
		saved := []*Task{}
		if parseStore(log.path, data, &saved) == nil {
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
			// the moment the app stopped is not known: the last one it is known to have
			// worked on the task, instead of its start, which showed it lasted no time at all
			task.Finished = task.Started
			for _, at := range []time.Time{task.LeftAt, saved} {
				if at.After(task.Finished) {
					task.Finished = at
				}
			}
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

// ProgressFilesLeft is ProgressFiles with the time left estimated from the speed of each step,
// or 0 while it is not known.
func (l *TaskLog) ProgressFilesLeft(id int64, current int, total int, message string, number int, count int, lanes []TaskLane, left time.Duration) {
	l.update(id, false, func(task *Task) {
		task.Current, task.Total, task.Message, task.MessageArg, task.Lanes = current, total, message, "", lanes
		task.FileNumber, task.FileCount = number, count
		task.Estimated, task.Left, task.LeftAt = true, left, time.Now()
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

// SetCompressResult records the files compressed so far and the bytes saved. It is saved
// at once, so a task the app is stopped in the middle of still says what it did.
func (l *TaskLog) SetCompressResult(id int64, files int, saved int64) {
	l.update(id, false, func(task *Task) {
		task.Files, task.Saved = files, saved
	})
}

// SetResume records what a task on files still has to do; nil when it ended by itself or
// was cancelled, so it is not started again.
func (l *TaskLog) SetResume(id int64, resume *TaskResume) {
	l.update(id, true, func(task *Task) {
		task.Resume = resume
	})
}

// interrupted gives the last task on files the app was stopped in the middle of that still
// has files left, with what it had left; tasks running now are not looked at.
func (l *TaskLog) interrupted() (int64, string, *TaskResume) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	for _, task := range l.tasks {
		if task.Resume != nil && task.Status == TASK_FAILED && task.Error != nil && task.Error.Text == NOTE_INTERRUPTED {
			resume := *task.Resume
			resume.Paths = append([]string(nil), task.Resume.Paths...)
			return task.Id, task.Kind, &resume
		}
	}
	return 0, "", nil
}

// SetPaused records that a running task is held, or goes on again.
func (l *TaskLog) SetPaused(id int64, paused bool) {
	l.update(id, false, func(task *Task) {
		if task.Paused == paused {
			return
		}
		task.Paused = paused
		if paused {
			task.PausedAt = time.Now()
		} else {
			task.PausedFor += time.Since(task.PausedAt)
			task.PausedAt = time.Time{}
		}
	})
}

// SetSubject sets the file a task is about.
func (l *TaskLog) SetSubject(id int64, subject string) {
	l.update(id, false, func(task *Task) {
		task.Subject = subject
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

// SetPlan records every file a task on files has to do, in its order, all waiting.
func (l *TaskLog) SetPlan(id int64, names []string) {
	plan := make([]TaskTile, len(names))
	for i, name := range names {
		plan[i] = TaskTile{Name: name, Waiting: true}
	}
	l.update(id, false, func(task *Task) {
		task.Plan = plan
	})
}

// AddFileTile records how a file of a task on files ended, in its plan too.
func (l *TaskLog) AddFileTile(id int64, name string, failed bool, note string) {
	l.update(id, false, func(task *Task) {
		tile := TaskTile{Name: name, Failed: failed, Note: note}
		task.Tiles = append(task.Tiles, tile)
		if len(task.Tiles) > maxTaskResults {
			task.Tiles = task.Tiles[len(task.Tiles)-maxTaskResults:]
		}
		for i := range task.Plan {
			if task.Plan[i].Waiting && task.Plan[i].Name == name {
				task.Plan[i] = tile
				break
			}
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
	var failed *Task
	defer func() {
		if failed != nil && l.onFailed != nil {
			go l.onFailed(*failed)
		}
	}()
	l.update(id, true, func(task *Task) {
		defer func() {
			if task.Status == TASK_FAILED {
				copied := *task
				failed = &copied
			}
		}()
		task.Finished = time.Now()
		task.Message = ""
		task.Lanes, task.Plan = nil, nil
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
		copied.Tiles = append([]TaskTile(nil), task.Tiles...)
		copied.Plan = append([]TaskTile(nil), task.Plan...)
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

// Touch tells the pages of the tasks that something they show changed (the queue).
func (l *TaskLog) Touch() {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.changed(false)
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
	writeFileAtomic(l.path, data)
}
