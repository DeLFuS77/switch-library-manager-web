package web

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// A running task shows every file it works on at the same time, each with its progress,
// under the total; a finished one keeps none.
func TestTaskShowsItsLanes(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	id := web.taskLog().Start(TASK_VERIFY, TRIGGER_MANUAL)
	web.taskLog().SetWorkers(id, 2, 0)
	web.taskLog().Progress(id, 300, 1000, "Game A.nsp (2/9)")
	web.taskLog().Lanes(id, []TaskLane{{Name: "Game A.nsp", Done: 512 << 20, Total: 1024 << 20}, {Name: "Game B.nsz", Done: 0, Total: 2048 << 20}})

	page := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/tasks.html?part=list", nil)
	request.Header.Set("Accept-Language", "es")
	web.router.ServeHTTP(page, request)
	body := page.Body.String()
	for _, want := range []string{"2 archivos a la vez", "Total", "Game A.nsp", "Game B.nsz", "50%", "task-lane"} {
		if !strings.Contains(body, want) {
			t.Fatalf("%q in the running task: %s", want, body)
		}
	}
	web.taskLog().Finish(id, nil)
	for _, task := range web.taskLog().Snapshot() {
		if task.Id == id && len(task.Lanes) != 0 {
			t.Fatal("a finished task keeps no lanes")
		}
	}
}

// The steps of a scan are translated, with the file they name apart.
func TestScanStepsAreTranslated(t *testing.T) {
	key, arg := progressMessage("Found Game [0100000000010000][v0].nsz")
	if key != "Looking at the folders: %v" || arg != "Game [0100000000010000][v0].nsz" {
		t.Fatalf("%q %q", key, arg)
	}
	if key, arg := progressMessage("Downloading versions database..."); key != "Downloading versions database..." || arg != "" {
		t.Fatalf("other messages stay: %q %q", key, arg)
	}
	for _, step := range progressSteps {
		if _, ok := translations["es"][step.key]; !ok {
			t.Errorf("no Spanish translation for %q", step.key)
		}
	}
}

// The files of a compression, decompression or conversion say which format they become.
func TestFormatChange(t *testing.T) {
	for _, c := range []struct{ kind, name, want string }{
		{TASK_COMPRESS, "Game [v0].nsp", "NSP → NSZ"},
		{TASK_COMPRESS, "Game [v0].XCI", "XCI → XCZ"},
		{TASK_DECOMPRESS, "Game [v0].nsz", "NSZ → NSP"},
		{TASK_CONVERT, "Game [v0].xci", "XCI → NSP"},
		{TASK_VERIFY, "Game [v0].nsp", ""},
	} {
		if got := formatChange(c.kind, c.name); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.kind, c.name, got, c.want)
		}
	}
}

// The Tasks link counts the tasks running, in violet, apart from the failed ones, in red.
func TestNavShowsRunningAndFailedTasks(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	web.taskLog().Finish(web.taskLog().Start(TASK_SCAN, TRIGGER_MANUAL), &TaskNote{Text: NOTE_SCAN_FAILED})
	web.taskLog().Start(TASK_VERIFY, TRIGGER_MANUAL)
	web.taskLog().Start(TASK_COMPRESS, TRIGGER_MANUAL)

	page := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/tasks.html", nil)
	request.Header.Set("Accept-Language", "es")
	web.router.ServeHTTP(page, request)
	body := page.Body.String()
	for _, want := range []string{"nav-running", ">2<span class=\"visually-hidden\"> Tareas en marcha: 2</span>", ">1<span class=\"visually-hidden\"> Tareas fallidas: 1</span>"} {
		if !strings.Contains(body, want) {
			t.Errorf("the menu shows %q", want)
		}
	}
}

