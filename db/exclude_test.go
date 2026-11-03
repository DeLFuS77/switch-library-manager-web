package db

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeGame(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("game"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scannedNames(t *testing.T, root string, files []ExtendedFileInfo) string {
	t.Helper()
	names := []string{}
	for _, file := range files {
		rel, _ := filepath.Rel(root, filepath.Join(file.BaseFolder, file.FileName))
		names = append(names, filepath.ToSlash(rel))
	}
	sort.Strings(names)
	return strings.Join(names, "|")
}

// A folder left out by its name is not read, with its subfolders: its games, its temporary
// files and its empty folders are never the library's, and the folder it is in, holding it,
// is never taken for an empty one.
func TestScanSkipsExcludedFolders(t *testing.T) {
	root := t.TempDir()
	writeGame(t, filepath.Join(root, "Zelda", "Zelda [01007EF00011E000][v0].nsp"))
	writeGame(t, filepath.Join(root, "_build", "a.nsp"))
	writeGame(t, filepath.Join(root, "_build", "deep", "b.nsp"))
	writeGame(t, filepath.Join(root, "_build", ".b.nsp.tmp"))
	writeGame(t, filepath.Join(root, "Games", "Old.CACHE", "c.nsp"))
	writeGame(t, filepath.Join(root, "games_old", "d.nsp"))
	writeGame(t, filepath.Join(root, "Only", "_build", "e.nsp"))
	os.MkdirAll(filepath.Join(root, "cache", "empty"), 0o755)

	files := []ExtendedFileInfo{}
	leftovers := []LeftoverFile{}
	empty := []string{}
	if err := scanFolder(root, true, []string{"_build", "cache", "*_old"}, &files, &leftovers, &empty, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if got := scannedNames(t, root, files); got != "Zelda/Zelda [01007EF00011E000][v0].nsp" {
		t.Fatalf("files: %v", got)
	}
	if len(leftovers) != 0 || len(empty) != 0 {
		t.Fatalf("leftovers %v, empty folders %v", leftovers, empty)
	}

	// without the setting, as before it existed, every folder is read
	files = []ExtendedFileInfo{}
	if err := scanFolder(root, true, nil, &files, nil, nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if len(files) != 6 {
		t.Fatalf("every game: %v", scannedNames(t, root, files))
	}
}

// A folder chosen to be scanned is read whatever its name: only the folders below it are left
// out by theirs.
func TestScanNeverSkipsTheFolderChosen(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Cache")
	writeGame(t, filepath.Join(root, "a.nsp"))
	writeGame(t, filepath.Join(root, "Mario", "b.nsp"))
	writeGame(t, filepath.Join(root, "cache2", "c.nsp"))
	files := []ExtendedFileInfo{}
	if err := scanFolder(root, true, []string{"cache"}, &files, nil, nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if got := scannedNames(t, root, files); got != "Mario/b.nsp|a.nsp" {
		t.Fatalf("files: %v", got)
	}
}

// A folder chosen inside one the scan of another folder leaves out is read on its own; inside
// a folder read with the other one, it is not read twice.
func TestFolderInsideAnExcludedOneIsScannedOnItsOwn(t *testing.T) {
	root := t.TempDir()
	chosen := filepath.Join(root, "_build", "Games")
	inside := filepath.Join(root, "More")
	for _, folder := range []string{chosen, inside} {
		os.MkdirAll(folder, 0o755)
	}
	exclude := []string{"_build"}
	if got := distinctFolders([]string{root, chosen, inside}, true, exclude); len(got) != 2 || got[0] != root || got[1] != chosen {
		t.Fatalf("folders: %v", got)
	}
	if got := distinctFolders([]string{root, filepath.Join(root, "_build")}, true, exclude); len(got) != 2 {
		t.Fatalf("the folder left out itself, chosen: %v", got)
	}
	if got := distinctFolders([]string{root, chosen}, true, nil); len(got) != 1 {
		t.Fatalf("without the setting it is read with the other one: %v", got)
	}
}

// A folder with the file .slmignore in it is not read, whatever its name, with its subfolders,
// also the ones read before the file was found (# and ( sort before the dot); the folder chosen
// is read even with the file in it.
func TestScanSkipsFoldersWithTheMarker(t *testing.T) {
	root := t.TempDir()
	writeGame(t, filepath.Join(root, "Zelda", "Zelda [01007EF00011E000][v0].nsp"))
	writeGame(t, filepath.Join(root, "Tests", ".slmignore"))
	writeGame(t, filepath.Join(root, "Tests", "a.nsp"))
	writeGame(t, filepath.Join(root, "Tests", "#old", "b.nsp"))
	writeGame(t, filepath.Join(root, "Tests", "(copy)", "c.nsp"))
	writeGame(t, filepath.Join(root, "Tests", "deep", "d.nsp"))
	writeGame(t, filepath.Join(root, "Tests", "(copy)", ".c.nsp.tmp"))
	os.MkdirAll(filepath.Join(root, "Tests", "#empty"), 0o755)
	writeGame(t, filepath.Join(root, ".slmignore"))
	writeGame(t, filepath.Join(root, "root.nsp"))

	files := []ExtendedFileInfo{{FileName: "before.nsp", BaseFolder: "/elsewhere/"}}
	leftovers := []LeftoverFile{}
	empty := []string{}
	if err := scanFolder(root, true, nil, &files, &leftovers, &empty, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if files[0].FileName != "before.nsp" {
		t.Fatalf("the files of the folders scanned before are kept: %v", files[0])
	}
	if got := scannedNames(t, root, files[1:]); got != "Zelda/Zelda [01007EF00011E000][v0].nsp|root.nsp" {
		t.Fatalf("files: %v", got)
	}
	if len(leftovers) != 0 || len(empty) != 0 {
		t.Fatalf("leftovers %v, empty folders %v", leftovers, empty)
	}
	// a folder chosen inside the marked one is read on its own
	chosen := filepath.Join(root, "Tests", "deep")
	if got := distinctFolders([]string{root, chosen}, true, nil); len(got) != 2 {
		t.Fatalf("folders: %v", got)
	}
}
