package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// A task cancelled by the user, or stopped by a restart of the app (and gone on or not), is no
// failure: it has its own ending, is not counted among the failed tasks, and goes from the
// history like the tasks that ended well.
func TestTaskEndings(t *testing.T) {
	cases := []struct {
		task Task
		want string
	}{
		{Task{Status: TASK_SUCCESS}, ""},
		{Task{Status: TASK_FAILED, Error: &TaskNote{Text: NOTE_SCAN_FAILED}}, ""},
		{Task{Status: TASK_FAILED, Error: &TaskNote{Text: NOTE_COMPRESS_CANCELED}}, ENDING_CANCELLED},
		{Task{Status: TASK_FAILED, Error: &TaskNote{Text: NOTE_INTERRUPTED}}, ENDING_STOPPED},
		{Task{Status: TASK_FAILED, Error: &TaskNote{Text: NOTE_INTERRUPTED}, ContinuedIn: 9}, ENDING_CONTINUED},
	}
	for _, c := range cases {
		if got := c.task.Ending(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.task.Error, got, c.want)
		}
	}

	log := &TaskLog{subscribers: map[chan int64]struct{}{}}
	for _, note := range []string{NOTE_SCAN_FAILED, NOTE_COMPRESS_CANCELED} {
		log.Finish(log.Start(TASK_SCAN, TRIGGER_MANUAL), &TaskNote{Text: note})
	}
	log.tasks = append(log.tasks, &Task{Id: 50, Status: TASK_FAILED, Error: &TaskNote{Text: NOTE_INTERRUPTED}})
	if count := log.FailedCount(); count != 1 {
		t.Fatalf("only the real failure counts: %d", count)
	}

	// the history keeps its real failures and what has files left to go on with
	log.mutex.Lock()
	log.tasks = nil
	for i := 0; i < maxFinishedTasks+5; i++ {
		log.tasks = append(log.tasks, &Task{Id: int64(100 + i), Status: TASK_FAILED, Error: &TaskNote{Text: NOTE_COMPRESS_CANCELED}})
	}
	log.tasks = append(log.tasks, &Task{Id: 1, Status: TASK_FAILED, Error: &TaskNote{Text: NOTE_SCAN_FAILED}},
		&Task{Id: 2, Status: TASK_FAILED, Error: &TaskNote{Text: NOTE_INTERRUPTED}, Resume: &TaskResume{Paths: []string{"a.nsp"}}})
	log.trim()
	kept := map[int64]bool{}
	for _, task := range log.tasks {
		kept[task.Id] = true
	}
	log.mutex.Unlock()
	if len(log.tasks) != maxFinishedTasks+2 || !kept[1] || !kept[2] {
		t.Fatalf("%d kept, failure %v, to go on with %v", len(log.tasks), kept[1], kept[2])
	}
}

// A running task that did not change for a quarter of an hour is told as stuck; a paused one
// is held on purpose.
func TestStalledTask(t *testing.T) {
	now := time.Now()
	task := Task{Status: TASK_RUNNING, Beat: now.Add(-20 * time.Minute)}
	if got := task.stalledTextAt(now); got != "20m" {
		t.Fatalf("stuck for 20 minutes: %q", got)
	}
	task.Paused = true
	if got := task.stalledTextAt(now); got != "" {
		t.Fatalf("paused: %q", got)
	}
	task = Task{Status: TASK_RUNNING, Beat: now.Add(-5 * time.Minute)}
	if got := task.stalledTextAt(now); got != "" {
		t.Fatalf("working: %q", got)
	}
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

// The Tasks page shows the endings that are no failure in their colours, says when a running
// task does not move and what a paused one holds up, and the menu counts only real failures.
func TestTasksPageShowsTheEndings(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	log := web.taskLog()

	cancelled := log.Start(TASK_VERIFY, TRIGGER_MANUAL)
	log.Finish(cancelled, &TaskNote{Text: NOTE_COMPRESS_CANCELED})
	went := log.Start(TASK_COMPRESS, TRIGGER_RESUME)
	log.Finish(went, nil)
	stuck := log.Start(TASK_SCAN, TRIGGER_WATCHER)
	paused := log.Start(TASK_COMPRESS, TRIGGER_MANUAL)
	log.SetPaused(paused, true)
	log.mutex.Lock()
	log.tasks = append(log.tasks,
		&Task{Id: 90, Kind: TASK_COMPRESS, Trigger: TRIGGER_SCHEDULE, Status: TASK_FAILED, Started: time.Now().Add(-time.Hour), Finished: time.Now().Add(-time.Minute),
			Error: &TaskNote{Text: NOTE_INTERRUPTED}, ContinuedIn: went},
		&Task{Id: 91, Kind: TASK_ORGANIZE, Trigger: TRIGGER_MANUAL, Status: TASK_FAILED, Started: time.Now().Add(-time.Hour), Finished: time.Now().Add(-time.Minute),
			Error: &TaskNote{Text: NOTE_INTERRUPTED}})
	for _, task := range log.tasks {
		if task.Id == stuck {
			task.Beat = time.Now().Add(-20 * time.Minute)
		}
		if task.Id == paused {
			task.PausedAt = time.Now().Add(-3 * time.Hour)
		}
	}
	log.mutex.Unlock()
	web.queue().add(QueuedTask{Kind: TASK_VERIFY})
	web.queue().add(QueuedTask{Kind: TASK_VERIFY})

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
	if !endings[ENDING_CANCELLED] || !endings[ENDING_CONTINUED] || !endings[ENDING_STOPPED] {
		t.Fatalf("the API tells the endings: %v", endings)
	}
	if web.taskLog().FailedCount() != 0 {
		t.Fatal("the menu counts no failure")
	}
	web.queue().remove(map[int64]bool{1: true, 2: true})
}
