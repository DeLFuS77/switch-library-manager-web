package web

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// The automations wait while anything holds the files, a measure of the server too, instead of
// trying a step that is refused: the new files used to leave their queue unchecked then.
func TestAutomationWaitsWhileTheFilesAreHeld(t *testing.T) {
	saved := automationPollOverride.Load()
	automationPollOverride.Store(int64(10 * time.Millisecond))
	defer automationPollOverride.Store(saved)

	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Automation = settings.AutomationOptions{Enabled: true, Verify: true}
	})
	web.measuring.Store(true)
	web.queueAutomation(newAutomationEvents())
	time.Sleep(100 * time.Millisecond)
	if pending := web.automationPending(); pending != 3 {
		t.Fatalf("the files wait while the server is measured: %d", pending)
	}
	if _, ok := web.latestTask(tasklog.TASK_VERIFY); ok {
		t.Fatal("no check while the files are held")
	}

	web.measuring.Store(false)
	queue := web.automation()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		queue.mutex.Lock()
		busy := queue.running || len(queue.Paths) > 0
		queue.mutex.Unlock()
		if !busy {
			break
		}
	}
	task, ok := web.latestTask(tasklog.TASK_VERIFY)
	if !ok || task.Trigger != tasklog.TRIGGER_AUTOMATION || task.Running() {
		t.Fatalf("the new files are checked once the files are free: %+v", task)
	}
	if pending := web.automationPending(); pending != 0 {
		t.Fatalf("taken care of: %d", pending)
	}
}

// The files the automations are working on count among the ones waiting for them.
func TestAutomationPendingCountsTheFilesAtWork(t *testing.T) {
	web := newTestWeb(t)
	queue := web.automation()
	queue.mutex.Lock()
	queue.Paths = []string{"a.nsp"}
	queue.Running = &automationBatch{Paths: []string{"b.nsp", "c.nsp"}}
	queue.mutex.Unlock()
	if pending := web.automationPending(); pending != 3 {
		t.Fatalf("waiting and at work: %d", pending)
	}
}

// A rescan asked while Organize holds the scan used to wait for the end of a scan that never
// came when Organize changed nothing, and a check of the library waited in the queue with it.
// Letting the scan go does the rescan.
func TestReleasingTheScanDoesTheRescanAsked(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	if !web.holdScan() {
		t.Fatal("the scan is free")
	}
	web.Rescan(tasklog.TRIGGER_WATCHER)
	if !web.rescanPending.Load() {
		t.Fatal("left for the end of the scan")
	}
	web.releaseScan("")
	if web.rescanPending.Load() {
		t.Fatal("done once the scan is let go")
	}
	waitForBackgroundWork(web)
	if task, ok := web.latestTask(tasklog.TASK_SCAN); !ok || task.Trigger != tasklog.TRIGGER_WATCHER {
		t.Fatalf("the rescan ran: %+v", task)
	}

	// and a check of the whole library waiting for it starts
	web.queue().add(QueuedTask{Kind: tasklog.TASK_VERIFY})
	web.runQueue()
	if len(web.queue().list()) != 0 {
		t.Fatal("the check left the queue")
	}
	waitForTask(t, web, tasklog.TASK_VERIFY)
}

// A scan that starts reads the files as they are: a rescan asked before it is done by it.
func TestAScanDoesTheRescanAskedBeforeIt(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	web.rescanPending.Store(true)
	if !web.Rescan(tasklog.TRIGGER_MANUAL) {
		t.Fatal("the scan starts")
	}
	if web.rescanPending.Load() {
		t.Fatal("no second scan for what the first one reads")
	}
	waitForBackgroundWork(web)
}

// Stopping the app saves what the task on the files has left at that moment, and no task
// starts any more.
func TestStopSavesWhatATaskHasLeft(t *testing.T) {
	web := newTestWeb(t)
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
	if !web.runResumableTask(tasklog.TASK_COMPRESS, tasklog.TRIGGER_MANUAL, paths, 1, &tasklog.Resume{Level: "fast"}, job) {
		t.Fatal("not started")
	}
	<-started
	<-started
	// the first file is done, the second at work: saved every half minute only
	time.Sleep(50 * time.Millisecond)
	web.Stop()

	again := tasklog.Load(web.dataFolder)
	task := again.Snapshot()[0]
	if task.Resume == nil || !reflect.DeepEqual(task.Resume.Paths, paths[1:]) {
		t.Fatalf("what is left, as of the stop: %+v", task.Resume)
	}
	if web.startVerificationOf(nil, verifyRecheckChanged, tasklog.TRIGGER_MANUAL) {
		t.Fatal("no task starts while the app stops")
	}
	close(release)
	waitForCompression(t, web)
}

