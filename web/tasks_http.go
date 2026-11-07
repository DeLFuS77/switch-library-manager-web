package web

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
)

type TasksPageData struct {
	GlobalPageData
	Running  []tasklog.Task
	Finished []TaskGroup
	// the tasks on files waiting for their turn
	Queue []QueuedView
	// what waits while a task on files is paused
	Waits PausedWaits
	// the version of the task list, so the page knows when to refresh
	ListVersion int64
}

// PausedWaits is what waits while a task on files is paused, as it holds the files: the tasks of
// the queue, a scheduled synchronization that is due, the new files of the automations.
type PausedWaits struct {
	Queue      int
	Sync       bool
	Automation int
}

// Text is what waits, in a sentence of the language: "2 tasks in the queue, the scheduled
// synchronization and 4 new files of the automations"; "" when nothing waits.
func (w PausedWaits) Text(lang string) string {
	parts := []string{}
	switch {
	case w.Queue == 1:
		parts = append(parts, i18n.Translate(lang, "1 task in the queue"))
	case w.Queue > 1:
		parts = append(parts, i18n.Translatef(lang, "%v tasks in the queue", i18n.FormatNumber(lang, w.Queue)))
	}
	if w.Sync {
		parts = append(parts, i18n.Translate(lang, "the scheduled synchronization"))
	}
	switch {
	case w.Automation == 1:
		parts = append(parts, i18n.Translate(lang, "1 new file of the automations"))
	case w.Automation > 1:
		parts = append(parts, i18n.Translatef(lang, "%v new files of the automations", i18n.FormatNumber(lang, w.Automation)))
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return i18n.Translatef(lang, "%v and %v", strings.Join(parts[:len(parts)-1], ", "), parts[len(parts)-1])
}

// pausedWaits is what waits now while a task on files is paused.
func (web *Web) pausedWaits() PausedWaits {
	appSettings := settings.ReadSettings(web.dataFolder)
	waits := PausedWaits{Queue: len(web.queue().list()), Sync: syncDue(appSettings, time.Now())}
	if appSettings.Automation.Any() {
		waits.Automation = web.automationPending()
	}
	return waits
}

func (web *Web) tasksPageData() TasksPageData {
	data := web.taskListData()
	data.GlobalPageData = web.globalPageData("tasks")
	return data
}

// taskListData is the list of tasks alone, refreshed by the page while tasks run: it needs
// none of the header.
func (web *Web) taskListData() TasksPageData {
	data := TasksPageData{Running: []tasklog.Task{}, ListVersion: web.taskLog().Version()}
	finished := []tasklog.Task{}
	paused := false
	for _, task := range web.taskLog().Snapshot() {
		if task.Running() {
			data.Running = append(data.Running, web.withCovers(task))
			paused = paused || task.Paused
		} else {
			finished = append(finished, web.withCovers(task))
		}
	}
	if paused {
		data.Waits = web.pausedWaits()
	}
	data.Finished = groupTasks(finished)
	data.Queue = web.queuedViews()
	return data
}

// maxShownTiles is how many files of a running task the view by covers shows at once: a check
// of the whole library has thousands.
const maxShownTiles = 240

// withCovers gives a task on files with the cover of the game of each file it works on and
// each file it ended, and the next file it takes from those it has left.
func (web *Web) withCovers(task tasklog.Task) tasklog.Task {
	if len(task.Lanes) == 0 && len(task.Tiles) == 0 && len(task.Plan) == 0 && task.Resume == nil {
		return task
	}
	covers := web.fileCovers()
	// the tiles of the snapshot are its own copy
	for i := range task.Tiles {
		task.Tiles[i].Cover = covers[task.Tiles[i].Name]
	}
	task.Plan, task.HiddenAfter = shownPlan(task.Plan, task.Lanes, covers)
	// the lanes of the snapshot are shared with the running task: a copy is changed
	lanes := make([]tasklog.Lane, len(task.Lanes))
	working := map[string]bool{}
	for i, lane := range task.Lanes {
		lane.Cover = covers[lane.Name]
		lanes[i] = lane
		working[lane.Name] = true
	}
	task.Lanes = lanes
	if task.Resume != nil && !task.Paused {
		for _, path := range task.Resume.Paths {
			if name := filepath.Base(path); !working[name] {
				task.Next = &tasklog.Lane{Name: name, Cover: covers[name]}
				break
			}
		}
	}
	return task
}

// shownPlan is the part of the plan of a running task the view by covers shows, each file with
// its cover and, at work, its progress, and how many files are left out after it: a few of the
// files ended, then those at work and those waiting. The files ended before them are left out.
func shownPlan(plan []tasklog.Tile, lanes []tasklog.Lane, covers map[string]string) ([]tasklog.Tile, int) {
	if len(plan) == 0 {
		return nil, 0
	}
	working := map[string]tasklog.Lane{}
	for _, lane := range lanes {
		if lane.Name != "" {
			working[lane.Name] = lane
		}
	}
	// the files at work come right after the ended ones, before those waiting, so the view
	// fills up in order (AddFileTile puts the ended files first)
	ordered := make([]tasklog.Tile, 0, len(plan))
	for _, tile := range plan {
		if !tile.Waiting {
			ordered = append(ordered, tile)
		}
	}
	first := len(ordered)
	for _, tile := range plan {
		if _, ok := working[tile.Name]; ok && tile.Waiting {
			ordered = append(ordered, tile)
		}
	}
	for _, tile := range plan {
		if _, ok := working[tile.Name]; !ok && tile.Waiting {
			ordered = append(ordered, tile)
		}
	}
	plan = ordered
	start := max(0, min(first-maxShownTiles/10, len(plan)-maxShownTiles))
	end := min(len(plan), start+maxShownTiles)
	shown := make([]tasklog.Tile, 0, end-start)
	for _, tile := range plan[start:end] {
		tile.Cover = covers[tile.Name]
		if lane, ok := working[tile.Name]; ok && tile.Waiting {
			cover := tile.Cover
			tile = lane.Tile()
			tile.Cover, tile.Waiting = cover, true
		}
		shown = append(shown, tile)
	}
	return shown, len(plan) - end
}

// fileCovers are the small covers of the games of the library, by the name of each file.
func (web *Web) fileCovers() map[string]string {
	return web.derived("fileCovers", func() any {
		switchDB, localDB := web.state.get()
		covers := map[string]string{}
		if localDB == nil {
			return covers
		}
		for prefix, local := range localDB.TitlesMap {
			cover := ""
			if icon := gameIcon(switchDB, prefix, local); icon != "" {
				cover = "/i/" + icon
			} else if switchDB != nil && switchDB.TitlesMap[prefix] != nil {
				cover = coverUrl(switchDB, localDB, switchDB.TitlesMap[prefix].Attributes)
			}
			if cover == "" {
				continue
			}
			cover = thumbUrl(cover)
			add := func(file db.ExtendedFileInfo) {
				if _, ok := covers[file.FileName]; !ok || local.BaseExist && file == local.File.ExtendedInfo {
					covers[file.FileName] = cover
				}
				// the file it was made from, or is turned into, by compressing or decompressing it
				if other := otherPacking(file.FileName); other != "" {
					if _, ok := covers[other]; !ok {
						covers[other] = cover
					}
				}
			}
			if local.BaseExist {
				add(local.File.ExtendedInfo)
			}
			for _, update := range local.Updates {
				add(update.ExtendedInfo)
			}
			for _, dlc := range local.Dlc {
				add(dlc.ExtendedInfo)
			}
		}
		// a scan shows the library it has read so far, and a compressed file leaves it: the
		// covers found before are kept, so the view by covers does not lose them meanwhile
		for name, cover := range covers {
			web.knownCovers.Store(name, cover)
		}
		web.knownCovers.Range(func(name, cover any) bool {
			if _, ok := covers[name.(string)]; !ok {
				covers[name.(string)] = cover.(string)
			}
			return true
		})
		return covers
	}).(map[string]string)
}

// otherPacking is the name of the same file compressed, or decompressed: "Game.nsp" and
// "Game.nsz", "Game.xci" and "Game.xcz"; empty for other files.
func otherPacking(name string) string {
	ext := filepath.Ext(name)
	other, ok := map[string]string{".nsp": ".nsz", ".nsz": ".nsp", ".xci": ".xcz", ".xcz": ".xci"}[strings.ToLower(ext)]
	if !ok {
		return ""
	}
	return name[:len(name)-len(ext)] + other
}

// TaskGroup is a finished task, or several of the same kind that followed one another and
// went well, shown once: "Library scan ×9".
type TaskGroup struct {
	tasklog.Task
	Count int
	// the IDs of the tasks of the group, separated by commas, to dismiss them together
	Ids string
}

// groupTasks puts together the tasks of the same kind and trigger that went well one after
// another; the first of each group is shown.
func groupTasks(tasks []tasklog.Task) []TaskGroup {
	groups := []TaskGroup{}
	quiet := func(task tasklog.Task) bool {
		return task.Status == tasklog.TASK_SUCCESS && task.Error == nil && len(task.Warnings) == 0
	}
	for _, task := range tasks {
		if n := len(groups); n > 0 {
			last := &groups[n-1]
			if quiet(task) && quiet(last.Task) && task.Kind == last.Kind && task.Trigger == last.Trigger {
				last.Count++
				last.Ids += "," + strconv.FormatInt(task.Id, 10)
				continue
			}
		}
		groups = append(groups, TaskGroup{Task: task, Count: 1, Ids: strconv.FormatInt(task.Id, 10)})
	}
	return groups
}

const (
	// at most one update per interval is sent to the browser, however fast tasks progress
	taskEventInterval  = 500 * time.Millisecond
	taskEventKeepAlive = 20 * time.Second
)

// apiTask is a task as /api/tasks gives it: with its message in the language of the request,
// so the pages show it as Tasks does.
type apiTask struct {
	tasklog.Task
	Text string `json:"text,omitempty"`
	// a failed task that is no failure: cancelled, stopped by a restart, or gone on after it
	Ending string `json:"ending,omitempty"`
}

func (web *Web) HandleTasks() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/tasks.html")

	web.router.HandleFunc("/tasks.html", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("part") == "list" {
			// the list alone, refreshed by the page while tasks run
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := templates.executeTemplate(w, web.requestLanguage(r), "taskList", web.taskListData()); err != nil {
				web.sugarLogger.Error(fmt.Errorf("executing template failed: %w", err))
			}
			return
		}
		web.render(w, r, templates, web.tasksPageData())
	}).Methods("GET")

	web.router.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// the pages ask it every second or two: without the files a task has left or the size
		// of every file it compressed, which can be thousands of lines
		tasks := web.taskLog().Snapshot()
		lang := web.requestLanguage(r)
		result := make([]apiTask, len(tasks))
		for i := range tasks {
			tasks[i].Resume, tasks[i].Results = nil, nil
			result[i] = apiTask{Task: tasks[i], Text: messageText(lang, tasks[i].Message, tasks[i].MessageArg), Ending: tasks[i].Ending()}
		}
		writeJSON(w, http.StatusOK, result)
	}).Methods("GET")

	// server-sent events: the version of the task list after every change
	web.router.HandleFunc("/api/tasks/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming is not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		// reverse proxies must not buffer the stream
		w.Header().Set("X-Accel-Buffering", "no")

		changes, stop := web.taskLog().Subscribe()
		defer stop()

		send := func(version int64) {
			fmt.Fprintf(w, "event: tasks\ndata: %d\n\n", version)
			flusher.Flush()
		}
		send(web.taskLog().Version())

		keepAlive := time.NewTicker(taskEventKeepAlive)
		defer keepAlive.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-web.closingPages():
				return
			case <-keepAlive.C:
				fmt.Fprint(w, ": keep-alive\n\n")
				flusher.Flush()
			case version := <-changes:
				send(version)
				// coalesce the changes of busy tasks
				select {
				case <-r.Context().Done():
					return
				case <-time.After(taskEventInterval):
				}
			}
		}
	}).Methods("GET")

	web.router.HandleFunc("/api/tasks/{id:[0-9]+}/dismiss", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if !web.taskLog().Dismiss(id) {
			http.Error(w, "task not found or still running", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")

	web.handleQueue()

	web.router.HandleFunc("/api/tasks/clear", func(w http.ResponseWriter, r *http.Request) {
		web.taskLog().ClearFinished()
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")
}
