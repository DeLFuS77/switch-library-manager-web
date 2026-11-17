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

// A folder whose name only changes its case is renamed too, so the next run finds nothing to do
// (#179: the same changes came back after every run).
func TestMoveRenamesAFolderThatOnlyChangesItsCase(t *testing.T) {
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
