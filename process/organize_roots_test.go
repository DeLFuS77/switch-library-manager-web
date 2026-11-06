package process

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// Each file of a game is organized within the folder of the library it lies in: the game in the
// library folder, its update and DLC in an extra folder, which may be another disk, each in a
// folder of the game named the same, and none moves from one to the other.
func TestEachFileStaysInItsLibraryFolder(t *testing.T) {
	library, extra := t.TempDir(), t.TempDir()
	local, remote := fixtureLibrary(t, extra)
	game := local.TitlesMap[groupID]
	os.Remove(filepath.Join(extra, "base.nsp"))
	game.File.ExtendedInfo = writeFixture(t, filepath.Join(library, "base.nsp"), "base")

	ops, err := OrganizeLibrary(library, []string{library, extra}, local, remote, organizeOptions(), false, nil)
	if err != nil || len(failedOps(ops)) != 0 {
		t.Fatalf("organize: %v %+v", err, failedOps(ops))
	}
	assertExists(t, filepath.Join(library, "Test Game", "Test Game [0100E95004039000][BASE][v0].nsp"), true)
	assertExists(t, filepath.Join(extra, "Test Game", "Updates", "Test Game [0100E95004039800][UPD][v65536].nsp"), true)
	for _, root := range []string{library, extra} {
		other := extra
		if root == extra {
			other = library
		}
		for _, op := range ops {
			if op.Kind == OP_MOVE && within(op.From, root) && within(op.To, other) {
				t.Fatalf("moved to another folder of the library: %+v", op)
			}
		}
	}
	dlc, err := os.ReadDir(filepath.Join(extra, "Test Game", "DLC"))
	if err != nil || len(dlc) != 2 {
		t.Fatalf("the two DLC in the folder of the game in the extra folder: %v %v", dlc, err)
	}
	assertExists(t, filepath.Join(library, "Test Game", "Updates"), false)
}

// Without a folder for each game, the subfolders of the updates and DLC are made in the folder of
// the library each file lies in.
func TestSubfoldersInTheLibraryFolderOfTheFile(t *testing.T) {
	library, extra := t.TempDir(), t.TempDir()
	local, remote := fixtureLibrary(t, extra)
	options := organizeOptions()
	options.CreateFolderPerGame = false

	ops, err := OrganizeLibrary(library, []string{library, extra}, local, remote, options, false, nil)
	if err != nil || len(failedOps(ops)) != 0 {
		t.Fatalf("organize: %v %+v", err, failedOps(ops))
	}
	assertExists(t, filepath.Join(extra, "Updates", "Test Game [0100E95004039800][UPD][v65536].nsp"), true)
	assertExists(t, filepath.Join(library, "Updates"), false)
	assertExists(t, filepath.Join(library, "DLC"), false)
}

// A folder of the library inside another one is part of it: its files go to the folders of the
// games in the outer one, by a rename, as before.
func TestLibraryFolderInsideAnotherMovesIntoIt(t *testing.T) {
	library := t.TempDir()
	incoming := filepath.Join(library, "Incoming")
	local, remote := fixtureLibrary(t, incoming)

	ops, err := OrganizeLibrary(library, []string{library, incoming}, local, remote, organizeOptions(), false, nil)
	if err != nil || len(failedOps(ops)) != 0 {
		t.Fatalf("organize: %v %+v", err, failedOps(ops))
	}
	assertExists(t, filepath.Join(library, "Test Game", "Test Game [0100E95004039000][BASE][v0].nsp"), true)
	assertExists(t, filepath.Join(incoming, "Test Game"), false)
}

// A new file in an extra folder of the library goes to the folder its game has, named the same,
// in that extra folder: not to the one in the library folder.
func TestNewFileInAnExtraFolderStaysThere(t *testing.T) {
	library, extra := t.TempDir(), t.TempDir()
	folder := filepath.Join(library, "My Game")
	local, remote := fixtureLibrary(t, filepath.Join(folder, "Base"))
	game := local.TitlesMap[groupID]
	upload := writeFixture(t, filepath.Join(extra, "sxs-my_game_v131072.nsp"), "update")
	game.Updates[131072] = db.SwitchFileInfo{ExtendedInfo: upload, Metadata: metadataForID(updateID, 131072)}
	options := organizeOptions()
	options.BaseFolder = "Base"

	ops, err := OrganizeNewFiles(library, []string{library, extra}, []string{filepath.Join(extra, upload.FileName)}, local, remote, options, false, nil)
	if err != nil || len(failedOps(ops)) != 0 {
		t.Fatalf("organize: %v %+v", err, failedOps(ops))
	}
	if moves := countKind(ops, OP_MOVE); moves != 1 {
		t.Fatalf("only the new file moves: %+v", ops)
	}
	assertExists(t, filepath.Join(extra, "My Game", "Updates", "Test Game [0100E95004039800][UPD][v131072].nsp"), true)
	assertExists(t, filepath.Join(folder, "Updates"), false)
}

// The folder of the library of a path is the outermost one it lies in, and a folder of a game is
// moved to the folder of the library of a file by the same path below it.
func TestRootOfAndOnDiskOf(t *testing.T) {
	base, extra := filepath.Join(string(filepath.Separator), "mnt", "roms"), filepath.Join(string(filepath.Separator), "mnt", "roms2")
	inner := filepath.Join(base, "inner")
	o := &libraryOrganizer{baseFolder: base, roots: []string{base, extra, inner}}
	for path, want := range map[string]string{
		filepath.Join(extra, "Game", "a.nsp"): extra,
		filepath.Join(inner, "a.nsp"):         base,
		filepath.Join(base, "Game"):           base,
		extra:                                 extra,
		filepath.Join(string(filepath.Separator), "mnt", "roms3", "a.nsp"): base,
	} {
		if got := o.rootOf(path); got != want {
			t.Errorf("rootOf(%q) = %q, want %q", path, got, want)
		}
	}
	file := db.ExtendedFileInfo{BaseFolder: filepath.Join(extra, "loose")}
	if got, want := o.onDiskOf(file, filepath.Join(base, "Game [ID]")), filepath.Join(extra, "Game [ID]"); got != want {
		t.Errorf("onDiskOf: %q, want %q", got, want)
	}
	file.BaseFolder = base
	if got, want := o.onDiskOf(file, filepath.Join(base, "Game [ID]")), filepath.Join(base, "Game [ID]"); got != want {
		t.Errorf("onDiskOf in the same folder: %q, want %q", got, want)
	}
}

// within tells a path inside a folder.
func within(path string, folder string) bool {
	rel, err := filepath.Rel(folder, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && (len(rel) < 3 || rel[:3] != ".."+string(filepath.Separator))
}
