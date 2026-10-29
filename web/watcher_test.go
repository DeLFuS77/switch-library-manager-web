package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/fsnotify/fsnotify"
)

func TestFolderFingerprintChangesWithFiles(t *testing.T) {
	folder := t.TempDir()
	empty := folderFingerprint([]string{folder})

	game := filepath.Join(folder, "sub", "game [0100000000010000][v0].nsp")
	if err := os.MkdirAll(filepath.Dir(game), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(game, []byte("abc"), 0644); err != nil {
		t.Fatal(err)
	}
	added := folderFingerprint([]string{folder})
	if added == empty {
		t.Fatal("adding a file must change the fingerprint")
	}
	if again := folderFingerprint([]string{folder}); again != added {
		t.Fatal("the fingerprint must be stable while nothing changes")
	}

	// a file still being copied grows
	if err := os.WriteFile(game, []byte("abcdef"), 0644); err != nil {
		t.Fatal(err)
	}
	if grown := folderFingerprint([]string{folder}); grown == added {
		t.Fatal("a changed size must change the fingerprint")
	}

	renamed := filepath.Join(folder, "renamed.nsp")
	if err := os.Rename(game, renamed); err != nil {
		t.Fatal(err)
	}
	before := folderFingerprint([]string{folder})
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(renamed, later, later); err != nil {
		t.Fatal(err)
	}
	if folderFingerprint([]string{folder}) == before {
		t.Fatal("a replaced file must change the fingerprint")
	}

	if err := os.Remove(renamed); err != nil {
		t.Fatal(err)
	}
	if folderFingerprint([]string{folder}) != empty {
		t.Fatal("removing every file must give the fingerprint of an empty folder")
	}
}

func TestFolderFingerprintIgnoresMissingFolders(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if folderFingerprint([]string{missing}) != folderFingerprint(nil) {
		t.Fatal("a missing folder must count as empty")
	}
}

func TestOnlyChangedFoldersAreReadAgain(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for i := 0; i < 20; i++ {
		dir := filepath.Join(root, "game "+string(rune('a'+i)))
		os.MkdirAll(dir, 0755)
		file := filepath.Join(dir, "game.nsz")
		os.WriteFile(file, []byte("data"), 0644)
		os.Chtimes(file, old, old)
		os.Chtimes(dir, old, old)
	}
	os.Chtimes(root, old, old)

	tree := newDirTree()
	first, dirs := tree.refresh([]string{root})
	if tree.lastRead != 21 || len(dirs) != 21 {
		t.Fatalf("the first check reads every folder: %d read, %d found", tree.lastRead, len(dirs))
	}
	if again, _ := tree.refresh([]string{root}); again != first || tree.lastRead != 0 {
		t.Fatalf("an unchanged library is not read again: %d folders read", tree.lastRead)
	}

	// a new file is noticed by reading only its folder
	added := filepath.Join(root, "game c", "update.nsp")
	os.WriteFile(added, []byte("new"), 0644)
	changed, _ := tree.refresh([]string{root})
	if changed == first || tree.lastRead != 1 {
		t.Fatalf("one folder changed: fingerprint changed %v, %d folders read", changed != first, tree.lastRead)
	}
	// while it is recent, the folder is read again, so a file being copied is seen growing
	os.WriteFile(added, []byte("new and bigger"), 0644)
	if grown, _ := tree.refresh([]string{root}); grown == changed {
		t.Fatal("a file growing in a recently changed folder must change the fingerprint")
	}
}

func TestWatchFingerprintIsKeptBetweenRuns(t *testing.T) {
	web := newTestWeb(t)
	if web.savedWatchFingerprint() != 0 {
		t.Fatal("nothing was scanned yet")
	}
	// nothing asked: nothing saved
	web.saveWatchFingerprint()
	if web.savedWatchFingerprint() != 0 {
		t.Fatal("no fingerprint without a scan of the watcher")
	}
	web.watchFingerprint.Store(1234567890123)
	web.saveWatchFingerprint()
	if web.savedWatchFingerprint() != 1234567890123 {
		t.Fatal("the next run knows the folders were scanned")
	}

	// the folders did not change since: no scan
	w := &folderWatcher{web: web, scanned: web.savedWatchFingerprint()}
	w.scanIfChanged(1234567890123)
	if web.state.IsSynchronizing() {
		t.Fatal("unchanged folders are not scanned again after a restart")
	}
}

// While a task changes the files the watcher does not scan; a scan done after the task
// covers its changes, so the watcher does not scan them again.
func TestWatcherWaitsForTasksOnTheFiles(t *testing.T) {
	web := newTestWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.WatchFolders = true })
	w := &folderWatcher{web: web, tree: newDirTree(), watched: map[string]struct{}{}}
	current := w.fingerprint()

	// the folders changed while a compression runs
	web.organizing.Store(true)
	w.lastCheck, w.scanned = current, current+1
	w.checkPolled()
	if web.state.IsSynchronizing() {
		t.Fatal("no scan while a task changes the files")
	}

	// a scan started while the task ran (a synchronization asked meanwhile) does not cover
	// what came after it
	web.scanStarted.Store(w.busySeen.Add(-time.Minute).UnixNano())
	web.scansDone.Add(1)
	web.organizing.Store(false)
	if w.waiting() || w.scanned == current {
		t.Fatalf("a scan started during the task does not count: %d %v", w.scanned, w.waited)
	}
	web.organizing.Store(true)
	w.waiting()

	// the task scanned the library when it ended
	web.organizing.Store(false)
	web.scanStarted.Store(time.Now().Add(time.Millisecond).UnixNano())
	web.scansDone.Add(1)
	if !w.waiting() || w.scanned != current || w.waited {
		t.Fatalf("the scan of the task counts for the watcher: %d %v", w.scanned, w.waited)
	}
	if w.waiting() {
		t.Fatal("the watcher works again")
	}

	// a task that did not scan: the watcher looks at the changes itself
	web.organizing.Store(true)
	w.waiting()
	web.organizing.Store(false)
	if w.waiting() {
		t.Fatal("the watcher scans what the task changed")
	}
}

