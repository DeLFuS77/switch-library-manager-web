package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// caseInsensitive tells whether the file system of the folder takes a name with another case as
// the same one (Windows, macOS, many shares of a NAS).
func caseInsensitive(t *testing.T, folder string) bool {
	path := filepath.Join(folder, "probe")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	_, err := os.Stat(filepath.Join(folder, "PROBE"))
	return err == nil
}

// linuxCaseRename is the rename of Linux on a file system that does not tell cases apart (a share
// of a NAS, a dataset that ignores case): the new name is the same file, so it does nothing and
// says it did.
func linuxCaseRename(t *testing.T) {
	t.Cleanup(func() { renameSys = os.Rename })
	renameSys = func(from string, to string) error {
		if strings.EqualFold(from, to) {
			return nil
		}
		return os.Rename(from, to)
	}
}

// A file or a folder takes its new case even where the system takes the rename as one to itself
// (#179 in Docker, on a share of a NAS).
func TestRenameCaseWhereTheSystemDoesNothing(t *testing.T) {
	linuxCaseRename(t)
	root := t.TempDir()
	from := filepath.Join(root, "Astral Chain [01007300020FA000]")
	if err := os.MkdirAll(from, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(from, "game.nsz"), []byte("data"), 0o644)
	to := filepath.Join(root, "ASTRAL CHAIN [01007300020FA000]")
	if err := renameCase(from, to); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "ASTRAL CHAIN [01007300020FA000]" {
		t.Fatalf("the folder: %v", entries)
	}
	if data, err := os.ReadFile(filepath.Join(to, "game.nsz")); err != nil || string(data) != "data" {
		t.Fatalf("its file: %q %v", data, err)
	}
}

// A folder whose name only changes its case is renamed too, so the next run finds nothing to do
// (#179: the same changes came back after every run), with the rename of Linux too.
func TestMoveRenamesAFolderThatOnlyChangesItsCase(t *testing.T) {
	t.Run("system", testMoveRenamesAFolderThatOnlyChangesItsCase)
	t.Run("linux", func(t *testing.T) {
		linuxCaseRename(t)
		testMoveRenamesAFolderThatOnlyChangesItsCase(t)
	})
}

func testMoveRenamesAFolderThatOnlyChangesItsCase(t *testing.T) {
	root := t.TempDir()
	if !caseInsensitive(t, root) {
		t.Skip("the file system tells cases apart: a folder with another case is another folder")
	}
	from := filepath.Join(root, "game x [0100000000010000]", "base", "game x.nsp")
	to := filepath.Join(root, "Game X [0100000000010000]", "Base", "Game X.nsp")
	if err := os.MkdirAll(filepath.Dir(from), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	ops := newFileOperations(false)
	if err := ops.move(from, to); err != nil {
		t.Fatal(err)
	}
	// the names as the folder lists them
	names := func(dir string) []string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	if got := strings.Join(names(root), ","); got != "Game X [0100000000010000]" {
		t.Fatalf("the game folder is %q", got)
	}
	if got := strings.Join(names(filepath.Join(root, "Game X [0100000000010000]")), ","); got != "Base" {
		t.Fatalf("the subfolder is %q", got)
	}
	if got := strings.Join(names(filepath.Dir(to)), ","); got != "Game X.nsp" {
		t.Fatalf("the file is %q", got)
	}
	if data, err := os.ReadFile(to); err != nil || string(data) != "data" {
		t.Fatalf("the file read back: %q %v", data, err)
	}
	// a second run plans nothing: the file is where it goes
	again := newFileOperations(true)
	if err := again.move(to, to); err != nil || len(again.operations) != 0 {
		t.Fatalf("a second run: %v %+v", err, again.operations)
	}
}