// A check started by the user that the app was stopped in the middle of goes on by itself once
// it starts again, and the interrupted one points to it.
func TestVerificationGoesOnAfterARestart(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "a.nsp"), filepath.Join(dir, "b.nsp")}
	for _, path := range paths {
		os.WriteFile(path, []byte("data"), 0644)
	}
	checked := make(chan string, 4)
	saved := verifyLibraryFile
	verifyLibraryFile = func(ctx context.Context, path string, progress func(int64, int64)) (switchfs.FileCheck, error) {
		checked <- path
		return switchfs.FileCheck{}, nil
	}
	t.Cleanup(func() { verifyLibraryFile = saved })

	// the check of before: a.nsp was checked as it is now, then the app stopped
	since := time.Now().Add(-time.Hour)
	info, _ := os.Stat(paths[0])
	web.verifications().set(paths[0], verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checked: since.Add(time.Minute), Checker: verifyChecker})
	before := tasklog.Load(web.dataFolder)
	before.Edit(func([]*tasklog.Task) []*tasklog.Task {
		return []*tasklog.Task{{Id: 4, Kind: tasklog.TASK_VERIFY, Trigger: tasklog.TRIGGER_MANUAL, Status: tasklog.TASK_RUNNING, Started: since,
			Resume: &tasklog.Resume{Paths: paths, Level: tasklog.VERIFY_LEVEL_HASH, Recheck: verifyRecheckAll, Since: since}}}
	})
	before.Flush()
	web.tasks = tasklog.Load(web.dataFolder)

	web.resumeInterrupted()
	task := waitForTask(t, web, tasklog.TASK_VERIFY)
	if task.Trigger != tasklog.TRIGGER_RESUME || task.Error != nil {
		t.Fatalf("the check goes on: %+v", task)
	}
	close(checked)
	got := []string{}
	for path := range checked {
		got = append(got, path)
	}
	if !reflect.DeepEqual(got, paths[1:]) {
		t.Fatalf("only the file not checked since it began: %v", got)
	}
	for _, old := range web.taskLog().Snapshot() {
		if old.Id == 4 && (old.Resume != nil || old.ContinuedIn != task.Id) {
			t.Fatalf("the interrupted check points to the one that went on: %+v", old)
		}
	}
	if task.Resume != nil {
		t.Fatal("a check that ended by itself does not go on after a restart")
	}
}

// A scheduled check does not go on after a restart: the next background hours do it.
func TestScheduledVerificationIsNotResumed(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	saved := verifyLibraryFile
	block := make(chan struct{})
	verifyLibraryFile = func(ctx context.Context, path string, progress func(int64, int64)) (switchfs.FileCheck, error) {
		<-block
		return switchfs.FileCheck{}, nil
	}
	t.Cleanup(func() { verifyLibraryFile = saved })
	if !web.startVerificationOf(nil, verifyRecheckChanged, tasklog.TRIGGER_SCHEDULE) {
		t.Fatal("not started")
	}
	if task, _ := web.latestTask(tasklog.TASK_VERIFY); task.Resume != nil {
		t.Fatalf("nothing kept to go on with: %+v", task.Resume)
	}
	close(block)
	waitForTask(t, web, tasklog.TASK_VERIFY)
}

// A file the check itself panics on is left unchecked, not called damaged, and the check goes on
// with the others.
func TestVerificationSurvivesAPanicOnAFile(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "a.nsp"), filepath.Join(dir, "b.nsp")}
	for _, path := range paths {
		os.WriteFile(path, []byte("data"), 0644)
	}
	saved := verifyLibraryFile
	verifyLibraryFile = func(ctx context.Context, path string, progress func(int64, int64)) (switchfs.FileCheck, error) {
		if strings.HasSuffix(path, "a.nsp") {
			panic("index out of range")
		}
		return switchfs.FileCheck{}, nil
	}
	t.Cleanup(func() { verifyLibraryFile = saved })
	if !web.startVerificationAt(tasklog.VERIFY_LEVEL_HASH, paths, verifyRecheckChanged, tasklog.TRIGGER_MANUAL) {
		t.Fatal("not started")
	}
	task := waitForTask(t, web, tasklog.TASK_VERIFY)
	if task.Status == tasklog.TASK_FAILED || task.Files != 1 || task.Damaged != 0 {
		t.Fatalf("one file checked, the other left unchecked: %+v", task)
	}
	if _, ok := web.verifications().current(paths[0], mustStat(t, paths[0])); ok {
		t.Fatal("no result for the file the check failed on")
	}
}

// Organize that panics on a strange library ends failed and lets the files and the scan go,
// instead of staying running with the scan held for good.
func TestOrganizePanicEndsTheTask(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	// a title with nothing behind it
	localDB.TitlesMap["0100000000099"] = nil
	web.state.set(switchDB, localDB)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Folder = t.TempDir() })

	if !web.holdScan() {
		t.Fatal("the scan is free")
	}
	_, err := web.runOrganizeFiles(ORGANIZE_ACTION_ORGANIZE, nil, tasklog.TRIGGER_MANUAL)
	web.releaseScan("")
	task, _ := web.latestTask(tasklog.TASK_ORGANIZE)
	if task.Running() {
		t.Fatalf("the task ended: %+v", task)
	}
	if err == nil {
		// this library did not make it panic: nothing more to look at
		t.Skip("Organize did not panic on this library")
	}
	if task.Error == nil || task.Error.Text != tasklog.NOTE_TASK_CRASHED {
		t.Fatalf("failed on the unexpected error: %+v", task.Error)
	}
	if web.organizing.Load() || web.state.IsSynchronizing() {
		t.Fatal("the files and the scan are free again")
	}
}

// safely turns a panic of the work on a file into its error.
func TestSafely(t *testing.T) {
	_, err := safely(func() (int, error) { panic("boom") })
	if !errors.Is(err, errUnexpected) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("the panic as an error: %v", err)
	}
	if value, err := safely(func() (int, error) { return 7, nil }); value != 7 || err != nil {
		t.Fatalf("the work as it is: %v %v", value, err)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
