package web

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
)

// How the installs over the network go, for the Tasks page and for Settings › Install over the
// network: what each console is sent, as it is sent.
//
//   - With DBI and Tinfoil (and the installers that read the index of Tinfoil) the app counts what
//     it serves of each file (serveNetFile): the share the console has, the speed now and on
//     average, and the time left. DBI asks for a file in pieces, sometimes twice: the pieces are
//     put together by console and file, so a piece asked again is not counted twice. A file the
//     console stops asking for before it has it whole is cut once a minute goes by.
//   - With NXDock its queue says what it does (netqueue.go): the stage of each job, the jobs
//     waiting, and Pause and Cancel.
//   - A console that has asked for nothing for a minute, with nothing left to send, has ended its
//     run: the run is kept in the history of the tasks (tasklog.TASK_NET_INSTALL), and under the
//     console in Settings for half an hour.
//
// It is only shown: the queue of the tasks on files does not wait for it, nor does it wait for
// them. An update of the app waits for a console that is installing, as for a task.

// times of the runs; variables for the tests
var (
	// a file not asked for this long is cut
	netSendCutAfter = time.Minute
	// a run with nothing left to send, not heard of this long, has ended
	netSendEndAfter = time.Minute
	// how long Settings shows a run that ended
	netSendKeepEnded = 30 * time.Minute
	// how often the runs going on are looked at, to cut and end them
	netSendSweepEvery = 5 * time.Second
)

const (
	// the speed now is what was sent in this last while
	netSendSpeedWindow = 5 * time.Second
	// the pages of the tasks are told at most this often while bytes flow
	netSendTouchEvery = time.Second
	// at most so many files in a run, and runs kept that ended
	netSendFilesMax = 500
	netSendEndedMax = 50
)

// the installers a run is told by
const (
	NET_VIA_DBI     = "DBI"
	NET_VIA_TINFOIL = "Tinfoil"
	NET_VIA_NXDOCK  = "NXDock"
)

// netSpan is a piece of a file sent: from its first byte to the one after its last.
type netSpan struct{ from, to int64 }

// addNetSpan puts a piece with the pieces sent, joined to those it touches, in order.
func addNetSpan(spans []netSpan, piece netSpan) []netSpan {
	if piece.to <= piece.from {
		return spans
	}
	out := make([]netSpan, 0, len(spans)+1)
	placed := false
	for _, span := range spans {
		switch {
		case span.to < piece.from:
			out = append(out, span)
		case piece.to < span.from:
			if !placed {
				out = append(out, piece)
				placed = true
			}
			out = append(out, span)
		default:
			piece = netSpan{from: min(piece.from, span.from), to: max(piece.to, span.to)}
		}
	}
	if !placed {
		out = append(out, piece)
	}
	return out
}

// netSpansSize is how many bytes the pieces hold.
func netSpansSize(spans []netSpan) int64 {
	var size int64
	for _, span := range spans {
		size += span.to - span.from
	}
	return size
}

type netSample struct {
	at    time.Time
	bytes int64
}

// netSendFile is a file of a run: served to DBI or Tinfoil, or a job of the queue of NXDock.
type netSendFile struct {
	// the id of the file served, or the number of the job of NXDock
	id    string
	job   int
	name  string
	kind  string
	label string
	cover string
	size  int64
	// what of the file was sent, and the bytes written with what was sent twice
	spans   []netSpan
	done    int64
	written int64
	samples []netSample
	// NXDock tells these itself
	percent float64
	speed   float64
	left    int
	stage   string
	err     string
	// waiting, running, or how it ended (tasklog.SENT_*)
	state                string
	started, last, ended time.Time
}

func (f *netSendFile) running() bool {
	return f.state == "running" || f.state == "waiting"
}

// speedAt is the speed of a file now, in MB/s.
func (f *netSendFile) speedAt(now time.Time) float64 {
	if f.job != 0 {
		if f.state != "running" {
			return 0
		}
		return f.speed
	}
	if f.state != "running" || len(f.samples) == 0 || now.Sub(f.samples[len(f.samples)-1].at) > 2*time.Second+netSendSpeedWindow/2 {
		return 0
	}
	first := f.samples[0]
	for _, sample := range f.samples {
		if now.Sub(sample.at) <= netSendSpeedWindow {
			break
		}
		first = sample
	}
	seconds := now.Sub(first.at).Seconds()
	if seconds < 1 {
		return 0
	}
	return float64(f.written-first.bytes) / 1048576 / seconds
}

