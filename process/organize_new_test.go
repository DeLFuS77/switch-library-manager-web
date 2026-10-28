package process

import (
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// A new update uploaded to the library folder goes to the folder the game has already, named
// by hand, and nothing else of the library moves.
func TestNewFileGoesToTheFolderOfItsGame(t *testing.T) {
	library := t.TempDir()
	folder := filepath.Join(library, "My Game")
	local, remote := fixtureLibrary(t, filepath.Join(folder, "Base"))
	game := local.TitlesMap[groupID]
	upload := writeFixture(t, filepath.Join(library, "sxs-my_game_v65536.nsp"), "update")
	game.Updates[65536] = db.SwitchFileInfo{ExtendedInfo: upload, Metadata: metadataForID(updateID, 65536)}
	options := organizeOptions()
	options.BaseFolder = "Base"

	ops, err := OrganizeNewFiles(library, []string{library}, []string{filepath.Join(library, upload.FileName)}, local, remote, options, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if failed := failedOps(ops); len(failed) != 0 {
		t.Fatalf("no errors: %+v", failed)
	}
	if moves := countKind(ops, OP_MOVE); moves != 1 {
		t.Fatalf("only the new file moves: %+v", ops)
	}
	if skips := countKind(ops, OP_SKIP); skips != 0 {
		t.Fatalf("nothing told of the other files: %+v", ops)
	}
	assertExists(t, filepath.Join(folder, "Updates", "Test Game [0100E95004039800][UPD][v65536].nsp"), true)
	assertExists(t, filepath.Join(folder, "Base", "base.nsp"), true)
	assertExists(t, filepath.Join(folder, "Base", "dlc-one.nsp"), true)
	assertExists(t, filepath.Join(library, "Test Game"), false)
}

// A new game with no folder yet gets one from the template, with its base in the Base
// subfolder.
func TestNewGameGetsANewFolder(t *testing.T) {
	library := t.TempDir()
	local, remote := fixtureLibrary(t, library)
	options := organizeOptions()
	options.BaseFolder = "Base"
	base := filepath.Join(library, "base.nsp")

	ops, err := OrganizeNewFiles(library, []string{library}, []string{base}, local, remote, options, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if moves := countKind(ops, OP_MOVE); moves != 1 {
		t.Fatalf("only the new file moves: %+v", ops)
	}
	assertExists(t, filepath.Join(library, "Test Game", "Base", "Test Game [0100E95004039000][BASE][v0].nsp"), true)
	assertExists(t, filepath.Join(library, "update.nsp"), true)
}

// With the folders kept, Organize puts the files of a game in the folder it has, without
// renaming it; without them, in the one of the template.
func TestKeepGameFolders(t *testing.T) {
	library := t.TempDir()
	folder := filepath.Join(library, "Mi juego")
	local, remote := fixtureLibrary(t, folder)
	options := organizeOptions()
	options.KeepGameFolders = true

	if _, err := OrganizeByFolders(library, local, remote, options, false, nil); err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(folder, "Test Game [0100E95004039000][BASE][v0].nsp"), true)
	assertExists(t, filepath.Join(folder, "Updates", "Test Game [0100E95004039800][UPD][v65536].nsp"), true)
	assertExists(t, filepath.Join(library, "Test Game"), false)

	library = t.TempDir()
	local, remote = fixtureLibrary(t, filepath.Join(library, "Mi juego"))
	options.KeepGameFolders = false
	if _, err := OrganizeByFolders(library, local, remote, options, false, nil); err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(library, "Test Game", "Test Game [0100E95004039000][BASE][v0].nsp"), true)
}

// A folder with the base files of several games is no game's own: each one gets its folder.
func TestSharedFolderIsNoGamesOwn(t *testing.T) {
	library := t.TempDir()
	shared := filepath.Join(library, "Switch")
	local, _ := fixtureLibrary(t, shared)
	other := writeFixture(t, filepath.Join(shared, "other.nsp"), "other")
	local.TitlesMap["0100aaaa00000"] = &db.SwitchGameFiles{File: db.SwitchFileInfo{ExtendedInfo: other, Metadata: metadataForID("0100AAAA00000000", 0)}, BaseExist: true}
	options := organizeOptions()
	options.KeepGameFolders = true

	if folders := gameFolders([]string{library}, local, options, nil); len(folders) != 0 {
		t.Fatalf("no own folders: %v", folders)
	}
}

// The folder of a game is found from an update or DLC in its subfolders, also in the folders
// named with their IDs.
func TestGameFolderFromItsSubfolders(t *testing.T) {
	library := t.TempDir()
	local, _ := fixtureLibrary(t, library)
	game := local.TitlesMap[groupID]
	game.BaseExist = false
	game.Updates[65536] = db.SwitchFileInfo{ExtendedInfo: writeFixture(t, filepath.Join(library, "Game [0100E95004039000]", "Update [0100E95004039800]", "u.nsp"), "u"), Metadata: metadataForID(updateID, 65536)}
	game.Dlc = map[string]db.SwitchFileInfo{}
	options := organizeOptions()
	options.FoldersWithIds = true

	folders := gameFolders([]string{library}, local, options, nil)
	if want := filepath.Join(library, "Game [0100E95004039000]"); folders[groupID] != want {
		t.Fatalf("folder: %q, want %q", folders[groupID], want)
	}
}
