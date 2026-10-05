package web

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestTaskLifecycle(t *testing.T) {
	log := loadTaskLog(t.TempDir())

	id := log.Start(TASK_SYNC, TRIGGER_MANUAL)
	log.Progress(id, 2, 4, "Downloading titles database...")
	running := log.Snapshot()[0]
	if !running.Running() || running.Percent() != 50 || running.Message == "" {
		t.Fatalf("running task: %+v", running)
	}
	if log.Dismiss(id) {
		t.Fatal("a running task cannot be dismissed")
	}

	log.Warn(id, NOTE_NOTIFY_FAILED, "timeout")
	log.SetResult(id, 12, 30)
	log.Finish(id, nil)
	done := log.Snapshot()[0]
	if done.Status != TASK_WARNING || done.Games != 12 || done.Files != 30 || done.Finished.IsZero() || done.Message != "" {
		t.Fatalf("finished task: %+v", done)
	}

	failed := log.Start(TASK_SCAN, TRIGGER_WATCHER)
	log.Finish(failed, &TaskNote{Text: NOTE_SCAN_FAILED, Detail: "disk gone"})
	if log.FailedCount() != 1 || log.Snapshot()[0].Error.Detail != "disk gone" {
		t.Fatalf("failed task: %+v", log.Snapshot()[0])
	}
	if !log.Dismiss(failed) || log.FailedCount() != 0 {
		t.Fatal("a failed task must be dismissable")
	}

	ok := log.Start(TASK_SCAN, TRIGGER_STARTUP)
	log.Finish(ok, nil)
	if log.Snapshot()[0].Status != TASK_SUCCESS {
		t.Fatalf("successful task: %+v", log.Snapshot()[0])
	}
	log.ClearFinished()
	if len(log.Snapshot()) != 0 {
		t.Fatal("clearing must remove every finished task")
	}
}

func TestTaskPercentWithoutKnownSteps(t *testing.T) {
	for _, task := range []Task{{Current: -1, Total: -1}, {Current: 3, Total: 0}} {
		if task.Percent() != -1 {
			t.Errorf("%+v: unknown progress must be -1", task)
		}
	}
	if (Task{Current: 9, Total: 4}).Percent() != 100 {
		t.Error("progress must not exceed 100")
	}
}

func TestTaskLogPersistsAndMarksInterruptedTasks(t *testing.T) {
	folder := t.TempDir()
	log := loadTaskLog(folder)
	done := log.Start(TASK_SYNC, TRIGGER_SCHEDULE)
	log.Finish(done, nil)
	log.Start(TASK_SCAN, TRIGGER_WATCHER) // still running when the app "stops"
	// only finished tasks trigger a save, so save the running one explicitly
	log.mutex.Lock()
	log.save()
	log.mutex.Unlock()

	reloaded := loadTaskLog(folder)
	tasks := reloaded.Snapshot()
	if len(tasks) != 2 || tasks[0].Status != TASK_FAILED || tasks[0].Error == nil || tasks[0].Error.Text != NOTE_INTERRUPTED {
		t.Fatalf("an interrupted task must be marked as failed: %+v", tasks)
	}
	if next := reloaded.Start(TASK_SCAN, TRIGGER_MANUAL); next <= tasks[0].Id {
		t.Fatalf("ids must keep growing after a restart: %v", next)
	}
}

func TestTaskLogTrimKeepsFailedTasks(t *testing.T) {
	log := loadTaskLog(t.TempDir())
	failed := log.Start(TASK_SCAN, TRIGGER_MANUAL)
	log.Finish(failed, &TaskNote{Text: NOTE_SCAN_FAILED})
	for i := 0; i < maxFinishedTasks+10; i++ {
		log.Finish(log.Start(TASK_SCAN, TRIGGER_WATCHER), nil)
	}
	tasks := log.Snapshot()
	if len(tasks) != maxFinishedTasks+1 || tasks[len(tasks)-1].Id != failed {
		t.Fatalf("expected %v successful tasks and the failed one, got %v", maxFinishedTasks, len(tasks))
	}
}

