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
