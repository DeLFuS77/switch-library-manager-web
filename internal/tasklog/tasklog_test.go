package tasklog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
)

func TestTaskLifecycle(t *testing.T) {
	log := Load(t.TempDir())

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
	log.Finish(failed, &Note{Text: NOTE_SCAN_FAILED, Detail: "disk gone"})
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
	log := Load(folder)
	done := log.Start(TASK_SYNC, TRIGGER_SCHEDULE)
	log.Finish(done, nil)
	log.Start(TASK_SCAN, TRIGGER_WATCHER) // still running when the app "stops"
	// only finished tasks trigger a save, so save the running one explicitly
	log.mutex.Lock()
	log.save()
	log.mutex.Unlock()

	reloaded := Load(folder)
	tasks := reloaded.Snapshot()
	if len(tasks) != 2 || tasks[0].Status != TASK_FAILED || tasks[0].Error == nil || tasks[0].Error.Text != NOTE_INTERRUPTED {
		t.Fatalf("an interrupted task must be marked as failed: %+v", tasks)
	}
	if next := reloaded.Start(TASK_SCAN, TRIGGER_MANUAL); next <= tasks[0].Id {
		t.Fatalf("ids must keep growing after a restart: %v", next)
	}
}

func TestTaskLogTrimKeepsFailedTasks(t *testing.T) {
	log := Load(t.TempDir())
	failed := log.Start(TASK_SCAN, TRIGGER_MANUAL)
	log.Finish(failed, &Note{Text: NOTE_SCAN_FAILED})
	for i := 0; i < maxFinishedTasks+10; i++ {
		log.Finish(log.Start(TASK_SCAN, TRIGGER_WATCHER), nil)
	}
	tasks := log.Snapshot()
	if len(tasks) != maxFinishedTasks+1 || tasks[len(tasks)-1].Id != failed {
		t.Fatalf("expected %v successful tasks and the failed one, got %v", maxFinishedTasks, len(tasks))
	}
}

func TestTaskLogSubscribersGetTheLatestVersion(t *testing.T) {
	log := Load(t.TempDir())
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
		if _, ok := i18n.Translations["es"][text]; !ok {
			t.Errorf("no Spanish translation for task note %q", text)
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
		if got := FormatChange(c.kind, c.name); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.kind, c.name, got, c.want)
		}
	}
}

// The bar of a file has the colour of its step in a compression (hashes, work, check), and of
// what it holds in a check (game, update, DLC), instead of a colour taken from its name.
func TestLaneColoursFollowTheStepOrTheKind(t *testing.T) {
	steps := map[string]string{PHASE_HASHING: "cyan", PHASE_COMPRESSING: "yellow", PHASE_DECOMPRESSING: "yellow", PHASE_VERIFYING: "green"}
	for phase, tone := range steps {
		if got := (Lane{Name: "Game [0100000000010000][v0].nsp", Phase: phase, Kind: "Base"}).Tone(); got != tone {
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
		lane := Lane{Name: name, Kind: LaneKind(name)}
		if lane.Tone() != want[0] || lane.KindName() != want[1] {
			t.Errorf("%s: %s %s", name, lane.Tone(), lane.KindName())
		}
	}
	// a name without a title ID keeps a colour of its own, and no kind
	if lane := (Lane{Name: "game.nsp"}); lane.KindName() != "" || lane.Tone() == "" {
		t.Errorf("unknown: %+v", lane)
	}
	// in steps the kind is not shown, the step is
	if (Lane{Kind: "DLC", Phase: PHASE_VERIFYING}).KindName() != "" {
		t.Error("the kind of a file in steps")
	}
}

// A task the app stopped in the middle of ends when it was last heard of: the last estimate of
// the time left, or the last save of the history, instead of when it began, which showed a
// compression of hours as one that lasted no time.
func TestInterruptedTaskEndsWhenLastHeardOf(t *testing.T) {
	folder := t.TempDir()
	log := Load(folder)
	estimated := log.Start(TASK_COMPRESS, TRIGGER_MANUAL)
	silent := log.Start(TASK_SCAN, TRIGGER_WATCHER)
	lastEstimate := time.Now().Add(-10 * time.Minute)
	log.mutex.Lock()
	for _, task := range log.tasks {
		// last heard of as it started, or with its last estimate
		task.Started = time.Now().Add(-2 * time.Hour)
		task.Beat = task.Started
		if task.Id == estimated {
			task.LeftAt, task.Beat = lastEstimate, lastEstimate
		}
	}
	log.save()
	log.mutex.Unlock()
	saved := time.Now().Add(-20 * time.Minute)
	if err := os.Chtimes(filepath.Join(folder, TASKS_FILENAME), saved, saved); err != nil {
		t.Fatal(err)
	}

	want := map[int64]time.Time{estimated: lastEstimate, silent: saved}
	for _, task := range Load(folder).Snapshot() {
		if at, ok := want[task.Id]; ok && task.Finished.Sub(at).Abs() > time.Second {
			t.Errorf("task %d ends at %v, want %v", task.Id, task.Finished, at)
		}
	}
}