// A file read in three steps shows the step being done with the size of the file, the one
// seen in its folder: 13 GB compressed showed as "15.7 GB / 39.0 GB".
func TestLaneShowsTheStepWithTheSizeOfTheFile(t *testing.T) {
	lane := TaskLane{Name: "Game.nsp", Done: 6 << 30, Total: 12 << 30, Phase: "Compressing", Step: 2, Steps: 3}
	if lane.StepTotal() != 4<<30 || lane.StepDone() != 2<<30 || lane.StepPercent() != 50 {
		t.Fatalf("step: %d of %d, %d%%", lane.StepDone(), lane.StepTotal(), lane.StepPercent())
	}
	// at the very start of a step the report of the step before may still be the last one
	if early := (TaskLane{Done: 3 << 30, Total: 12 << 30, Step: 2, Steps: 3}); early.StepDone() != 0 {
		t.Fatalf("before the step: %d", early.StepDone())
	}
	if single := (TaskLane{Done: 1 << 30, Total: 4 << 30}); single.StepTotal() != 4<<30 || single.StepDone() != 1<<30 {
		t.Fatalf("one step: %d of %d", single.StepDone(), single.StepTotal())
	}

	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	id := web.taskLog().Start(TASK_COMPRESS, TRIGGER_MANUAL)
	// the whole task, other files with it, in MB
	web.taskLog().ProgressFiles(id, 20<<10, 50<<10, "Game.nsp", 2, 3, []TaskLane{lane})
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/tasks.html?part=list", nil))
	body := page.Body.String()
	if want := formatSize(2<<30) + " / " + formatSize(4<<30); !strings.Contains(body, want) {
		t.Errorf("the file shows %q", want)
	}
	if strings.Contains(body, formatSize(6<<30)+" / "+formatSize(12<<30)) {
		t.Error("the file shows the bytes of every step")
	}
}

