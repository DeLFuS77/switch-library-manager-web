// Package tasklog keeps the tasks the app runs in the background (synchronizations, scans,
// compressions, checks, copies) with their progress, file by file, and the history of the
// ones that ended, saved in the data folder so a task cut by a restart goes on.
package tasklog

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
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
	TASK_PACK       = "pack"
	// a game and its update made one NSP that takes the least room installed (experimental)
	TASK_OPTIMIZE = "optimize"
	TASK_SPLIT    = "split"
	// the metadata of a NUP counted from 0 written again, for Eglantine and Lichii
	TASK_NUP_FIX = "nupfix"
	// a NUP without an icon given the cover of its game
	TASK_NUP_ICON = "nupicon"
	// the files of a run of Organize put back where they were
	TASK_UNDO_ORGANIZE = "undoorganize"
	// games copied to an SD card
	TASK_COPY = "copy"
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
	// something the task did not expect stopped it: it ends failed, and the app goes on
	NOTE_TASK_CRASHED   = "The task stopped on an unexpected error."
	NOTE_SD_COPY_FAILED = "The files could not be copied."
	// every task on files that the user stops; the old text stays in the history saved before
	NOTE_COMPRESS_CANCELED     = "The task was cancelled."
	NOTE_COMPRESS_CANCELED_OLD = "The compression was cancelled."
	// an upload of a game from the browser that was cancelled or cut
	NOTE_UPLOAD_CANCELED = "The upload was cancelled."
	NOTE_UPLOAD_STOPPED  = "The upload stopped: the page was closed or lost the connection. Choose the same file again to go on where it stopped."
	// a check found an NCA a tool rewrote (see the levels of a check below)
	NOTE_VERIFY_UNSIGNED = "An NCA is not signed by Nintendo: a tool rewrote it."
)

var taskNoteTexts = []string{NOTE_SD_COPY_FAILED, NOTE_INTERRUPTED, NOTE_TITLES_DOWNLOAD, NOTE_TITLES_SAVED_COPY, NOTE_TITLES_OFFLINE, NOTE_TITLES_OFFLINE_NONE, NOTE_COVERS_OFFLINE, NOTE_SCAN_FAILED, NOTE_NOTIFY_FAILED, NOTE_ORGANIZE_FAILED, NOTE_PAUSED_FOR_SCAN, NOTE_PAUSED_FOR_HOURS, NOTE_TASK_CRASHED}

const (
	TASKS_FILENAME = "tasks.json"
	// a running task is saved at least this often, with when it was last heard of: a task the
	// app is stopped in the middle of is found again, with about when it stopped
	taskSaveInterval = 30 * time.Second
	// finished tasks kept in the history; failed tasks stay until they are dismissed
	maxFinishedTasks = 30
)

// formatChanges are the formats a task on files turns a file into, by kind and extension.
var formatChanges = map[string]map[string]string{
	TASK_COMPRESS:   {".nsp": "NSP → NSZ", ".xci": "XCI → XCZ"},
	TASK_DECOMPRESS: {".nsz": "NSZ → NSP", ".xcz": "XCZ → XCI"},
	TASK_CONVERT:    {".xci": "XCI → NSP", ".xcz": "XCZ → NSZ"},
}

// FormatChange is the change of format of a file in a task on files, e.g. "NSP → NSZ", or ""
// when the task does not change it.
func FormatChange(kind string, name string) string {
	return formatChanges[kind][strings.ToLower(filepath.Ext(name))]
}

// Resume is what a task on files still has to do and how, saved as it goes: a task the
// app was stopped in the middle of (a restart, an update, a backup of the server at night)
// goes on with it once the app is started again.
type Resume struct {
	Paths  []string `json:"paths"`
	Level  string   `json:"level,omitempty"`
	Delete bool     `json:"delete,omitempty"`
	// a check of files: how old a result may be to be left out (see startVerificationAt), and
	// when the check began, so the files it checked before the app stopped are not checked again
	Recheck time.Duration `json:"recheck,omitempty"`
	Since   time.Time     `json:"since,omitempty"`
}