// A task too short for the watcher to see it running is not scanned again after its own scan,
// when the folders reported no change since that scan began; a change after it, or a scan that
// did not end, leaves the folders to the watcher.
func TestWatcherLeavesTheScanOfAShortTask(t *testing.T) {
	web := newTestWeb(t)
	folder := t.TempDir()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.WatchFolders, s.Folder = true, folder })
	notify, err := fsnotify.NewWatcher()
	if err != nil {
		t.Skip("no notifications of changes here")
	}
	defer notify.Close()
	w := &folderWatcher{web: web, tree: newDirTree(), notify: notify, watched: map[string]struct{}{}}
	w.scanned = w.fingerprint()

	// a compression wrote its file, and asked for a scan once it ended
	os.WriteFile(filepath.Join(folder, "Game [0100000000010000][v0].nsz"), []byte("game"), 0o644)
	w.lastEvent = time.Now()
	web.noteAppScan(TRIGGER_COMPRESS)
	web.state.startSync()
	if !w.waiting() {
		t.Fatal("its scan is waited for")
	}
	web.scanStarted.Store(time.Now().UnixNano())
	web.scansDone.Add(1)
	web.state.endSync()
	if !w.waiting() || w.scanned != w.fingerprint() {
		t.Fatal("the scan of the task covers what it changed")
	}

	// a file copied in afterwards is the watcher's
	os.WriteFile(filepath.Join(folder, "Other [0100000000020000][v0].nsp"), []byte("other"), 0o644)
	w.lastEvent = time.Now()
	if w.waiting() {
		t.Fatal("a later change is scanned")
	}
	// so is a change during the scan of a task
	web.noteAppScan(TRIGGER_ORGANIZE)
	web.scanStarted.Store(time.Now().UnixNano())
	web.scansDone.Add(1)
	w.lastEvent = time.Now().Add(2 * time.Second)
	if w.waiting() {
		t.Fatal("a change while the scan ran is scanned")
	}
	// a scan asked for something else than a change of the app is no task's
	web.noteAppScan(TRIGGER_MANUAL)
	if asked := web.appScan.Load(); asked == nil || !asked.at.Equal(w.appScanAt) {
		t.Fatal("only the scans after the changes of the app")
	}
}