// seconds is how long the file took, or has taken so far.
func (f *netSendFile) seconds(now time.Time) float64 {
	if f.started.IsZero() {
		return 0
	}
	end := f.last
	if !f.ended.IsZero() {
		end = f.ended
	} else if f.job != 0 || f.state == "running" {
		end = now
	}
	return max(0, end.Sub(f.started).Seconds())
}

// average is the speed of the file over the time it took, in MB/s.
func (f *netSendFile) average(now time.Time) float64 {
	seconds := f.seconds(now)
	if seconds < 1 {
		return 0
	}
	return float64(f.done) / 1048576 / seconds
}

// netSendRun is what a console was sent, from its first request until it asked for nothing for
// a while.
type netSendRun struct {
	// the console: the id and name of its key (or its user, without a key), and its user
	id, console, user, via string
	started, last          time.Time
	// the last time the queue of NXDock was heard of, and whether it is paused
	heard  time.Time
	paused bool
	ended  time.Time
	files  []*netSendFile
}

func (run *netSendRun) running() bool {
	for _, file := range run.files {
		if file.running() {
			return true
		}
	}
	return false
}

func (run *netSendRun) file(id string, job int) *netSendFile {
	for _, file := range run.files {
		if file.id == id && file.job == job {
			return file
		}
	}
	return nil
}

// netSends are the runs going on, by console, and the ones that ended lately.
type netSends struct {
	mutex    sync.Mutex
	live     map[string]*netSendRun
	ended    []*netSendRun
	sweeping bool
	touched  time.Time
}

// netSendWho is the console of a request: the id and the name of its key, or its user.
func netSendWho(principal *Principal) (string, string) {
	if principal.KeyID != "" {
		name := principal.KeyName
		if name == "" {
			name = principal.Name
		}
		return principal.KeyID, name
	}
	return "user:" + principal.Name, principal.Name
}

// run gives the run of a console going on, or starts one.
func (s *netSends) run(id, console, user, via string, now time.Time) *netSendRun {
	if s.live == nil {
		s.live = map[string]*netSendRun{}
	}
	run := s.live[id]
	if run == nil {
		run = &netSendRun{id: id, console: console, user: user, via: via, started: now, last: now}
		s.live[id] = run
	}
	return run
}

// served notes that a piece of a file was sent to a console, from offset; it tells whether the
// pages should be told.
func (s *netSends) served(id, console, user, via string, file netFile, offset int64, n int, now time.Time) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	run := s.run(id, console, user, via, now)
	if run.via == NET_VIA_NXDOCK {
		// its queue tells what it does
		return false
	}
	sent := run.file(file.id, 0)
	changed := false
	if sent == nil {
		if len(run.files) >= netSendFilesMax {
			return false
		}
		sent = &netSendFile{id: file.id, size: file.size, state: "running", started: now}
		sent.name, sent.kind, sent.label = netSendNames(file)
		if file.cover != "" {
			sent.cover = thumbUrl("/i/" + file.cover)
		}
		run.files = append(run.files, sent)
		changed = true
	}
	if sent.state != "running" {
		// asked again after it was cut, or once more after it was sent whole
		sent.state, sent.ended, changed = "running", time.Time{}, true
	}
	sent.spans = addNetSpan(sent.spans, netSpan{from: offset, to: offset + int64(n)})
	sent.done = min(netSpansSize(sent.spans), sent.size)
	sent.written += int64(n)
	sent.last, run.last = now, now
	sent.samples = append(sent.samples, netSample{at: now, bytes: sent.written})
	for len(sent.samples) > 2 && now.Sub(sent.samples[1].at) > netSendSpeedWindow {
		sent.samples = sent.samples[1:]
	}
	if sent.size > 0 && sent.done >= sent.size {
		sent.state, sent.ended, sent.samples, changed = tasklog.SENT_DONE, now, nil, true
	}
	if changed || now.Sub(s.touched) >= netSendTouchEvery {
		s.touched = now
		return true
	}
	return false
}

// netSendNames are the name of a file served as a run shows it, what it holds, and its label: the
// version of an update, the name of a DLC.
func netSendNames(file netFile) (string, string, string) {
	name := file.game
	if name == "" {
		name = file.name
	}
	switch file.kind {
	case netFolderUpdates:
		return name, "update", "v" + strconv.Itoa(file.version)
	case netFolderDlc:
		label := file.dlcName
		if label == "" {
			label = file.name
		}
		return name, "dlc", label
	}
	return name, "game", ""
}

