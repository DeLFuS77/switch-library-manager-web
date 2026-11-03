package tasklog

import (
	"testing"
	"time"
)

// A task is saved as it starts: the app stopped right after finds it as interrupted, instead of
// losing it.
func TestTaskIsSavedAsItStarts(t *testing.T) {
	dir := t.TempDir()
	log := Load(dir)
	id := log.Start(TASK_VERIFY, TRIGGER_MANUAL)

	again := Load(dir)
	tasks := again.Snapshot()
	if len(tasks) != 1 || tasks[0].Id != id || tasks[0].Status != TASK_FAILED || tasks[0].Error == nil || tasks[0].Error.Text != NOTE_INTERRUPTED {
		t.Fatalf("found again as interrupted: %+v", tasks)
	}
}

// A running task is saved every half minute with when it was last heard of, which tells when
// a task the app was stopped in the middle of ended.
func TestRunningTaskIsSavedWithItsBeat(t *testing.T) {
	dir := t.TempDir()
	log := Load(dir)
	id := log.Start(TASK_COMPRESS, TRIGGER_MANUAL)
	// the last save was long ago
	log.mutex.Lock()
	log.saved = time.Now().Add(-time.Minute)
	log.tasks[0].Started = time.Now().Add(-time.Hour)
	log.mutex.Unlock()
	log.Progress(id, 10, 100, "Game.nsp")

	again := Load(dir)
	task := again.Snapshot()[0]
	if task.Current != 10 {
		t.Fatalf("the progress is saved within half a minute: %+v", task)
	}
	if time.Since(task.Finished) > time.Minute {
		t.Fatalf("an interrupted task ended when it was last heard of, not when it started: %v", task.Finished)
	}
}

// Each task that ends writes a line in the log.
func TestTaskEndsAreTold(t *testing.T) {
	log := &Log{subscribers: map[chan int64]struct{}{}}
	ended := []Task{}
	log.OnEnded = func(task Task) { ended = append(ended, task) }
	id := log.Start(TASK_SCAN, TRIGGER_MANUAL)
	log.Finish(id, &Note{Text: NOTE_SCAN_FAILED})
	if len(ended) != 1 || ended[0].Id != id || ended[0].Status != TASK_FAILED {
		t.Fatalf("told once, as it ended: %+v", ended)
	}
}
