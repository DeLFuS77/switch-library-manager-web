package db

import (
	"path/filepath"
	"testing"
)

// A folder listed twice, or inside another one scanned with its subfolders, is read once: its
// files would count as duplicates of themselves.
func TestDistinctFolders(t *testing.T) {
	root := filepath.Join("lib", "roms")
	inside := filepath.Join(root, "updates")
	other := filepath.Join("lib", "romsx")
	folders := []string{root, root + string(filepath.Separator), inside, other}
	if got := distinctFolders(folders, true); len(got) != 2 || got[0] != root || got[1] != other {
		t.Fatalf("with subfolders: %v", got)
	}
	if got := distinctFolders(folders, false); len(got) != 3 || got[1] != inside {
		t.Fatalf("without subfolders the folder inside is read on its own: %v", got)
	}
}

func TestDistinctFiles(t *testing.T) {
	files := []ExtendedFileInfo{
		{FileName: "a.nsp", BaseFolder: "roms"},
		{FileName: "a.nsp", BaseFolder: "roms" + string(filepath.Separator)},
		{FileName: "b.nsp", BaseFolder: "roms"},
	}
	if got := distinctFiles(files); len(got) != 2 {
		t.Fatalf("each file once: %+v", got)
	}
}
