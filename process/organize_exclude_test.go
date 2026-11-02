package process

import (
	"os"
	"path/filepath"
	"testing"
)

// Organize never moves a file into a folder the scan leaves out: the game would leave the
// library. It stays where it is, and the preview tells why.
func TestOrganizeLeavesOutTheFoldersTheScanSkips(t *testing.T) {
	library := t.TempDir()
	local, remote := fixtureLibrary(t, library)
	options := organizeOptions()
	options.ExcludeFolders = []string{"updates"}

	preview, err := OrganizeByFolders(library, local, remote, options, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasOp(preview, OP_SKIP, REASON_EXCLUDED_FOLDER) {
		t.Fatalf("the preview tells why: %+v", preview)
	}
	ops, err := OrganizeByFolders(library, local, remote, options, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if failed := failedOps(ops); len(failed) != 0 {
		t.Fatalf("no errors: %+v", failed)
	}
	assertExists(t, filepath.Join(library, "Test Game", "Test Game [0100E95004039000][BASE][v0].nsp"), true)
	assertExists(t, filepath.Join(library, "update.nsp"), true)
	assertExists(t, filepath.Join(library, "Test Game", "Updates"), false)

	// the game folder itself, by a pattern: nothing of the game moves
	library = t.TempDir()
	local, remote = fixtureLibrary(t, library)
	options.ExcludeFolders = []string{"test*"}
	if _, err := OrganizeByFolders(library, local, remote, options, false, nil); err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(library, "base.nsp"), true)
	assertExists(t, filepath.Join(library, "Test Game"), false)
}

// Only the folders inside the library count: the name of the library folder, or of the ones
// above it, leaves nothing out.
func TestExcludedFoldersStartBelowTheLibrary(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Updates")
	excluded := excludedFolders([]string{library}, []string{"updates"})
	if excluded(library) || excluded(filepath.Join(library, "Game")) {
		t.Fatal("the library folder is no folder left out")
	}
	if !excluded(filepath.Join(library, "Game", "Updates")) || !excluded(filepath.Join(library, "Updates", "Game")) {
		t.Fatal("a folder below it, or inside one")
	}
	if excluded(filepath.Join(filepath.Dir(library), "Elsewhere", "Updates")) {
		t.Fatal("outside the library nothing is scanned")
	}
	if excludedFolders([]string{library}, nil)(filepath.Join(library, "Game", "Updates")) {
		t.Fatal("without patterns nor the file nothing is left out")
	}
}

// A folder with the file .slmignore in it is left out, with the folders inside it, whatever
// their names; the library folder is not, even with the file.
func TestFoldersWithTheMarkerAreLeftOut(t *testing.T) {
	library := t.TempDir()
	tests := filepath.Join(library, "Tests")
	os.MkdirAll(filepath.Join(tests, "Game"), 0o755)
	os.WriteFile(filepath.Join(tests, ".slmignore"), nil, 0o644)
	os.WriteFile(filepath.Join(library, ".slmignore"), nil, 0o644)
	excluded := excludedFolders([]string{library}, nil)
	if !excluded(tests) || !excluded(filepath.Join(tests, "Game")) {
		t.Fatal("the folder with the file, and the ones inside it")
	}
	if excluded(library) || excluded(filepath.Join(library, "Zelda")) {
		t.Fatal("the library folder is read even with the file")
	}
}
