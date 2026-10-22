package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A task asked for while another works waits in the queue, and starts by itself once the
// files are free.
func TestQueuedTaskStartsWhenTheFilesAreFree(t *testing.T) {
	web, path := compressWeb(t)
	release := make(chan struct{})
	busy := func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		<-release
		return 0, nil
	}
	if !web.runFileTask(TASK_DECOMPRESS, []string{path}, 1, busy) {
		t.Fatal("the first task did not start")
	}

	answer := httptest.NewRecorder()
	web.startOrQueue(answer, QueuedTask{Kind: TASK_COMPRESS, Paths: []string{path}, Level: switchfs.LevelFast})
	reply := map[string]int{}
	json.Unmarshal(answer.Body.Bytes(), &reply)
	if reply["queued"] != 1 || len(web.queue().list()) != 1 {
		t.Fatalf("not queued: %s", answer.Body.String())
	}

	close(release)
	deadline := time.Now().Add(20 * time.Second)
	for {
		compressed := 0
		for _, task := range web.taskLog().Snapshot() {
			if task.Kind == TASK_COMPRESS && !task.Running() {
				compressed++
			}
		}
		if compressed == 1 && len(web.queue().list()) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the queued compression did not run: %+v", web.queue().list())
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitForCompression(t, web)
	if _, err := filepath.Glob(switchfs.CompressedPath(path)); err != nil {
		t.Fatal(err)
	}
}

// Nothing working: the task starts at once and the queue stays empty.
func TestTaskStartsAtOnceWhenNothingWorks(t *testing.T) {
	web, path := compressWeb(t)
	answer := httptest.NewRecorder()
	web.startOrQueue(answer, QueuedTask{Kind: TASK_COMPRESS, Paths: []string{path}, Level: switchfs.LevelFast})
	reply := map[string]int{}
	json.Unmarshal(answer.Body.Bytes(), &reply)
	if reply["started"] != 1 || len(web.queue().list()) != 0 {
		t.Fatalf("not started: %s", answer.Body.String())
	}
	waitForCompression(t, web)
}

// The tasks marked are taken out of the queue; the queue is kept on disk for a restart.
func TestQueueRemovesAndKeeps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	queue := loadTaskQueue(path)
	first := queue.add(QueuedTask{Kind: TASK_COMPRESS, Paths: []string{"/games/a.nsp"}, Level: switchfs.LevelMax, Delete: true})
	second := queue.add(QueuedTask{Kind: TASK_PACK, Paths: []string{"/games/b.nsp"}, Names: map[string]string{"/games/b.nsp": "B"}})
	third := queue.add(QueuedTask{Kind: TASK_VERIFY, Recheck: verifyRecheckAll})
	if queue.position(third) != 3 {
		t.Fatalf("place of the third: %d", queue.position(third))
	}
	if removed := queue.remove(map[int64]bool{first: true, third: true, 99: true}); removed != 2 {
		t.Fatalf("removed %d", removed)
	}

	again := loadTaskQueue(path)
	items := again.list()
	if len(items) != 1 || items[0].Id != second || items[0].Kind != TASK_PACK || items[0].Names["/games/b.nsp"] != "B" {
		t.Fatalf("after a restart: %+v", items)
	}
	// the IDs go on after a restart, never given twice
	if next := again.add(QueuedTask{Kind: TASK_SPLIT}); next <= third {
		t.Fatalf("ID %d given again", next)
	}
}

// The page of the tasks shows the queue, each task with its box to take it out, and the boxes
// marked go out through the API.
func TestTasksPageShowsTheQueue(t *testing.T) {
	web, path := compressWeb(t)
	web.HandleTasks()
	web.queue().add(QueuedTask{Kind: TASK_COMPRESS, Paths: []string{path}})
	pack := web.queue().add(QueuedTask{Kind: TASK_PACK, Paths: []string{path, path}})
	web.queue().add(QueuedTask{Kind: TASK_VERIFY})

	part := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/tasks.html?part=list", nil)
	request.Header.Set("Accept-Language", "es")
	web.router.ServeHTTP(part, request)
	body := part.Body.String()
	for _, want := range []string{"En cola", "Quitar de la cola", "data-queue-item", `value="` + strconv.FormatInt(pack, 10) + `"`, "2 juegos", "toda la biblioteca"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the queue lacks %q: %s", want, body)
		}
	}

	remove := httptest.NewRecorder()
	form := httptest.NewRequest("POST", "/api/queue/remove", strings.NewReader("id="+strconv.FormatInt(pack, 10)))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	web.router.ServeHTTP(remove, form)
	if remove.Code != http.StatusOK || len(web.queue().list()) != 2 {
		t.Fatalf("remove: %d %s, %d left", remove.Code, remove.Body.String(), len(web.queue().list()))
	}
}

// A task moves one place up or down in the queue, never out of it.
func TestQueueMoves(t *testing.T) {
	queue := loadTaskQueue(filepath.Join(t.TempDir(), "queue.json"))
	a := queue.add(QueuedTask{Kind: TASK_COMPRESS})
	b := queue.add(QueuedTask{Kind: TASK_PACK})
	c := queue.add(QueuedTask{Kind: TASK_VERIFY})
	order := func() []int64 {
		ids := []int64{}
		for _, item := range queue.list() {
			ids = append(ids, item.Id)
		}
		return ids
	}
	if !queue.move(c, true) || queue.move(a, true) || queue.move(b, false) {
		t.Fatalf("moves: %v", order())
	}
	if got := order(); got[0] != a || got[1] != c || got[2] != b {
		t.Fatalf("order: %v", got)
	}
	if again := loadTaskQueue(queue.path).list(); again[1].Id != c {
		t.Fatal("the new order is kept")
	}
}

// When the last task of a run of the queue ends, the notifications tell it once, with each
// task that ran, the one working when the first was queued included.
func TestQueueDoneIsNotified(t *testing.T) {
	web, path := compressWeb(t)
	var mutex sync.Mutex
	messages := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mutex.Lock()
		messages = append(messages, body["title"].(string)+" | "+body["message"].(string))
		mutex.Unlock()
	}))
	t.Cleanup(server.Close)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Notifications = settings.NotificationOptions{WebhookUrl: server.URL}
		s.Language = "es"
	})

	release := make(chan struct{})
	web.runFileTask(TASK_DECOMPRESS, []string{path}, 1, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		<-release
		return 0, nil
	})
	web.startOrQueue(httptest.NewRecorder(), QueuedTask{Kind: TASK_COMPRESS, Paths: []string{path}, Level: switchfs.LevelFast})
	close(release)

	deadline := time.Now().Add(20 * time.Second)
	for {
		mutex.Lock()
		n := len(messages)
		mutex.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	mutex.Lock()
	defer mutex.Unlock()
	if len(messages) != 1 || !strings.Contains(messages[0], "La cola de tareas ha terminado") || !strings.Contains(messages[0], "Descompresión: hecho") || !strings.Contains(messages[0], "Compresión: hecho") {
		t.Fatalf("messages: %q", messages)
	}
}