// The Tasks page by covers: a file at work fills its cover with the progress of its step, and
// a file ended says whether it was done or failed, and why.
func TestTasksByCoversShowEachFile(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	id := web.taskLog().Start(TASK_COMPRESS, TRIGGER_MANUAL)
	web.taskLog().ProgressFiles(id, 20<<10, 50<<10, "Game.nsp", 3, 3, []TaskLane{{Name: "Game.nsp", Done: 6 << 30, Total: 12 << 30, Phase: PHASE_COMPRESSING, Step: 2, Steps: 3, Phases: []string{PHASE_HASHING, PHASE_COMPRESSING, PHASE_VERIFYING}}})
	web.taskLog().AddFileTile(id, "Done.nsp", false, "")
	web.taskLog().AddFileTile(id, "Broken.nsp", true, NOTE_COMPRESS_FAILED)
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/tasks.html?part=list", nil))
	body := page.Body.String()
	for _, want := range []string{`task-tile is-working" style="--lane-fill: 50%"`, `task-tile-pct">50%`, "Done.nsp", "chip-success task-tile-state", `task-tile is-failed`, "Broken.nsp", "chip-danger task-tile-state"} {
		if !strings.Contains(body, want) {
			t.Errorf("the covers show %q", want)
		}
	}
	// the step of the file, and its bar split in its three steps, each in its colour: the hashes
	// done, the compression filling with the moving stripes, the check to come
	for _, want := range []string{`data-step="2"`, `task-tile-step`, "· 2/3", `task-tile-segs`, `task-tile-seg is-done lane-cyan"></span><span class="task-tile-seg"><span class="progress-bar progress-bar-striped progress-bar-animated lane-yellow" style="width: 50%"></span></span><span class="task-tile-seg"></span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the cover at work shows its step: %q", want)
		}
	}
}

// A file left as it is, as a game in sparse storage to compress, is no failure on its cover:
// it says it was skipped, in orange, with why.
func TestTasksByCoversSkipped(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	id := web.taskLog().Start(TASK_COMPRESS, TRIGGER_MANUAL)
	web.taskLog().SetPlan(id, []string{"Sparse.nsp", "Next.nsp"})
	web.taskLog().SkipFileTile(id, "Sparse.nsp", NOTE_COMPRESS_SPARSE)
	task := web.taskLog().Snapshot()[0]
	if !task.Plan[0].Skipped || task.Plan[0].Failed || task.Tiles[0].Failed || !task.Tiles[0].Skipped {
		t.Fatalf("a file skipped: %+v %+v", task.Plan[0], task.Tiles)
	}
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/tasks.html?part=list", nil))
	body := page.Body.String()
	for _, want := range []string{"task-tile is-skipped", "chip-warning task-tile-state", "Skipped"} {
		if !strings.Contains(body, want) {
			t.Errorf("the cover skipped shows %q", want)
		}
	}
	if strings.Contains(body, "chip-danger task-tile-state") || strings.Contains(body, "is-failed") {
		t.Error("a file skipped is no error")
	}
}

// A task of one step, as a verification, keeps the whole bar under its covers, with no step.
func TestTasksByCoversOneStep(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	id := web.taskLog().Start(TASK_VERIFY, TRIGGER_MANUAL)
	web.taskLog().SetPlan(id, []string{"A.nsp", "B.nsp"})
	web.taskLog().ProgressFiles(id, 1, 4, "A.nsp", 1, 2, []TaskLane{{Name: "A.nsp", Done: 1 << 30, Total: 4 << 30}})
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/tasks.html?part=list", nil))
	body := page.Body.String()
	if !strings.Contains(body, `task-tile-bar`) || !strings.Contains(body, `style="width: 25%"`) {
		t.Error("a file at work in one step has its whole bar")
	}
	if strings.Contains(body, "task-tile-segs") || strings.Contains(body, "task-tile-step") || strings.Contains(body, "data-step") {
		t.Error("a task of one step shows no steps")
	}
}

// A compression that saved something already: the size saved is an int64, which once stopped
// the drawing of the card halfway, without its files nor its sizes.
func TestCompressionCardWithSomethingSaved(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	id := web.taskLog().Start(TASK_COMPRESS, TRIGGER_MANUAL)
	web.taskLog().ProgressFiles(id, 21117, 705567, "Game.nsp", 3, 294, []TaskLane{{Name: "Game.nsp", Done: 2 << 30, Total: 4 << 30, Phase: "Compressing", Step: 2, Steps: 3}})
	web.taskLog().SetCompressResult(id, 2, 1326285249)
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/tasks.html?part=list", nil))
	body := page.Body.String()
	for _, want := range []string{"bi-piggy-bank", "task-lane", "Game.nsp", "%"} {
		if !strings.Contains(body, want) {
			t.Errorf("the card shows %q", want)
		}
	}
}

// The time left of a compression follows the speed of each step: hashes at 450 MB/s and the
// max compression at 5 MB/s said "2 minutes left" for an hour when they were averaged.
func TestTimeLeftFollowsEachStep(t *testing.T) {
	// three steps of different speeds, as a compression had them
	phases := []string{PHASE_HASHING, PHASE_COMPRESSING, PHASE_VERIFYING}
	const mb = int64(1) << 20
	// 13 GB to compress: the hashes measured, the compression not yet
	read := []int64{450 * mb * 20, 0, 0}
	spent := []time.Duration{20 * time.Second, 0, 0}
	remaining := []int64{0, 13000 * mb, 13000 * mb}
	if left := estimateLeft(phases, read, spent, remaining, 1); left != 0 {
		t.Fatalf("a step not measured yet: %v", left)
	}
	// the compression measured at 5 MB/s: the check, not measured, goes no slower than it
	read[1], spent[1] = 5*mb*60, time.Minute
	remaining[1] = 12000 * mb
	if left := estimateLeft(phases, read, spent, remaining, 1); left < 83*time.Minute || left > 84*time.Minute {
		t.Fatalf("12000 MB at 5 MB/s and 13000 MB checked at 5 MB/s at most: %v", left)
	}
	// the check measured too, at 200 MB/s
	read[2], spent[2] = 200*mb*10, 10*time.Second
	if left := estimateLeft(phases, read, spent, remaining, 1); left < 41*time.Minute || left > 42*time.Minute {
		t.Fatalf("12000 MB at 5 MB/s and 13000 MB at 200 MB/s: %v", left)
	}
	// two files at once share it
	if left := estimateLeft(phases, read, spent, remaining, 2); left < 20*time.Minute || left > 21*time.Minute {
		t.Fatalf("two files at once: %v", left)
	}
	// a few seconds are not enough to trust a speed
	if left := estimateLeft(phases, []int64{450 * mb, 0, 0}, []time.Duration{time.Second, 0, 0}, []int64{1000 * mb, 0, 0}, 1); left != 0 {
		t.Fatalf("one second measured: %v", left)
	}

	now := time.Now()
	task := Task{Kind: TASK_COMPRESS, Started: now.Add(-time.Hour), Current: 9000, Total: 10000, Estimated: true, Left: 40 * time.Minute, LeftAt: now.Add(-10 * time.Minute)}
	if got := task.remainingAt(now); got != 30*time.Minute {
		t.Fatalf("estimated 40 minutes 10 minutes ago: %v", got)
	}
	// unknown while a step was not measured, instead of the average of the others
	task.Left = 0
	if got := task.remainingAt(now); got != 0 {
		t.Fatalf("not known yet: %v", got)
	}
}

// The bar of a file has the colour of its step in a compression (hashes, work, check), and of
// what it holds in a check (game, update, DLC), instead of a colour taken from its name.
func TestLaneColoursFollowTheStepOrTheKind(t *testing.T) {
	steps := map[string]string{PHASE_HASHING: "cyan", PHASE_COMPRESSING: "yellow", PHASE_DECOMPRESSING: "yellow", PHASE_VERIFYING: "green"}
	for phase, tone := range steps {
		if got := (TaskLane{Name: "Game [0100000000010000][v0].nsp", Phase: phase, Kind: "Base"}).Tone(); got != tone {
			t.Errorf("%s: %s", phase, got)
		}
	}
	kinds := map[string][2]string{
		"Game [0100000000010000][v0].nsp":                    {"red", "Game"},
		"Game [0100000000010800][v65536].nsz":                {"yellow", "Update"},
		"Game DLC [0100000000011001][v0].nsp":                {"cyan", "DLC"},
		"Game [0100000000010000][v65536][+1 DLC] (pack).nsz": {"green", "Combine"},
	}
	for name, want := range kinds {
		lane := TaskLane{Name: name, Kind: laneKind(name)}
		if lane.Tone() != want[0] || lane.KindName() != want[1] {
			t.Errorf("%s: %s %s", name, lane.Tone(), lane.KindName())
		}
	}
	// a name without a title ID keeps a colour of its own, and no kind
	if lane := (TaskLane{Name: "game.nsp"}); lane.KindName() != "" || lane.Tone() == "" {
		t.Errorf("unknown: %+v", lane)
	}
	// in steps the kind is not shown, the step is
	if (TaskLane{Kind: "DLC", Phase: PHASE_VERIFYING}).KindName() != "" {
		t.Error("the kind of a file in steps")
	}
}

// A task the app stopped in the middle of ends when it was last heard of: the last estimate of
// the time left, or the last save of the history, instead of when it began, which showed a
// compression of hours as one that lasted no time.
func TestInterruptedTaskEndsWhenLastHeardOf(t *testing.T) {
	folder := t.TempDir()
	log := loadTaskLog(folder)
	estimated := log.Start(TASK_COMPRESS, TRIGGER_MANUAL)
	silent := log.Start(TASK_SCAN, TRIGGER_WATCHER)
	lastEstimate := time.Now().Add(-10 * time.Minute)
	log.mutex.Lock()
	for _, task := range log.tasks {
		task.Started = time.Now().Add(-2 * time.Hour)
		if task.Id == estimated {
			task.LeftAt = lastEstimate
		}
	}
	log.save()
	log.mutex.Unlock()
	saved := time.Now().Add(-20 * time.Minute)
	if err := os.Chtimes(filepath.Join(folder, TASKS_FILENAME), saved, saved); err != nil {
		t.Fatal(err)
	}

	want := map[int64]time.Time{estimated: lastEstimate, silent: saved}
	for _, task := range loadTaskLog(folder).Snapshot() {
		if at, ok := want[task.Id]; ok && task.Finished.Sub(at).Abs() > time.Second {
			t.Errorf("task %d ends at %v, want %v", task.Id, task.Finished, at)
		}
	}
}
