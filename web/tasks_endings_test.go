package web

import (
	"encoding/json"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The Tasks page shows the endings that are no failure in their colours, says when a running
// task does not move and what a paused one holds up, and the menu counts only real failures.
func TestTasksPageShowsTheEndings(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	log := web.taskLog()

	cancelled := log.Start(tasklog.TASK_VERIFY, tasklog.TRIGGER_MANUAL)
	log.Finish(cancelled, &tasklog.Note{Text: tasklog.NOTE_COMPRESS_CANCELED})
	went := log.Start(tasklog.TASK_COMPRESS, tasklog.TRIGGER_RESUME)
	log.Finish(went, nil)
	stuck := log.Start(tasklog.TASK_SCAN, tasklog.TRIGGER_WATCHER)
	paused := log.Start(tasklog.TASK_COMPRESS, tasklog.TRIGGER_MANUAL)
	log.SetPaused(paused, true)
	log.Edit(func(tasks []*tasklog.Task) []*tasklog.Task {
		tasks = append(tasks,
			&tasklog.Task{Id: 90, Kind: tasklog.TASK_COMPRESS, Trigger: tasklog.TRIGGER_SCHEDULE, Status: tasklog.TASK_FAILED, Started: time.Now().Add(-time.Hour), Finished: time.Now().Add(-time.Minute),
				Error: &tasklog.Note{Text: tasklog.NOTE_INTERRUPTED}, ContinuedIn: went},
			&tasklog.Task{Id: 91, Kind: tasklog.TASK_ORGANIZE, Trigger: tasklog.TRIGGER_MANUAL, Status: tasklog.TASK_FAILED, Started: time.Now().Add(-time.Hour), Finished: time.Now().Add(-time.Minute),
				Error: &tasklog.Note{Text: tasklog.NOTE_INTERRUPTED}})
		for _, task := range tasks {
			if task.Id == stuck {
				task.Beat = time.Now().Add(-20 * time.Minute)
			}
			if task.Id == paused {
				task.PausedAt = time.Now().Add(-3 * time.Hour)
			}
		}
		return tasks
	})
	web.queue().add(QueuedTask{Kind: tasklog.TASK_VERIFY})
	web.queue().add(QueuedTask{Kind: tasklog.TASK_VERIFY})

	request := httptest.NewRequest("GET", "/tasks.html?part=list", nil)
	request.Header.Set("Accept-Language", "es")
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	for _, want := range []string{"Cancelada", "Seguida tras reiniciar", `href="#task-` + itoa(went) + `"`, "Ver la tarea que siguió",
		"Parada por un reinicio", "vuelve a lanzarla para terminar", "Sin avanzar desde hace 20m", "reinicia la app y mira el disco",
		"En pausa desde hace 3h 0m.", "Mientras siga así esperan: 2 tareas en cola.", "task-muted", `id="task-` + itoa(stuck) + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Fallida") || strings.Contains(body, "Interrumpida") {
		t.Error("none of them is a failure")
	}

	api := httptest.NewRecorder()
	web.router.ServeHTTP(api, httptest.NewRequest("GET", "/api/tasks", nil))
	var tasks []map[string]any
	if api.Code != http.StatusOK || json.Unmarshal(api.Body.Bytes(), &tasks) != nil {
		t.Fatalf("api: %v", api.Code)
	}
	endings := map[string]bool{}
	for _, task := range tasks {
		if ending, ok := task["ending"].(string); ok {
			endings[ending] = true
		}
	}
	if !endings[tasklog.ENDING_CANCELLED] || !endings[tasklog.ENDING_CONTINUED] || !endings[tasklog.ENDING_STOPPED] {
		t.Fatalf("the API tells the endings: %v", endings)
	}
	if web.taskLog().FailedCount() != 0 {
		t.Fatal("the menu counts no failure")
	}
	web.queue().remove(map[int64]bool{1: true, 2: true})
}

// What waits while a task on files is paused, in a sentence of the language.
func TestPausedWaitsText(t *testing.T) {
	if text := (PausedWaits{}).Text("en"); text != "" {
		t.Fatalf("nothing waits: %q", text)
	}
	if text := (PausedWaits{Queue: 1}).Text("en"); text != "1 task in the queue" {
		t.Fatalf("one: %q", text)
	}
	text := PausedWaits{Queue: 2, Sync: true, Automation: 4}.Text("es")
	if text != "2 tareas en cola, la sincronización programada y 4 archivos nuevos de las automatizaciones" {
		t.Fatalf("all of them: %q", text)
	}
}