// queue notes what the queue of NXDock on a console does; cover gives the cover of a game by its
// name.
func (s *netSends) queue(id, console, user string, live netQueueLive, cover func(string) string, now time.Time) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	run := s.live[id]
	working := false
	for _, job := range live.Jobs {
		working = working || job.State == "running" || job.State == "waiting"
	}
	if run == nil && !working {
		// a queue that has nothing to do: nothing to show
		return
	}
	run = s.run(id, console, user, NET_VIA_NXDOCK, now)
	if run.via != NET_VIA_NXDOCK {
		// it downloaded before it told its queue: the queue tells it better
		run.via, run.files = NET_VIA_NXDOCK, nil
	}
	run.heard, run.paused = now, live.Paused
	seen := map[int]bool{}
	for _, job := range live.Jobs {
		seen[job.Id] = true
		file := run.file("", job.Id)
		if file == nil {
			if len(run.files) >= netSendFilesMax {
				continue
			}
			file = &netSendFile{job: job.Id, name: job.Game, label: job.Label, size: job.Size, state: "waiting"}
			if cover != nil {
				file.cover = cover(job.Game)
			}
			run.files = append(run.files, file)
		}
		file.size = max(file.size, job.Size)
		file.percent, file.speed, file.left, file.stage = job.Percent, job.Speed, job.Left, job.Stage
		if file.size > 0 {
			file.done = int64(job.Percent / 100 * float64(file.size))
		}
		state := job.State
		if state != "waiting" && file.started.IsZero() {
			file.started = now
		}
		switch state {
		case "done":
			file.done = file.size
			state = tasklog.SENT_DONE
		case "failed":
			file.err = job.Error
			state = tasklog.SENT_FAILED
		case "cancelled":
			state = tasklog.SENT_CANCELLED
		}
		if state != file.state {
			if state != "running" && state != "waiting" {
				file.ended = now
			}
			file.state, run.last = state, now
		}
		if file.running() {
			file.last, run.last = now, now
		}
	}
	// a job NXDock no longer tells of ended as far as it went
	for _, file := range run.files {
		if file.job != 0 && !seen[file.job] && file.running() {
			file.state, file.ended = tasklog.SENT_CUT, now
			if file.percent >= 99.5 {
				file.state = tasklog.SENT_DONE
			}
		}
	}
}

// end ends the run of a console now, as the queue of NXDock tells it ended; it gives the run.
func (s *netSends) end(id string, now time.Time) *netSendRun {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	run := s.live[id]
	if run == nil || run.via != NET_VIA_NXDOCK {
		return nil
	}
	return s.close(run, now)
}

// close ends a run, cutting what was still going; it is called with the mutex held.
func (s *netSends) close(run *netSendRun, now time.Time) *netSendRun {
	for _, file := range run.files {
		if file.running() {
			file.state, file.ended = tasklog.SENT_CUT, file.last
			if file.ended.IsZero() {
				file.ended = now
			}
		}
	}
	run.ended = run.last
	delete(s.live, run.id)
	s.ended = append(s.ended, run)
	if len(s.ended) > netSendEndedMax {
		s.ended = s.ended[len(s.ended)-netSendEndedMax:]
	}
	return run
}

// sweep cuts the files the consoles stopped asking for and ends the runs that have nothing left;
// it gives the runs that ended, and whether any run goes on.
func (s *netSends) sweep(now time.Time) ([]*netSendRun, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	ended := []*netSendRun{}
	for _, run := range s.live {
		if run.via == NET_VIA_NXDOCK {
			if now.Sub(run.heard) > netSendCutAfter {
				ended = append(ended, s.close(run, now))
			} else if !run.running() && now.Sub(run.last) > netSendEndAfter {
				ended = append(ended, s.close(run, now))
			}
			continue
		}
		for _, file := range run.files {
			if file.state == "running" && now.Sub(file.last) > netSendCutAfter {
				file.state, file.ended, file.samples = tasklog.SENT_CUT, file.last, nil
			}
		}
		if !run.running() && now.Sub(run.last) > netSendEndAfter {
			ended = append(ended, s.close(run, now))
		}
	}
	kept := s.ended[:0]
	for _, run := range s.ended {
		if now.Sub(run.ended) <= netSendKeepEnded {
			kept = append(kept, run)
		}
	}
	s.ended = kept
	sort.Slice(ended, func(i, j int) bool { return ended[i].started.Before(ended[j].started) })
	return ended, len(s.live) > 0
}

