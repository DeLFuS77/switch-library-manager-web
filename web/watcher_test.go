package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
