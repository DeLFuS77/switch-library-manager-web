package process

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// acrossDisks makes every move fail as between two disks while the test runs, as with two
// volumes of a container; before is called with the move first.
func acrossDisks(t *testing.T, before func(from, to string)) {
	t.Helper()
	t.Cleanup(func() { rename = renameNoReplace })
	rename = func(from, to string) error {
		if before != nil {
			before(from, to)
		}
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: errCrossDevice}
	}
}

// Between two disks, where a rename cannot go, Organize copies each file and deletes the
// original once the copy has its name: the files of a game all move, with their dates.
func TestOrganizeMovesFilesBetweenDisks(t *testing.T) {
	baseFolder := t.TempDir()
	source := filepath.Join(t.TempDir(), "incoming")
	local, remote := fixtureLibrary(t, source)
	past := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	for _, name := range []string{"base.nsp", "update.nsp", "dlc-one.nsp", "dlc-two.nsp"} {
		os.Chtimes(filepath.Join(source, name), past, past)
	}
	acrossDisks(t, nil)

	ops, err := OrganizeByFolders(baseFolder, local, remote, organizeOptions(), false, nil)
	if err != nil || len(failedOps(ops)) != 0 {
		t.Fatalf("organize: %v %+v", err, failedOps(ops))
	}
	if moves := countKind(ops, OP_MOVE); moves != 4 {
		t.Fatalf("the game, its update and its DLC: %+v", ops)
	}
	base := filepath.Join(baseFolder, "Test Game", "Test Game [0100E95004039000][BASE][v0].nsp")
	update := filepath.Join(baseFolder, "Test Game", "Updates", "Test Game [0100E95004039800][UPD][v65536].nsp")
	for path, contents := range map[string]string{base: "base", update: "update"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != contents {
			t.Fatalf("%s: %q %v", path, data, err)
		}
		if info, _ := os.Stat(path); !info.ModTime().Equal(past) {
			t.Errorf("%s keeps the date of the original: %v", path, info.ModTime())
		}
	}
	entries, _ := os.ReadDir(source)
	if len(entries) != 0 {
		t.Fatalf("the originals are deleted once copied: %v", entries)
	}
	for _, folder := range []string{filepath.Join(baseFolder, "Test Game"), filepath.Join(baseFolder, "Test Game", "Updates")} {
		entries, _ := os.ReadDir(folder)
		for _, entry := range entries {
			if leftoverTemporary(entry.Name()) {
				t.Fatalf("no temporary copy left: %s", entry.Name())
			}
		}
	}
}

func leftoverTemporary(name string) bool {
	return len(name) > 0 && name[0] == '.' && filepath.Ext(name) == ".tmp"
}

// A file that comes at the destination after it was looked at is never written over, by a
// rename or by a copy between two disks: the file moved stays in its folder, with its new name.
func TestMoveNeverWritesOverAFileThatCame(t *testing.T) {
	for _, across := range []bool{false, true} {
		baseFolder := t.TempDir()
		local, remote := fixtureLibrary(t, filepath.Join(baseFolder, "incoming"))
		game := local.TitlesMap[groupID]
		game.Updates = nil
		game.Dlc = nil
		came := func(from, to string) {
			if filepath.Base(from) == "base.nsp" {
				os.WriteFile(to, []byte("another file"), 0o644)
			}
		}
		if across {
			acrossDisks(t, came)
		} else {
			t.Cleanup(func() { rename = renameNoReplace })
			rename = func(from, to string) error {
				came(from, to)
				return renameNoReplace(from, to)
			}
		}

		ops, err := OrganizeByFolders(baseFolder, local, remote, organizeOptions(), false, nil)
		if err != nil || len(failedOps(ops)) != 0 {
			t.Fatalf("across disks %v: %v %+v", across, err, failedOps(ops))
		}
		destination := filepath.Join(baseFolder, "Test Game", "Test Game [0100E95004039000][BASE][v0].nsp")
		if data, _ := os.ReadFile(destination); string(data) != "another file" {
			t.Fatalf("across disks %v: the file that came stays as it was: %q", across, data)
		}
		if data, _ := os.ReadFile(filepath.Join(baseFolder, "incoming", filepath.Base(destination))); string(data) != "base" {
			t.Fatalf("across disks %v: the game stays in its folder with its new name: %+v", across, ops)
		}
		entries, _ := os.ReadDir(filepath.Dir(destination))
		for _, entry := range entries {
			if leftoverTemporary(entry.Name()) {
				t.Fatalf("across disks %v: no temporary copy left: %s", across, entry.Name())
			}
		}
		rename = renameNoReplace
	}
}

// A rename to a name no file has goes; one to a name a file has is refused.
func TestRenameNoReplace(t *testing.T) {
	folder := t.TempDir()
	a, b := filepath.Join(folder, "a.nsp"), filepath.Join(folder, "b.nsp")
	os.WriteFile(a, []byte("a"), 0o644)
	os.WriteFile(b, []byte("b"), 0o644)
	if err := renameNoReplace(a, b); !errors.Is(err, os.ErrExist) {
		t.Fatalf("refused: %v", err)
	}
	if data, _ := os.ReadFile(b); string(data) != "b" {
		t.Fatal("never written over")
	}
	c := filepath.Join(folder, "c.nsp")
	if err := renameNoReplace(a, c); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(c); string(data) != "a" {
		t.Fatal("renamed")
	}
}
