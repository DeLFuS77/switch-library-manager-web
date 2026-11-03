package tasklog

import (
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
		{Task{Status: TASK_FAILED, Error: &Note{Text: NOTE_SCAN_FAILED}}, ""},
		{Task{Status: TASK_FAILED, Error: &Note{Text: NOTE_COMPRESS_CANCELED}}, ENDING_CANCELLED},
		{Task{Status: TASK_FAILED, Error: &Note{Text: NOTE_INTERRUPTED}}, ENDING_STOPPED},
		{Task{Status: TASK_FAILED, Error: &Note{Text: NOTE_INTERRUPTED}, ContinuedIn: 9}, ENDING_CONTINUED},
	}
	for _, c := range cases {
		if got := c.task.Ending(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.task.Error, got, c.want)
		}
	}

	log := &Log{subscribers: map[chan int64]struct{}{}}
	for _, note := range []string{NOTE_SCAN_FAILED, NOTE_COMPRESS_CANCELED} {
		log.Finish(log.Start(TASK_SCAN, TRIGGER_MANUAL), &Note{Text: note})
	}
	log.tasks = append(log.tasks, &Task{Id: 50, Status: TASK_FAILED, Error: &Note{Text: NOTE_INTERRUPTED}})
	if count := log.FailedCount(); count != 1 {
		t.Fatalf("only the real failure counts: %d", count)
	}

	// the history keeps its real failures and what has files left to go on with
	log.mutex.Lock()
	log.tasks = nil
	for i := 0; i < maxFinishedTasks+5; i++ {
		log.tasks = append(log.tasks, &Task{Id: int64(100 + i), Status: TASK_FAILED, Error: &Note{Text: NOTE_COMPRESS_CANCELED}})
	}
	log.tasks = append(log.tasks, &Task{Id: 1, Status: TASK_FAILED, Error: &Note{Text: NOTE_SCAN_FAILED}},
		&Task{Id: 2, Status: TASK_FAILED, Error: &Note{Text: NOTE_INTERRUPTED}, Resume: &Resume{Paths: []string{"a.nsp"}}})
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