// NetSendFileView is a file of a run, as the pages show it.
type NetSendFileView struct {
	Job   int    `json:"job,omitempty"`
	Name  string `json:"name"`
	Kind  string `json:"kind,omitempty"`
	Label string `json:"label,omitempty"`
	Cover string `json:"cover,omitempty"`
	// waiting, running, or how it ended (tasklog.SENT_*)
	State string `json:"state"`
	// what NXDock does with it: copy, network, decompress, write, check, register
	Stage   string  `json:"stage,omitempty"`
	Error   string  `json:"error,omitempty"`
	Size    int64   `json:"size"`
	Done    int64   `json:"done"`
	Speed   float64 `json:"speed"`
	Average float64 `json:"average"`
	Left    int     `json:"left,omitempty"`
	Seconds float64 `json:"seconds"`
}

// Percent is how much of the file was sent, to one decimal.
func (f NetSendFileView) Percent() float64 {
	if f.Size <= 0 {
		if f.State == tasklog.SENT_DONE {
			return 100
		}
		return 0
	}
	return min(100, float64(int(float64(f.Done)/float64(f.Size)*1000))/10)
}

// Tone is the colour of the stage NXDock is in, as the steps of the tasks on files have theirs.
func (f NetSendFileView) Tone() string {
	switch f.Stage {
	case "network", "copy":
		return "cyan"
	case "decompress", "write":
		return "yellow"
	case "check", "register":
		return "green"
	}
	return ""
}

// StageName is the stage NXDock is in, in English to be translated.
func (f NetSendFileView) StageName() string {
	return netStageNames[f.Stage]
}

var netStageNames = map[string]string{"copy": "Copying the save", "network": "Downloading", "decompress": "Decompressing",
	"write": "Writing", "check": "Checking", "register": "Registering"}

// LeftText is the time left of the file, as "2m 05s".
func (f NetSendFileView) LeftText() string {
	if f.Left <= 0 {
		return ""
	}
	return tasklog.ShortDuration(time.Duration(f.Left) * time.Second)
}

// NetSendView is a run of a console, as the pages show it.
type NetSendView struct {
	Id      string `json:"id"`
	Console string `json:"console"`
	User    string `json:"user"`
	// DBI, Tinfoil or NXDock
	Via     string    `json:"via"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
	Running bool      `json:"running"`
	Paused  bool      `json:"paused,omitempty"`
	// NXDock has not said what it does for a while
	Gone  bool              `json:"gone,omitempty"`
	Files []NetSendFileView `json:"files"`
	// all the files: their size, what was sent, the speed now, and the average and time of each
	Size    int64   `json:"size"`
	Done    int64   `json:"done"`
	Speed   float64 `json:"speed"`
	Average float64 `json:"average"`
	Left    int     `json:"left,omitempty"`
	Seconds float64 `json:"seconds"`
	// how the files ended
	Ok        int `json:"ok"`
	Cut       int `json:"cut"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	// on the Tasks page: the console is of who looks, who may pause and cancel what NXDock does
	Mine bool `json:"-"`
}

// Percent is the share of all the files sent, to two decimals as the bars of the tasks have it.
func (v NetSendView) Percent() float64 {
	if v.Size <= 0 {
		return 0
	}
	return min(100, float64(int(float64(v.Done)/float64(v.Size)*10000))/100)
}

// PercentText is Percent for the width of a bar.
func (v NetSendView) PercentText() string {
	return strconv.FormatFloat(v.Percent(), 'f', 2, 64)
}

// Number is the file the run is at, counting those ended; Count, the files it knows of.
func (v NetSendView) Number() int {
	number := 0
	for _, file := range v.Files {
		if file.State != "waiting" {
			number++
		}
	}
	return number
}

// Current is the file being sent: the first running, or nil.
func (v NetSendView) Current() *NetSendFileView {
	for i := range v.Files {
		if v.Files[i].State == "running" {
			return &v.Files[i]
		}
	}
	return nil
}

// LeftText is the time left of the run, as "2m 05s".
func (v NetSendView) LeftText() string {
	if v.Left <= 0 {
		return ""
	}
	return tasklog.ShortDuration(time.Duration(v.Left) * time.Second)
}

