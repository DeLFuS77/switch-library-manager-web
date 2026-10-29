package db

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// otherPath is folder written in another way that reaches the same folder: with \\?\ in front
// on Windows, a link to it elsewhere; the test is skipped when the system makes no link.
func otherPath(t *testing.T, folder string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `\\?\` + folder
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(folder, link); err != nil {
		t.Skipf("no link to a folder here: %v", err)
	}
	return link
}

// One folder reached by two paths is scanned once, and so is one inside it reached by another
// path: its only copy of each game would count as a duplicate of itself.
func TestDistinctFoldersOnTheDisk(t *testing.T) {
	lib := t.TempDir()
	if err := os.Mkdir(filepath.Join(lib, "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := otherPath(t, lib)
	if got := distinctFolders([]string{lib, alias}, true); len(got) != 1 || got[0] != lib {
		t.Fatalf("one folder by two paths: %v", got)
	}
	if got := distinctFolders([]string{filepath.Join(alias, "new"), lib}, true); len(got) != 1 || got[0] != lib {
		t.Fatalf("a folder inside the other by another path: %v", got)
	}
	if got := distinctFolders([]string{filepath.Join(alias, "new"), lib}, false); len(got) != 2 {
		t.Fatalf("without subfolders the folder inside is read on its own: %v", got)
	}
}

// A game seen by two paths is one game, never a duplicate of itself; the scan keeps it once.
func TestGameSeenByTwoPathsIsNoDuplicate(t *testing.T) {
	lib := t.TempDir()
	name := "Game [0100000000010000][v0].nsp"
	if err := os.WriteFile(filepath.Join(lib, name), make([]byte, 300), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := otherPath(t, lib)
	files := []ExtendedFileInfo{{FileName: name, BaseFolder: lib, Size: 300}, {FileName: name, BaseFolder: alias, Size: 300}}
	if got := distinctFiles(files); len(got) != 1 || got[0].BaseFolder != lib {
		t.Fatalf("the game seen first stays, once: %+v", got)
	}

	manager, err := NewLocalSwitchDBManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	localDB, err := manager.CreateLocalSwitchFilesDB(nil, t.TempDir(), []string{lib, alias}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if game := localDB.TitlesMap["0100000000010"]; game == nil || !game.BaseExist {
		t.Fatalf("the game is in the library: %v", localDB.TitlesMap)
	}
	for file, skipped := range localDB.Skipped {
		if skipped.ReasonCode == REASON_DUPLICATE {
			t.Fatalf("a duplicate of itself: %v %v", file, skipped)
		}
	}
}

// A link to a game is not a second copy of it: the game stays in the library, and the link,
// whose own size is the length of the path it points to, is never the smaller copy to keep.
func TestLinkToAGameIsNoCopy(t *testing.T) {
	lib := t.TempDir()
	game := filepath.Join(lib, "Game [0100000000010000][v0].nsp")
	if err := os.WriteFile(game, make([]byte, 3000), 0o644); err != nil {
		t.Fatal(err)
	}
	// seen before the game
	link := filepath.Join(lib, "A link [0100000000010000][v0].nsp")
	if err := os.Symlink(game, link); err != nil {
		t.Skipf("no link to a file here: %v", err)
	}
	files := []ExtendedFileInfo{}
	if err := scanFolder(lib, true, &files, nil, nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Size != 3000 {
			t.Fatalf("the size of the game, also through the link: %+v", files)
		}
	}
	if got := distinctFiles(files); len(got) != 1 || fullPath(got[0]) != game {
		t.Fatalf("the game stays, not the link: %+v", got)
	}
}