func TestTaskLogSubscribersGetTheLatestVersion(t *testing.T) {
	log := loadTaskLog(t.TempDir())
	changes, stop := log.Subscribe()
	defer stop()

	id := log.Start(TASK_SCAN, TRIGGER_MANUAL)
	for i := 0; i < 100; i++ {
		// a slow reader must not block the task
		log.Progress(id, i, 100, "Reading")
	}
	select {
	case version := <-changes:
		if version != log.Version() {
			t.Fatalf("got version %v, want the latest %v", version, log.Version())
		}
	case <-time.After(time.Second):
		t.Fatal("no change received")
	}
}

func TestTaskNotesAreTranslated(t *testing.T) {
	for _, text := range taskNoteTexts {
		if _, ok := translations["es"][text]; !ok {
			t.Errorf("no Spanish translation for task note %q", text)
		}
	}
}

func TestTasksHttp(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()

	failed := web.taskLog().Start(TASK_SYNC, TRIGGER_SCHEDULE)
	web.taskLog().Warn(failed, NOTE_TITLES_SAVED_COPY, "502 Bad Gateway")
	web.taskLog().Finish(failed, &TaskNote{Text: NOTE_SCAN_FAILED, Detail: "permission denied"})
	organized := web.taskLog().Start(TASK_ORGANIZE, TRIGGER_MANUAL)
	web.taskLog().SetResult(organized, 0, 7)
	web.taskLog().Finish(organized, nil)
	scanned := web.taskLog().Start(TASK_SCAN, TRIGGER_WATCHER)
	web.taskLog().SetResult(scanned, 3, 9)
	web.taskLog().Finish(scanned, nil)
	web.taskLog().Progress(web.taskLog().Start(TASK_SYNC, TRIGGER_MANUAL), 1, 4, "Downloading versions database...")

	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/tasks.html", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "permission denied") || !strings.Contains(page.Body.String(), "web.js") ||
		!strings.Contains(page.Body.String(), "7 files changed") || !strings.Contains(page.Body.String(), "3 games, 9 files") || !strings.Contains(page.Body.String(), "25%") || !strings.Contains(page.Body.String(), "Manager Web "+settings.SLM_WEB_VERSION) {
		t.Fatalf("tasks page: %v %s", page.Code, page.Body.String())
	}

	part := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/tasks.html?part=list", nil)
	request.Header.Set("Accept-Language", "es")
	web.router.ServeHTTP(part, request)
	body := part.Body.String()
	if strings.Contains(body, "<html") || !strings.Contains(body, "Fallida") || !strings.Contains(body, "se usa la copia guardada") {
		t.Fatalf("the list part must be translated and without the layout: %s", body)
	}

	dismiss := httptest.NewRecorder()
	web.router.ServeHTTP(dismiss, httptest.NewRequest("POST", "/api/tasks/"+strconv.FormatInt(failed, 10)+"/dismiss", nil))
	if dismiss.Code != http.StatusNoContent || web.taskLog().FailedCount() != 0 {
		t.Fatalf("dismiss: %v", dismiss.Code)
	}
	missing := httptest.NewRecorder()
	web.router.ServeHTTP(missing, httptest.NewRequest("POST", "/api/tasks/999/dismiss", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("dismissing an unknown task: %v", missing.Code)
	}
}

func TestTaskEventsStream(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	server := httptest.NewServer(web.router)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/tasks/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type: %q", response.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(response.Body)
	readData := func() string {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				return strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			}
		}
	}
	if first := readData(); first != "0" {
		t.Fatalf("the stream starts with the current version, got %q", first)
	}
	web.taskLog().Start(TASK_SCAN, TRIGGER_MANUAL)
	if next := readData(); next == "0" {
		t.Fatal("a change must send a new version")
	}
}

func TestNavCountsIncludeFailedTasks(t *testing.T) {
	web := newTestWeb(t)
	web.taskLog().Finish(web.taskLog().Start(TASK_SCAN, TRIGGER_MANUAL), &TaskNote{Text: NOTE_SCAN_FAILED})
	if web.navCounts().FailedTasks != 1 {
		t.Fatalf("failed tasks must be counted: %+v", web.navCounts())
	}
}
