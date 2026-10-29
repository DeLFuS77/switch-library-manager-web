package db

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// folderLink makes link a link to the folder target: a junction of Windows (mklink /J) when
// asked there, else a symlink; the test is skipped when the system makes none.
func folderLink(t *testing.T, target, link string, junction bool) {
	t.Helper()
	if junction {
		if runtime.GOOS != "windows" {
			t.Skip("junctions are made on Windows only")
		}
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Skipf("no junction here: %v %s", err, out)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no link to a folder here: %v", err)
	}
}

func writeGames(t *testing.T, folder string, names ...string) {
	t.Helper()
	for i, name := range names {
		path := filepath.Join(folder, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		// sizes apart: games of the same size are compared on the disk
		if err := os.WriteFile(path, make([]byte, 100+i), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// listed tells a library with files that are no game or duplicates (with keys loaded by other
// tests, the games made of zeros are also recognized by their names only).
func listed(localDB *LocalSwitchFilesDB) bool {
	for _, skipped := range localDB.Skipped {
		if skipped.ReasonCode == REASON_UNSUPPORTED_TYPE || skipped.ReasonCode == REASON_DUPLICATE || skipped.ReasonCode == REASON_OLD_UPDATE {
			return true
		}
	}
	return false
}

func scanLibrary(t *testing.T, folders ...string) (*LocalSwitchFilesDB, error) {
	t.Helper()
	manager, err := NewLocalSwitchDBManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	return manager.CreateLocalSwitchFilesDB(nil, t.TempDir(), folders, nil, true, true)
}

// A library folder that is a link (a symlink, a junction of Windows, as a share mounted on a
// folder) is scanned through it: the walk of the folders stopped at the link and found no game,
// without an error. Its files keep the path of the settings.
func TestScanFollowsALinkedLibraryFolder(t *testing.T) {
	for _, junction := range []bool{false, true} {
		t.Run(map[bool]string{false: "symlink", true: "junction"}[junction], func(t *testing.T) {
			real := t.TempDir()
			writeGames(t, real, "Game [0100000000010000][v0].nsp", filepath.Join("Updates", "Game [0100000000010800][v65536].nsp"))
			root := filepath.Join(t.TempDir(), "library")
			folderLink(t, real, root, junction)
			localDB, err := scanLibrary(t, root)
			if err != nil {
				t.Fatal(err)
			}
			game := localDB.TitlesMap["0100000000010"]
			if game == nil || !game.BaseExist || game.LatestUpdate != 65536 || listed(localDB) {
				t.Fatalf("the games through the link: %+v %v", game, localDB.Skipped)
			}
			if !strings.HasPrefix(game.File.ExtendedInfo.BaseFolder, root) || !strings.HasPrefix(game.Updates[65536].ExtendedInfo.BaseFolder, root) {
				t.Fatalf("by the path of the settings: %+v", game)
			}
		})
	}
}

// A folder of the library that is a link is walked as the folder it is, once: it was taken for
// a file that is no game. A link to a folder above is not walked again and again.
func TestScanFollowsLinkedFolders(t *testing.T) {
	for _, junction := range []bool{false, true} {
		t.Run(map[bool]string{false: "symlink", true: "junction"}[junction], func(t *testing.T) {
			lib := t.TempDir()
			other := t.TempDir()
			writeGames(t, lib, "Game [0100000000010000][v0].nsp")
			writeGames(t, other, "Game DLC [0100000000011001][v0].nsp")
			folderLink(t, other, filepath.Join(lib, "More"), junction)
			folderLink(t, lib, filepath.Join(lib, "Loop"), junction)
			localDB, err := scanLibrary(t, lib)
			if err != nil {
				t.Fatal(err)
			}
			game := localDB.TitlesMap["0100000000010"]
			if game == nil || !game.BaseExist || len(game.Dlc) != 1 || localDB.NumFiles != 2 || listed(localDB) {
				t.Fatalf("the game and the DLC in the linked folder, once each: %+v %d %v", game, localDB.NumFiles, localDB.Skipped)
			}
			if dlc := game.Dlc["0100000000011001"]; dlc.ExtendedInfo.BaseFolder != filepath.Join(lib, "More")+string(filepath.Separator) {
				t.Fatalf("the DLC by the path of the link: %+v", dlc.ExtendedInfo)
			}
		})
	}
}

// A folder of the library that cannot be read (its owner changed, a disk of the array failed)
// keeps the games found in it before: taken for gone they would count as deleted. The library
// folder itself that cannot be read fails the scan, as one that cannot be reached.
func TestUnreadableFolderKeepsItsGames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the folders are made unreadable by their permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads any folder")
	}
	lib := t.TempDir()
	writeGames(t, lib, filepath.Join("A", "Game [0100000000010000][v0].nsp"), filepath.Join("B", "Other [0100000000020000][v0].nsp"))
	manager, err := NewLocalSwitchDBManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	scan := func() (*LocalSwitchFilesDB, error) {
		return manager.CreateLocalSwitchFilesDB(nil, t.TempDir(), []string{lib}, nil, true, true)
	}
	if localDB, err := scan(); err != nil || len(localDB.TitlesMap) != 2 {
		t.Fatalf("both games: %v %v", err, localDB)
	}
	unreadable := filepath.Join(lib, "B")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(unreadable, 0o755)
	localDB, err := scan()
	if err != nil || len(localDB.TitlesMap) != 2 || localDB.TitlesMap["0100000000020"] == nil {
		t.Fatalf("the game of the folder that cannot be read stays: %v %v", err, localDB)
	}
	for _, folder := range localDB.EmptyFolders {
		if folder == unreadable {
			t.Fatal("a folder that cannot be read is not empty")
		}
	}

	if err := os.Chmod(lib, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(lib, 0o755)
	if _, err := scan(); err == nil {
		t.Fatal("a library folder that cannot be read fails the scan")
	}
}

func TestFilesIn(t *testing.T) {
	files := []ExtendedFileInfo{
		{FileName: "a.nsp", BaseFolder: filepath.Join("lib", "B") + string(filepath.Separator)},
		{FileName: "b.nsp", BaseFolder: filepath.Join("lib", "B", "Updates")},
		{FileName: "c.nsp", BaseFolder: filepath.Join("lib", "Bx")},
		{FileName: "d.nsp", BaseFolder: "lib"},
	}
	if got := filesIn(files, []string{filepath.Join("lib", "B")}); len(got) != 2 || got[0].FileName != "a.nsp" || got[1].FileName != "b.nsp" {
		t.Fatalf("the files in the folder and below: %+v", got)
	}
}