// Task is a synchronization, scan or organize run, as shown on the Tasks page.
type Task struct {
	Id int64 `json:"id"`
	// the files a compression, decompression or conversion has left, while it runs
	Resume   *Resume   `json:"resume,omitempty"`
	Kind     string    `json:"kind"`
	Trigger  string    `json:"trigger"`
	Status   string    `json:"status"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
	// the last time a running task changed, saved every half minute: a task that stopped
	// changing is stuck, and a task the app was stopped in the middle of ended about then
	Beat time.Time `json:"beat,omitempty"`
	// a task the app was stopped in the middle of that went on by itself in this other task
	ContinuedIn int64  `json:"continuedIn,omitempty"`
	Current     int    `json:"current"`
	Total       int    `json:"total"`
	Message     string `json:"message"`
	// what the message names, when it is "... %v"
	MessageArg string `json:"messageArg,omitempty"`
	// the file a task is about, kept once it ended (an upload)
	Subject string `json:"subject,omitempty"`
	// the files of a task on files: the one it reached, out of how many
	FileNumber int    `json:"fileNumber,omitempty"`
	FileCount  int    `json:"fileCount,omitempty"`
	Error      *Note  `json:"error,omitempty"`
	Warnings   []Note `json:"warnings,omitempty"`
	// results, depending on the kind
	Games int `json:"games,omitempty"`
	Files int `json:"files,omitempty"`
	// bytes saved by a compression, out of the size of the files compressed
	Saved    int64 `json:"saved,omitempty"`
	Original int64 `json:"original,omitempty"`
	// the size of each file before and after a compression
	Results []Result `json:"results,omitempty"`
	// how each file of a task on files ended, for the view of the Tasks page by covers
	Tiles []Tile `json:"tiles,omitempty"`
	// every file of a running task on files, in its order, waiting or ended: the view by covers
	// shows them all, as NXPress does. Not saved to disk.
	Plan []Tile `json:"-"`
	// on the Tasks page only: the files of the plan left out after those shown
	HiddenAfter int `json:"-"`
	// damaged files found by a verification (failed), and why its files are checked
	Damaged   int              `json:"damaged,omitempty"`
	Breakdown *VerifyBreakdown `json:"breakdown,omitempty"`
	// the files of a verification with an NCA not signed by Nintendo and nothing damaged
	// (modified, no failure), and its level (see verifyLevel); ByFiles: its progress counts files,
	// not megabytes, as it reads no file whole
	Modified int    `json:"modified,omitempty"`
	Level    string `json:"level,omitempty"`
	ByFiles  bool   `json:"byFiles,omitempty"`
	// work done at the same time: the files being worked on, each with its progress, and
	// how many workers or threads the task uses
	Lanes []Lane `json:"lanes,omitempty"`
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
	Next *Lane `json:"-"`
}

// Lane is a file a running task is working on, with its own progress in bytes.
type Lane struct {
	Name  string `json:"name"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	// what is being done to the file, an English sentence translated by the interface, as
	// the step out of the steps of the file
	Phase string `json:"phase,omitempty"`
	Step  int    `json:"step,omitempty"`
	Steps int    `json:"steps,omitempty"`
	// every step of the file, in its order, for the colours of the bar of its cover
	Phases []string `json:"-"`
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

// phaseTone is the colour of a step of a file: the hashes, the check, or the work itself.
func phaseTone(phase string) string {
	if tone, ok := phaseTones[phase]; ok {
		return tone
	}
	return "yellow"
}

// the colours of what a file holds, as in the library overview
var kindTones = map[string]string{"Base": "red", "Update": "yellow", "DLC": "cyan", "Pack": "green"}

// Tone is the colour of the bar of the file: the step it is in for a task in steps
// (compression, decompression...), what it holds for a check (game, update or DLC), or else
// always the same for the same name.
func (l Lane) Tone() string {
	if l.Phase != "" {
		return phaseTone(l.Phase)
	}
	if tone, ok := kindTones[l.Kind]; ok {
		return tone
	}
	hash := fnv.New32a()
	hash.Write([]byte(l.Name))
	return laneTones[hash.Sum32()%uint32(len(laneTones))]
}

// LaneKind is what a file holds for its bar: a complete pack, named after its base game, is a
// pack and not a game.
func LaneKind(name string) string {
	if strings.Contains(strings.ToLower(name), "(pack)") {
		return "Pack"
	}
	return ContentKind(name)
}

// KindName is what the file holds, to translate, shown while it is checked: "Game", "Update"
// or "DLC"; "" when unknown or when the file goes through steps.
func (l Lane) KindName() string {
	if l.Phase != "" {
		return ""
	}
	switch l.Kind {
	case "Base":
		return "Game"
	case "Pack":
		return "Combine"
	case "Update", "DLC":
		return l.Kind
	}
	return ""
}

// Percent is the progress of the file.
func (l Lane) Percent() int {
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
func (l Lane) StepTotal() int64 {
	if l.Steps <= 1 {
		return l.Total
	}
	return l.Total / int64(l.Steps)
}

// StepDone is how far the step being done is, out of StepTotal.
func (l Lane) StepDone() int64 {
	if l.Steps <= 1 {
		return l.Done
	}
	total := l.StepTotal()
	return min(max(l.Done-int64(l.Step-1)*total, 0), total)
}

// Tile is the file at work as a cover of the view by covers: its progress in the step it is
// in, and the step.
func (l Lane) Tile() Tile {
	tile := Tile{Name: l.Name, Cover: l.Cover, Working: true, Percent: l.StepPercent()}
	if l.Phase != "" && l.Steps > 1 {
		tile.Phase, tile.Step, tile.Steps, tile.Tone, tile.Phases = l.Phase, l.Step, l.Steps, l.Tone(), l.Phases
	}
	return tile
}

// StepPercent is the progress of the step being done.
func (l Lane) StepPercent() int {
	return Lane{Done: l.StepDone(), Total: l.StepTotal()}.Percent()
}

// VerifyBreakdown is why the files of a verification are checked: never checked before,
// changed since, checked again once because an older version of the check may have got them
// wrong, or checked again as asked (everything, or the files checked longest ago).
type VerifyBreakdown struct {
	New      int `json:"new"`
	Changed  int `json:"changed"`
	Improved int `json:"improved"`
	Again    int `json:"again"`
	// checked before for their hashes: only their signatures are checked
	Signatures int `json:"signatures,omitempty"`
}

// Result is a file a task finished, with its size before and after.
type Result struct {
	Name   string `json:"name"`
	Before int64  `json:"before"`
	After  int64  `json:"after"`
}

// Tile is how a file of a task on files ended: done, or failed and why.
type Tile struct {
	Name   string `json:"name"`
	Failed bool   `json:"failed,omitempty"`
	// left as it is, with why: nothing in it could be done (a game in sparse storage to
	// compress), which is no failure
	Skipped bool `json:"skipped,omitempty"`
	// a file checked with an NCA not signed by Nintendo and nothing damaged, which is no failure
	Modified bool `json:"modified,omitempty"`
	// why it failed, an English sentence translated by the interface
	Note string `json:"note,omitempty"`
	// in the plan of a running task: the file is not done yet
	Waiting bool `json:"-"`
	// on the Tasks page only: the cover of the game of the file, and, for a file at work, its
	// progress
	Cover   string `json:"-"`
	Working bool   `json:"-"`
	Percent int    `json:"-"`
	// for a file at work in a task in steps (hashes, work, check): the step it is in, out of
	// how many, and its colour, as in the view by bars
	Phase  string   `json:"-"`
	Step   int      `json:"-"`
	Steps  int      `json:"-"`
	Tone   string   `json:"-"`
	Phases []string `json:"-"`
}

// TileSegment is a step of a file at work, for the bar of its cover split in steps: done, the
// one being done, or still to come, in the colour of its step.
type TileSegment struct {
	Done    bool
	Current bool
	Tone    string
}

// Segments are the steps of a file at work, for the bar of its cover; none for a task of one
// step, whose bar is whole.
func (t Tile) Segments() []TileSegment {
	if t.Steps <= 1 {
		return nil
	}
	segments := make([]TileSegment, t.Steps)
	for i := range segments {
		segments[i] = TileSegment{Done: i+1 < t.Step, Current: i+1 == t.Step, Tone: t.Tone}
		if i < len(t.Phases) {
			segments[i].Tone = phaseTone(t.Phases[i])
		}
	}
	return segments
}

// maxTaskResults is how many files a task lists, the last ones.
const maxTaskResults = 200

// SavedBytes is what the compression of the file saved.
func (r Result) SavedBytes() int64 { return r.Before - r.After }

// KeptWidth and SavedWidth are the shares of the size before that the file keeps and
// saved, as CSS widths in percent.
func (r Result) KeptWidth() string  { return keptWidth(r.After, r.Before) }
func (r Result) SavedWidth() string { return savedWidth(r.After, r.Before) }

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
func (r Result) SavedPercent() string {
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

// Note is a problem of a task: Text is an English sentence translated by the
// interface, Detail the technical reason, shown as is.
type Note struct {
	Text   string `json:"text"`
	Detail string `json:"detail,omitempty"`
}

func (t Task) Running() bool {
	return t.Status == TASK_RUNNING
}

// how a task that did not end well ended when it is no failure
const (
	// the user stopped it
	ENDING_CANCELLED = "cancelled"
	// a restart of the app stopped it (an update, the nightly backup of a NAS)
	ENDING_STOPPED = "stopped"
	// and it went on by itself in another task once the app started again
	ENDING_CONTINUED = "continued"
)

// Ending tells how a task that did not end well ended when that is no failure: cancelled by the
// user, stopped by a restart of the app, or stopped and gone on in another task. "" for a
// failure and for a task that ended well. These are neither red nor counted as failures.
func (t Task) Ending() string {
	if t.Status != TASK_FAILED || t.Error == nil {
		return ""
	}
	switch {
	case t.Error.Text == NOTE_INTERRUPTED && t.ContinuedIn != 0:
		return ENDING_CONTINUED
	case t.Error.Text == NOTE_INTERRUPTED:
		return ENDING_STOPPED
	case NotWorthTelling[t.Error.Text]:
		return ENDING_CANCELLED
	}
	return ""
}

// GoesOnByItself tells whether a task a restart stopped is done again without the user: it has
// files left to go on with, or what started it (a schedule, the automations, a scan, the start of
// the app) starts it again.
func (t Task) GoesOnByItself() bool {
	return t.Resume != nil || t.Trigger != TRIGGER_MANUAL || t.Kind == TASK_SCAN || t.Kind == TASK_COVERS || t.Kind == TASK_THUMBNAILS
}

// StoppedToday tells whether a task a restart stopped ended today, so its hour says when.
func (t Task) StoppedToday() bool {
	return t.Finished.Local().Format("2006-01-02") == time.Now().Format("2006-01-02")
}

// StoppedClock is the hour a task a restart stopped ended, as 05:00.
func (t Task) StoppedClock() string {
	return t.Finished.Local().Format("15:04")
}

// stallAfter is how long a running task goes without any change before it is told as stuck: a
// file the task waits for that never comes, a disk that does not answer.
const stallAfter = 15 * time.Minute

// StalledText is how long a running task has not changed, as "18m", once that is longer than
// stallAfter; "" otherwise, and while it is paused, which holds it on purpose.
func (t Task) StalledText() string {
	return t.stalledTextAt(time.Now())
}

func (t Task) stalledTextAt(now time.Time) string {
	if !t.Running() || t.Paused || t.Beat.IsZero() {
		return ""
	}
	if idle := now.Sub(t.Beat); idle >= stallAfter {
		return i18n.FormatDuration(idle)
	}
	return ""
}

// PausedText is how long a running task has been paused, as "3h 5m".
func (t Task) PausedText() string {
	if !t.Paused || t.PausedAt.IsZero() {
		return ""
	}
	return i18n.FormatDuration(time.Since(t.PausedAt))
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
	return t.SpeedAt(time.Now())
}

func (t Task) SpeedAt(now time.Time) int {
	if t.Paused {
		return 0
	}
	elapsed := (now.Sub(t.Started) - t.PausedFor).Seconds()
	if !measuredInMegabytes(t.Kind) || t.ByFiles || t.Current <= 0 || elapsed < 10 {
		return 0
	}
	return int(float64(t.Current) / elapsed)
}

// measuredInMegabytes tells the tasks whose progress counts megabytes of files.
func measuredInMegabytes(kind string) bool {
	switch kind {
	case TASK_VERIFY, TASK_COPY, TASK_COMPRESS, TASK_DECOMPRESS, TASK_CONVERT, TASK_PACK, TASK_SPLIT, TASK_OPTIMIZE:
		return true
	}
	return false
}

// RemainingText is the time left of a running task and about when it ends ("2h 15m · 18:40"),
// or "" while it is not known yet.
func (t Task) RemainingText() string {
	return t.RemainingTextAt(time.Now())
}

func (t Task) RemainingTextAt(now time.Time) string {
	left := t.RemainingAt(now)
	if left <= 0 {
		return ""
	}
	end := now.Add(left)
	clock := end.Format("15:04")
	// a task that ends another day says which
	if end.Format("2006-01-02") != now.Format("2006-01-02") {
		clock = end.Format("02/01 15:04")
	}
	return i18n.FormatDuration(left) + " · " + clock
}

// Remaining estimates the time left of a running task that reads files, or 0.
func (t Task) Remaining() time.Duration {
	return t.RemainingAt(time.Now())
}

func (t Task) RemainingAt(now time.Time) time.Duration {
	// the steps of a task on files go at very different speeds (hashes at hundreds of MB/s,
	// the max compression at a few): the average of all of them said 2 minutes for an hour
	if t.Estimated {
		if t.Paused || t.Left <= 0 {
			return 0
		}
		return max(t.Left-now.Sub(t.LeftAt), time.Minute).Round(time.Minute)
	}
	speed := t.SpeedAt(now)
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

// Log keeps the running and recent tasks and tells subscribers when they change.
type Log struct {
	mutex       sync.Mutex
	path        string
	tasks       []*Task // newest first
	nextId      int64
	version     int64
	subscribers map[chan int64]struct{}
	// told of each task that fails, in the background
	OnFailed func(Task)
	// told of each task that ends, as it ends
	OnEnded func(Task)
	// when the history was last written
	saved time.Time
}

// Load reads the history saved in the data folder. Tasks that were running when
// the app stopped are marked as failed.
func Load(dataFolder string) *Log {
	log := &Log{path: filepath.Join(dataFolder, TASKS_FILENAME), subscribers: map[chan int64]struct{}{}}
	// when the history was saved last: the app was still working then
	var saved time.Time
	if info, err := os.Stat(log.path); err == nil {
		saved = info.ModTime()
	}
	if data, ok := durable.ReadStore(log.path); ok {
		saved := []*Task{}
		if durable.ParseStore(log.path, data, &saved) == nil {
			log.tasks = saved
		}
	}
	for _, task := range log.tasks {
		if task.Id >= log.nextId {
			log.nextId = task.Id
		}
		// a check of the signatures of an older version counted the files not signed as damaged
		if task.Kind == TASK_VERIFY && task.Level == "" && task.Modified == 0 {
			for _, warning := range task.Warnings {
				if warning.Text == NOTE_VERIFY_UNSIGNED {
					task.Level, task.Modified, task.Damaged = VERIFY_LEVEL_SIGNATURE, task.Damaged, 0
					break
				}
			}
		}
		if task.Status == TASK_RUNNING {
			task.Status = TASK_FAILED
			task.Error = &Note{Text: NOTE_INTERRUPTED}
			// the moment the app stopped is not known: the last one it is known to have
			// worked on the task, instead of its start, which showed it lasted no time at all
			task.Finished = task.Started
			for _, at := range []time.Time{task.LeftAt, task.Beat, saved} {
				if at.After(task.Finished) {
					task.Finished = at
				}
			}
		}
	}
	return log
}

// Start records a new running task. It is saved at once: a task the app is stopped in the
// middle of is told as such once it starts again, instead of being gone.
func (l *Log) Start(kind string, trigger string) int64 {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.nextId++
	now := time.Now()
	task := &Task{Id: l.nextId, Kind: kind, Trigger: trigger, Status: TASK_RUNNING, Started: now, Beat: now, Total: -1, Current: -1}
	l.tasks = append([]*Task{task}, l.tasks...)
	l.changed(true)
	return task.Id
}

// Progress updates a running task. Progress is saved at most every half minute.
func (l *Log) Progress(id int64, current int, total int, message string) {
	l.ProgressArg(id, current, total, message, "")
}

// ProgressArg is Progress with a message "... %v" and what it names apart, so the message is
// translated.
func (l *Log) ProgressArg(id int64, current int, total int, message string, arg string) {
	l.update(id, false, func(task *Task) {
		task.Current, task.Total, task.Message, task.MessageArg = current, total, message, arg
	})
}

// ProgressLanes is Progress with the files the task works on, in one update.
func (l *Log) ProgressLanes(id int64, current int, total int, message string, lanes []Lane) {
	l.update(id, false, func(task *Task) {
		task.Current, task.Total, task.Message, task.MessageArg, task.Lanes = current, total, message, "", lanes
	})
}

// ProgressFiles is ProgressLanes for a task on files, with the file it reached out of how
// many, shown in a place of their own instead of after the name of the file.
func (l *Log) ProgressFiles(id int64, current int, total int, message string, number int, count int, lanes []Lane) {
	l.update(id, false, func(task *Task) {
		task.Current, task.Total, task.Message, task.MessageArg, task.Lanes = current, total, message, "", lanes
		task.FileNumber, task.FileCount = number, count
	})
}

// ProgressFilesLeft is ProgressFiles with the time left estimated from the speed of each step,
// or 0 while it is not known.
func (l *Log) ProgressFilesLeft(id int64, current int, total int, message string, number int, count int, lanes []Lane, left time.Duration) {
	l.update(id, false, func(task *Task) {
		task.Current, task.Total, task.Message, task.MessageArg, task.Lanes = current, total, message, "", lanes
		task.FileNumber, task.FileCount = number, count
		task.Estimated, task.Left, task.LeftAt = true, left, time.Now()
	})
}

// Lanes updates the files a running task is working on at the same time. Not saved to disk.
func (l *Log) Lanes(id int64, lanes []Lane) {
	l.update(id, false, func(task *Task) {
		task.Lanes = lanes
	})
}

// SetWorkers tells how many files a task works on at the same time, and with how many
// threads each one.
func (l *Log) SetWorkers(id int64, workers int, threads int) {
	l.update(id, false, func(task *Task) {
		task.Workers, task.Threads = workers, threads
	})
}

// Warn adds a problem that did not stop the task.
func (l *Log) Warn(id int64, text string, detail string) {
	l.update(id, false, func(task *Task) {
		task.Warnings = append(task.Warnings, Note{Text: text, Detail: detail})
	})
}

// SetResult records what the task found.
func (l *Log) SetResult(id int64, games int, files int) {
	l.update(id, false, func(task *Task) {
		task.Games, task.Files = games, files
	})
}

// SetCompressResult records the files compressed so far and the bytes saved. It is saved
// within half a minute, so a task the app is stopped in the middle of still says what it did.
func (l *Log) SetCompressResult(id int64, files int, saved int64) {
	l.update(id, false, func(task *Task) {
		task.Files, task.Saved = files, saved
	})
}

// SetResume records what a task on files still has to do; nil when it ended by itself or
// was cancelled, so it is not started again.
func (l *Log) SetResume(id int64, resume *Resume) {
	l.update(id, true, func(task *Task) {
		task.Resume = resume
	})
}

// Interrupted gives the last task on files the app was stopped in the middle of that still
// has files left, with what it had left; tasks running now are not looked at.
func (l *Log) Interrupted() (int64, string, *Resume) {
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

// SetContinued records that a task the app was stopped in the middle of went on in another.
func (l *Log) SetContinued(id int64, other int64) {
	l.update(id, true, func(task *Task) {
		task.ContinuedIn = other
	})
}

// SetPaused records that a running task is held, or goes on again.
func (l *Log) SetPaused(id int64, paused bool) {
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
func (l *Log) SetSubject(id int64, subject string) {
	l.update(id, false, func(task *Task) {
		task.Subject = subject
	})
}

// AddFileResult records a file compressed, with its size before and after.
func (l *Log) AddFileResult(id int64, name string, before int64, after int64) {
	l.update(id, false, func(task *Task) {
		task.Original += before
		task.Results = append(task.Results, Result{Name: name, Before: before, After: after})
		if len(task.Results) > maxTaskResults {
			task.Results = task.Results[len(task.Results)-maxTaskResults:]
		}
	})
}

// SetPlan records every file a task on files has to do, in its order, all waiting.
func (l *Log) SetPlan(id int64, names []string) {
	plan := make([]Tile, len(names))
	for i, name := range names {
		plan[i] = Tile{Name: name, Waiting: true}
	}
	l.update(id, false, func(task *Task) {
		task.Plan = plan
	})
}

// AddFileTile records how a file of a task on files ended, in its plan too: after the files
// ended before it, so the view by covers fills up in order, from the top left, even when
// several files are worked on at once and end in another order than the plan's.
func (l *Log) AddFileTile(id int64, name string, failed bool, note string) {
	l.addTile(id, Tile{Name: name, Failed: failed, Note: note})
}

// SkipFileTile records a file of a task on files left as it is, with why: neither done nor
// failed.
func (l *Log) SkipFileTile(id int64, name string, note string) {
	l.addTile(id, Tile{Name: name, Skipped: true, Note: note})
}

// ModifiedFileTile records a file of a verification that ended modified, with why: an NCA not
// signed by Nintendo and nothing damaged, which is no failure.
func (l *Log) ModifiedFileTile(id int64, name string, note string) {
	l.addTile(id, Tile{Name: name, Modified: true, Note: note})
}

func (l *Log) addTile(id int64, tile Tile) {
	name := tile.Name
	l.update(id, false, func(task *Task) {
		task.Tiles = append(task.Tiles, tile)
		if len(task.Tiles) > maxTaskResults {
			task.Tiles = task.Tiles[len(task.Tiles)-maxTaskResults:]
		}
		ended := 0
		for ended < len(task.Plan) && !task.Plan[ended].Waiting {
			ended++
		}
		for i := ended; i < len(task.Plan); i++ {
			if task.Plan[i].Waiting && task.Plan[i].Name == name {
				copy(task.Plan[ended+1:i+1], task.Plan[ended:i])
				task.Plan[ended] = tile
				break
			}
		}
	})
}

// SetBreakdown records why the files of a verification are checked.
func (l *Log) SetBreakdown(id int64, breakdown VerifyBreakdown) {
	l.update(id, false, func(task *Task) {
		task.Breakdown = &breakdown
	})
}

// SetVerifyResult records the files checked so far, the failed ones (damaged) and the modified
// ones.
func (l *Log) SetVerifyResult(id int64, checked int, failed int, modified int) {
	l.update(id, false, func(task *Task) {
		task.Files, task.Damaged, task.Modified = checked, failed, modified
	})
}

// SetVerifyLevel records the level of a verification, and whether its progress counts files
// instead of megabytes.
func (l *Log) SetVerifyLevel(id int64, level string, byFiles bool) {
	l.update(id, false, func(task *Task) {
		task.Level, task.ByFiles = level, byFiles
	})
}

// Finish ends a task; a failure makes it failed, warnings make it a warning.
func (l *Log) Finish(id int64, failure *Note) {
	var failed, ended *Task
	defer func() {
		if ended != nil && l.OnEnded != nil {
			l.OnEnded(*ended)
		}
		if failed != nil && l.OnFailed != nil {
			go l.OnFailed(*failed)
		}
	}()
	l.update(id, true, func(task *Task) {
		defer func() {
			copied := *task
			ended = &copied
			if task.Status == TASK_FAILED {
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
func (l *Log) Dismiss(id int64) bool {
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
func (l *Log) RunningCount() int {
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
func (l *Log) ClearFinished() {
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
func (l *Log) Snapshot() []Task {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	tasks := make([]Task, 0, len(l.tasks))
	for _, task := range l.tasks {
		copied := *task
		copied.Warnings = append([]Note(nil), task.Warnings...)
		copied.Tiles = append([]Tile(nil), task.Tiles...)
		copied.Plan = append([]Tile(nil), task.Plan...)
		tasks = append(tasks, copied)
	}
	return tasks
}

// FailedCount counts the failed tasks that were not dismissed: not the ones cancelled or
// stopped by a restart, which are no failure.
func (l *Log) FailedCount() int {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	count := 0
	for _, task := range l.tasks {
		if task.Status == TASK_FAILED && task.Ending() == "" {
			count++
		}
	}
	return count
}

// Subscribe returns a channel that receives the new version after every change, and a
// function to stop. Changes are coalesced: a slow reader only gets the latest version.
func (l *Log) Subscribe() (<-chan int64, func()) {
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
func (l *Log) Touch() {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.changed(false)
}

// Edit changes the tasks as they are, without noting that they were heard of nor saving
// them: the tests make up tasks of the past with it.
func (l *Log) Edit(change func(tasks []*Task) []*Task) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.tasks = change(l.tasks)
}

// Flush writes the history as it is now: the app is about to stop.
func (l *Log) Flush() {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.save()
}

func (l *Log) Version() int64 {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.version
}

func (l *Log) update(id int64, save bool, change func(task *Task)) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	for _, task := range l.tasks {
		if task.Id == id {
			change(task)
			if task.Status == TASK_RUNNING {
				task.Beat = time.Now()
			}
			l.changed(save)
			return
		}
	}
}

// changed must be called with the mutex held. A change not saved is saved anyway once the
// history was not written for half a minute, with the running tasks as they are.
func (l *Log) changed(save bool) {
	save = save || time.Since(l.saved) >= taskSaveInterval
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

// trim drops the oldest finished tasks beyond the limit, but keeps failed ones, and the ones a
// restart stopped that still have files to go on with. A task cancelled or stopped by a restart
// is no failure, and goes like the others.
func (l *Log) trim() {
	kept := []*Task{}
	finished := 0
	for _, task := range l.tasks {
		failed := task.Status == TASK_FAILED && task.Ending() == ""
		if task.Status != TASK_RUNNING && !failed && task.Resume == nil {
			finished++
			if finished > maxFinishedTasks {
				continue
			}
		}
		kept = append(kept, task)
	}
	l.tasks = kept
}

func (l *Log) save() {
	if l.path == "" {
		return
	}
	data, err := json.MarshalIndent(l.tasks, "", " ")
	if err != nil {
		return
	}
	l.saved = time.Now()
	durable.WriteFile(l.path, data)
}

// the passes over a file, translated by the interface
const (
	PHASE_HASHING       = "Calculating hashes"
	PHASE_COMPRESSING   = "Compressing"
	PHASE_DECOMPRESSING = "Decompressing"
	PHASE_CONVERTING    = "Converting"
	PHASE_PACKING       = "Combining the files"
	PHASE_OPTIMIZING    = "Optimizing for install"
	PHASE_VERIFYING     = "Verifying"
)

// the levels of a check: the signature of Nintendo on the header of every NCA, read in
// seconds; the hashes of all their data, read whole; or both, what a check was before it had
// levels
const (
	VERIFY_LEVEL_SIGNATURE = "signature"
	VERIFY_LEVEL_HASH      = "hash"
	VERIFY_LEVEL_BOTH      = "both"
)

// NotWorthTelling are the ends that are not failures to tell: what the user stopped.
var NotWorthTelling = map[string]bool{NOTE_COMPRESS_CANCELED: true, NOTE_COMPRESS_CANCELED_OLD: true, NOTE_UPLOAD_CANCELED: true, NOTE_UPLOAD_STOPPED: true}

var titleIdInName = regexp.MustCompile(`\[([0-9A-Fa-f]{16})\]`)

// ContentKind tells from the title ID in the name of a file whether it is a game, an update or
// a DLC, as the Compress page names them; "" when the name has none.
func ContentKind(path string) string {
	match := titleIdInName.FindStringSubmatch(filepath.Base(path))
	if match == nil {
		return ""
	}
	id := strings.ToLower(match[1])
	switch {
	case strings.HasSuffix(id, "000"):
		return "Base"
	case strings.HasSuffix(id, "800"):
		return "Update"
	}
	return "DLC"
}
