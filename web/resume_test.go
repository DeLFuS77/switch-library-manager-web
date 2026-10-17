package web

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A compression keeps the files it has left as it goes, for a restart in the middle of it,
// and forgets them once it ends by itself.
func TestCompressionKeepsWhatIsLeft(t *testing.T) {
	web := newTestWeb(t)
	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	web.localDbManager = manager
	// the rescan after the compression must end before the folders are removed
	t.Cleanup(func() {
		for deadline := time.Now().Add(10 * time.Second); web.state.IsSynchronizing() && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		manager.Close()
	})
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CompressFilesAtOnce = 1 })
	dir := t.TempDir()
	paths := []string{}
	for _, name := range []string{"a.nsp", "b.nsp", "c.nsp"} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte("data"), 0644)
		paths = append(paths, path)
	}
	release := make(chan struct{})
	started := make(chan string, 3)
	job := func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		started <- path
		if path == paths[1] {
			<-release
		}
		return 1, nil
	}
	if !web.runResumableTask(TASK_COMPRESS, TRIGGER_MANUAL, paths, 1, &TaskResume{Level: "fast", Delete: true}, job) {
		t.Fatal("not started")
	}
	<-started
	<-started
	// the first file is done, the second is being worked on
	deadline := time.Now().Add(5 * time.Second)
	var resume *TaskResume
	for time.Now().Before(deadline) {
		resume = web.taskLog().Snapshot()[0].Resume
		if resume != nil && len(resume.Paths) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if resume == nil || !reflect.DeepEqual(resume.Paths, paths[1:]) || resume.Level != "fast" || !resume.Delete {
		t.Fatalf("what is left: %+v", resume)
	}
	close(release)
	task := waitForCompression(t, web)
	if task.Resume != nil || task.Files != 3 {
		t.Fatalf("a task that ended forgets what was left: %+v", task)
	}
}

// After a restart, the task the app was stopped in the middle of gives what it had left,
// once; a task that ended does not.
func TestInterruptedTaskGoesOnOnce(t *testing.T) {
	dir := t.TempDir()
	saved := &TaskLog{path: filepath.Join(dir, TASKS_FILENAME), subscribers: map[chan int64]struct{}{}}
	id := saved.Start(TASK_COMPRESS, TRIGGER_MANUAL)
	saved.SetResume(id, &TaskResume{Paths: []string{"/games/b.nsp"}, Level: "max"})

	log := loadTaskLog(dir)
	kind, resume := log.takeInterrupted()
	if kind != TASK_COMPRESS || resume == nil || resume.Paths[0] != "/games/b.nsp" || resume.Level != "max" {
		t.Fatalf("the interrupted compression goes on: %q %+v", kind, resume)
	}
	if _, again := log.takeInterrupted(); again != nil {
		t.Fatal("it goes on once")
	}
	if _, again := loadTaskLog(dir).takeInterrupted(); again != nil {
		t.Fatal("once also after another restart")
	}
}