// view is a run as the pages show it; called with the mutex held.
func (run *netSendRun) view(now time.Time) NetSendView {
	view := NetSendView{Id: run.id, Console: run.console, User: run.user, Via: run.via, Started: run.started, Ended: run.ended,
		Running: run.ended.IsZero(), Paused: run.paused, Files: make([]NetSendFileView, 0, len(run.files))}
	if run.via == NET_VIA_NXDOCK && view.Running {
		view.Gone = now.Sub(run.heard) > netQueueStale
	}
	waiting, left := 0.0, 0
	for _, file := range run.files {
		item := NetSendFileView{Job: file.job, Name: file.name, Kind: file.kind, Label: file.label, Cover: file.cover, State: file.state,
			Stage: file.stage, Error: file.err, Size: file.size, Done: file.done, Speed: file.speedAt(now), Average: file.average(now),
			Seconds: file.seconds(now)}
		if file.state != "running" {
			item.Stage = ""
		}
		if file.state == "running" {
			switch {
			case file.job != 0 && file.left > 0:
				item.Left = file.left
			case item.Speed > 0:
				item.Left = int(float64(file.size-file.done) / 1048576 / item.Speed)
			}
		}
		view.Files = append(view.Files, item)
		view.Size += file.size
		view.Done += file.done
		view.Speed += item.Speed
		view.Seconds += item.Seconds
		switch file.state {
		case tasklog.SENT_DONE:
			view.Ok++
		case tasklog.SENT_CUT:
			view.Cut++
		case tasklog.SENT_FAILED:
			view.Failed++
		case tasklog.SENT_CANCELLED:
			view.Cancelled++
		case "waiting":
			waiting += float64(file.size) / 1048576
		}
		left += item.Left
	}
	if view.Seconds >= 1 {
		view.Average = float64(view.Done) / 1048576 / view.Seconds
	}
	// with what waits, at the speed of now
	view.Left = left
	if view.Speed > 0 {
		view.Left += int(waiting / view.Speed)
	}
	return view
}

// views are the runs going on and those that ended lately (the newest first), of all consoles.
func (s *netSends) views(now time.Time) ([]NetSendView, []NetSendView) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	live := make([]NetSendView, 0, len(s.live))
	for _, run := range s.live {
		if len(run.files) > 0 {
			live = append(live, run.view(now))
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].Started.Before(live[j].Started) })
	ended := make([]NetSendView, 0, len(s.ended))
	for i := len(s.ended) - 1; i >= 0; i-- {
		ended = append(ended, s.ended[i].view(now))
	}
	return live, ended
}

// count is how many runs go on, for the update to wait for them.
func (s *netSends) count() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return len(s.live)
}

// netSendTask is a run that ended, as the history of the tasks keeps it; ok is false for a run
// that sent nothing.
func netSendTask(run *netSendRun) (tasklog.Task, bool) {
	view := run.view(run.ended)
	task := tasklog.Task{Kind: tasklog.TASK_NET_INSTALL, Trigger: tasklog.TRIGGER_CONSOLE, Started: run.started, Finished: run.ended,
		Console: run.console, Via: run.via, Total: -1, Current: -1, Files: view.Ok}
	if task.Finished.Before(task.Started) {
		task.Finished = task.Started
	}
	sent := false
	for _, file := range view.Files {
		if file.Done > 0 || file.State != tasklog.SENT_CUT {
			sent = true
		}
		task.Sent = append(task.Sent, tasklog.SentFile{Name: file.Name, Kind: file.Kind, Label: file.Label, Cover: file.Cover, Size: file.Size,
			Done: file.Done, Seconds: file.Seconds, State: file.State, Error: file.Error})
	}
	switch {
	case view.Failed > 0 && view.Ok == 0:
		task.Status, task.Error = tasklog.TASK_FAILED, &tasklog.Note{Text: tasklog.NOTE_NET_FAILED}
	case view.Failed > 0 || view.Cut > 0:
		task.Status = tasklog.TASK_WARNING
	case view.Ok == 0 && view.Cancelled > 0:
		task.Status, task.Error = tasklog.TASK_FAILED, &tasklog.Note{Text: tasklog.NOTE_NET_CANCELLED}
	default:
		task.Status = tasklog.TASK_SUCCESS
	}
	return task, sent
}

