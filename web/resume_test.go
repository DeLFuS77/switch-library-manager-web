package web

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
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
	// saved after every file, to look at it
	saved := resumeSaveInterval
	resumeSaveInterval = 0
	t.Cleanup(func() { resumeSaveInterval = saved })
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
	if !web.runResumableTask(tasklog.TASK_COMPRESS, tasklog.TRIGGER_MANUAL, paths, 1, &tasklog.Resume{Level: "fast", Delete: true}, job) {
		t.Fatal("not started")
	}
	<-started
	<-started
	// the first file is done, the second is being worked on
	deadline := time.Now().Add(5 * time.Second)
	var resume *tasklog.Resume
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

// A rescan asked while a scan runs (a compression ended, files were organized) is done once
// that scan ends, not lost; the copies written while it ran are not forgotten by it.
func TestRescanAskedDuringAScanIsNotLost(t *testing.T) {
	web := newTestWeb(t)
	if !web.state.startSync() {
		t.Fatal("no scan running")
	}
	web.scanStarted.Store(time.Now().UnixNano())
	if web.Rescan(tasklog.TRIGGER_COMPRESS) {
		t.Fatal("a scan is running")
	}
	if !web.rescanPending.Load() {
		t.Fatal("the rescan is kept for when the scan ends")
	}
	before := time.Now().Add(-time.Hour)
	web.compressor.written = map[string]time.Time{"old.nsz": before, "new.nsz": time.Now()}
	web.forgetWritten(time.Unix(0, web.scanStarted.Load()))
	if web.writtenCopy("old.nsz") || !web.writtenCopy("new.nsz") {
		t.Fatalf("only the copies written before the scan are forgotten: %v", web.compressor.written)
	}
	web.state.endSync()
}

// A task the app was stopped in the middle of goes on only while no task works on the files:
// a compression started by hand meanwhile may be writing the temporary file it would remove.
func TestResumeLeavesTheFileOfARunningTask(t *testing.T) {
	web, path := compressWeb(t)
	saved := tasklog.Load(web.dataFolder)
	id := saved.Start(tasklog.TASK_COMPRESS, tasklog.TRIGGER_MANUAL)
	saved.SetResume(id, &tasklog.Resume{Paths: []string{path}, Level: "fast"})
	temporary := temporaryOf(path, switchfs.CompressedPath(path))

	// the same file compressed by hand, before the scan of the start let the task go on
	writing, release := make(chan struct{}), make(chan struct{})
	web.runFileTask(tasklog.TASK_COMPRESS, []string{path}, 1, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		os.WriteFile(temporary, []byte("being written"), 0o644)
		close(writing)
		<-release
		os.Remove(temporary)
		return 0, nil
	})
	<-writing
	web.resumeInterrupted()
	if _, err := os.Stat(temporary); err != nil {
		t.Fatal("the file being written stays")
	}
	if _, _, resume := web.taskLog().Interrupted(); resume == nil {
		t.Fatal("the task goes on later")
	}
	close(release)
	waitForCompression(t, web)
}
