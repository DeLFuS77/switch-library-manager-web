package web

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
)

type TasksPageData struct {
	GlobalPageData
	Running  []Task
	Finished []TaskGroup
	// the version of the task list, so the page knows when to refresh
	ListVersion int64
}

func (web *Web) tasksPageData() TasksPageData {
	data := TasksPageData{GlobalPageData: web.globalPageData("tasks"), Running: []Task{}, ListVersion: web.taskLog().Version()}
	finished := []Task{}
	for _, task := range web.taskLog().Snapshot() {
		if task.Running() {
			data.Running = append(data.Running, task)
		} else {
			finished = append(finished, task)
		}
	}
	data.Finished = groupTasks(finished)
	return data
}

// TaskGroup is a finished task, or several of the same kind that followed one another and
// went well, shown once: "Library scan ×9".
type TaskGroup struct {
	Task
	Count int
	// the IDs of the tasks of the group, separated by commas, to dismiss them together
	Ids string
}

// groupTasks puts together the tasks of the same kind and trigger that went well one after
// another; the first of each group is shown.
func groupTasks(tasks []Task) []TaskGroup {
	groups := []TaskGroup{}
	quiet := func(task Task) bool { return task.Status == TASK_SUCCESS && task.Error == nil && len(task.Warnings) == 0 }
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

func (web *Web) HandleTasks() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/tasks.html")

	web.router.HandleFunc("/tasks.html", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("part") == "list" {
			// the list alone, refreshed by the page while tasks run
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := templates.executeTemplate(w, web.requestLanguage(r), "taskList", web.tasksPageData()); err != nil {
				web.sugarLogger.Error(fmt.Errorf("executing template failed: %w", err))
			}
			return
		}
		web.render(w, r, templates, web.tasksPageData())
	}).Methods("GET")

	web.router.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, web.taskLog().Snapshot())
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

	web.router.HandleFunc("/api/tasks/clear", func(w http.ResponseWriter, r *http.Request) {
		web.taskLog().ClearFinished()
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")
}