// netSendChanged tells the pages of the tasks that a run changed.
func (web *Web) netSendChanged() {
	web.taskLog().Touch()
}

// netSendEnded keeps the runs that ended in the history of the tasks.
func (web *Web) netSendEnded(runs []*netSendRun) {
	for _, run := range runs {
		if task, ok := netSendTask(run); ok && !isDemoMode() {
			web.taskLog().Record(task)
		}
	}
	if len(runs) > 0 {
		web.netSendChanged()
	}
}

// netSendSweep looks at the runs every few seconds while any goes on.
func (web *Web) netSendSweep() {
	web.netSends.mutex.Lock()
	if web.netSends.sweeping {
		web.netSends.mutex.Unlock()
		return
	}
	web.netSends.sweeping = true
	web.netSends.mutex.Unlock()
	go func() {
		ticker := time.NewTicker(netSendSweepEvery)
		defer ticker.Stop()
		for {
			select {
			case <-web.closingPages():
				web.netSends.mutex.Lock()
				web.netSends.sweeping = false
				web.netSends.mutex.Unlock()
				return
			case <-ticker.C:
			}
			ended, going := web.netSends.sweep(time.Now())
			web.netSendEnded(ended)
			if going {
				// the speed now and the consoles that do not answer
				web.netSendChanged()
				continue
			}
			web.netSends.mutex.Lock()
			if len(web.netSends.live) == 0 {
				web.netSends.sweeping = false
				web.netSends.mutex.Unlock()
				return
			}
			web.netSends.mutex.Unlock()
		}
	}()
}

// netCoverOfGame is the cover of a game of the list the consoles are given, by its name.
func (web *Web) netCoverOfGame(name string) string {
	web.netList.mutex.Lock()
	defer web.netList.mutex.Unlock()
	for _, file := range web.netList.files {
		if file.cover != "" && strings.EqualFold(file.game, name) {
			return thumbUrl("/i/" + file.cover)
		}
	}
	return ""
}

// netSendWriter counts what a request for a file sends to the console.
type netSendWriter struct {
	http.ResponseWriter
	web          *Web
	id, console  string
	user, via    string
	file         netFile
	offset       int64
	status       int
	wroteHeaders bool
}

func (w *netSendWriter) WriteHeader(status int) {
	if !w.wroteHeaders {
		w.status, w.wroteHeaders = status, true
		if status == http.StatusOK {
			w.offset = 0
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *netSendWriter) Write(p []byte) (int, error) {
	if !w.wroteHeaders {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	if n > 0 && (w.status == http.StatusOK || w.status == http.StatusPartialContent) {
		if w.web.netSends.served(w.id, w.console, w.user, w.via, w.file, w.offset, n, time.Now()) {
			w.web.netSendChanged()
			w.web.netSendSweep()
		}
		w.offset += int64(n)
	}
	return n, err
}

func (w *netSendWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// netRangeStart is where the one piece a request asks for starts; ok is false for a request of
// several pieces, which is not counted.
func netRangeStart(header string, size int64) (int64, bool) {
	if header == "" {
		return 0, true
	}
	spec, found := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, false
	}
	from, _, found := strings.Cut(spec, "-")
	if !found {
		return 0, false
	}
	if from == "" {
		// the last bytes
		_, last, _ := strings.Cut(spec, "-")
		n, err := strconv.ParseInt(strings.TrimSpace(last), 10, 64)
		if err != nil || n <= 0 {
			return 0, false
		}
		return max(0, size-n), true
	}
	start, err := strconv.ParseInt(strings.TrimSpace(from), 10, 64)
	if err != nil || start < 0 {
		return 0, false
	}
	return start, true
}

// netSendCounted wraps the writer of a request for a file so what it sends is counted.
func (web *Web) netSendCounted(w http.ResponseWriter, r *http.Request, file netFile) http.ResponseWriter {
	if r.Method != http.MethodGet || isDemoMode() {
		return w
	}
	start, ok := netRangeStart(r.Header.Get("Range"), file.size)
	if !ok {
		return w
	}
	principal := principalFrom(r)
	id, console := netSendWho(principal)
	via := NET_VIA_TINFOIL
	if strings.HasPrefix(r.URL.Path, netInstallDbi) {
		via = NET_VIA_DBI
	}
	return &netSendWriter{ResponseWriter: w, web: web, id: id, console: console, user: principal.Name, via: via, file: file, offset: start}
}
