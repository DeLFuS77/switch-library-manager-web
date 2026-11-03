package tasklog

import (
	"path/filepath"
	"testing"
)

// After a restart, the task the app was stopped in the middle of gives what it had left,
// once; a task that ended does not.
func TestInterruptedTaskGoesOnOnce(t *testing.T) {
	dir := t.TempDir()
	saved := &Log{path: filepath.Join(dir, TASKS_FILENAME), subscribers: map[chan int64]struct{}{}}
	id := saved.Start(TASK_COMPRESS, TRIGGER_MANUAL)
	saved.SetResume(id, &Resume{Paths: []string{"/games/b.nsp"}, Level: "max"})

	log := Load(dir)
	taskId, kind, resume := log.Interrupted()
	if taskId != id || kind != TASK_COMPRESS || resume == nil || resume.Paths[0] != "/games/b.nsp" || resume.Level != "max" {
		t.Fatalf("the interrupted compression goes on: %d %q %+v", taskId, kind, resume)
	}
	// kept until it goes on, also after another restart
	if _, _, again := Load(dir).Interrupted(); again == nil {
		t.Fatal("kept while it did not go on")
	}
	log.SetResume(id, nil)
	if _, _, again := log.Interrupted(); again != nil {
		t.Fatal("forgotten once it went on")
	}
	// a task running now is not taken for an interrupted one
	running := log.Start(TASK_COMPRESS, TRIGGER_STARTUP)
	log.SetResume(running, &Resume{Paths: []string{"/games/c.nsp"}})
	if _, _, again := log.Interrupted(); again != nil {
		t.Fatal("a running task is not interrupted")
	}
}
