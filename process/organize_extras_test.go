package process

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// releaseLibrary is the fixture library with its base game in a folder of its own, as a release
// comes: the game, its NFO named as it, an SFV and a Proof folder.
func releaseLibrary(t *testing.T, library string) (*db.LocalSwitchFilesDB, *db.SwitchTitlesDB, string) {
	t.Helper()
	local, remote := fixtureLibrary(t, library)
	if err := os.Remove(filepath.Join(library, "base.nsp")); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(library, "Test.Game.NSW-GRP")
	game := local.TitlesMap[groupID]
	game.File.ExtendedInfo = writeFixture(t, filepath.Join(release, "grp-test.nsp"), "base")
	writeFixture(t, filepath.Join(release, "grp-test.nfo"), "nfo")
	writeFixture(t, filepath.Join(release, "grp.sfv"), "sfv")
	writeFixture(t, filepath.Join(release, "Proof", "grp-proof.jpg"), "proof")
	return local, remote, release
}

// The files named as a game go with it and take its new name; the others of a library folder
// stay.
func TestExtrasNamedAsTheGameGoWithIt(t *testing.T) {
	library := t.TempDir()
	local, remote := fixtureLibrary(t, library)
	writeFixture(t, filepath.Join(library, "base.nfo"), "nfo")
	writeFixture(t, filepath.Join(library, "base.SFV"), "sfv")
	writeFixture(t, filepath.Join(library, "update.nfo"), "nfo")
	writeFixture(t, filepath.Join(library, "other.nfo"), "other")

	ops, err := OrganizeByFolders(library, local, remote, organizeOptions(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if failed := failedOps(ops); len(failed) != 0 {
		t.Fatalf("no errors: %+v", failed)
	}
	folder := filepath.Join(library, "Test Game")
	assertExists(t, filepath.Join(folder, "Test Game [0100E95004039000][BASE][v0].nfo"), true)
	assertExists(t, filepath.Join(folder, "Test Game [0100E95004039000][BASE][v0].SFV"), true)
	assertExists(t, filepath.Join(folder, "Updates", "Test Game [0100E95004039800][UPD][v65536].nfo"), true)
	assertExists(t, filepath.Join(library, "base.nfo"), false)
	assertExists(t, filepath.Join(library, "other.nfo"), true)
}

// A folder that holds only the game goes with it: its NFO, SFV and Proof folder, and the folder
// is left empty. The preview plans the very changes the run makes.
func TestReleaseFolderGoesWithItsGame(t *testing.T) {
	library := t.TempDir()
	local, remote, release := releaseLibrary(t, library)
	options := organizeOptions()
	options.DeleteEmptyFolders = true

	planned, err := OrganizeByFolders(library, local, remote, options, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := OrganizeByFolders(library, local, remote, options, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if failed := failedOps(ops); len(failed) != 0 {
		t.Fatalf("no errors: %+v", failed)
	}
	moves := func(ops []Operation) []Operation {
		list := []Operation{}
		for _, op := range ops {
			if op.Kind == OP_MOVE || op.Kind == OP_SKIP {
				list = append(list, op)
			}
		}
		return list
	}
	if want, got := moves(planned), moves(ops); len(want) != len(got) {
		t.Fatalf("the preview plans what the run does:\n%+v\n%+v", want, got)
	} else {
		for i := range want {
			if want[i] != got[i] {
				t.Fatalf("the preview plans what the run does: %+v != %+v", want[i], got[i])
			}
		}
	}
	folder := filepath.Join(library, "Test Game")
	assertExists(t, filepath.Join(folder, "Test Game [0100E95004039000][BASE][v0].nsp"), true)
	assertExists(t, filepath.Join(folder, "Test Game [0100E95004039000][BASE][v0].nfo"), true)
	assertExists(t, filepath.Join(folder, "grp.sfv"), true)
	assertExists(t, filepath.Join(folder, "Proof", "grp-proof.jpg"), true)
	assertExists(t, release, false)
}

// A file that goes with a game never overwrites another one: it stays where it is, and is told.
func TestExtraWhoseNameIsTakenStays(t *testing.T) {
	library := t.TempDir()
	local, remote, release := releaseLibrary(t, library)
	writeFixture(t, filepath.Join(library, "Test Game", "grp.sfv"), "another sfv")

	ops, err := OrganizeByFolders(library, local, remote, organizeOptions(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if failed := failedOps(ops); len(failed) != 0 {
		t.Fatalf("no errors: %+v", failed)
	}
	told := false
	for _, op := range ops {
		told = told || (op.Kind == OP_SKIP && op.From == filepath.Join(release, "grp.sfv") && op.Reason == REASON_EXTRA_NAME_TAKEN)
	}
	if !told {
		t.Fatalf("the file left is told: %+v", ops)
	}
	assertExists(t, filepath.Join(release, "grp.sfv"), true)
	if data, _ := os.ReadFile(filepath.Join(library, "Test Game", "grp.sfv")); string(data) != "another sfv" {
		t.Fatalf("the file at the destination is kept: %q", data)
	}
	assertExists(t, filepath.Join(library, "Test Game", "Proof", "grp-proof.jpg"), true)
}

// Turned off, the files of a game stay where they are.
func TestExtrasStayWhenAsked(t *testing.T) {
	library := t.TempDir()
	local, remote, release := releaseLibrary(t, library)
	options := organizeOptions()
	options.LeaveExtraFiles = true

	if _, err := OrganizeByFolders(library, local, remote, options, false, nil); err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(library, "Test Game", "Test Game [0100E95004039000][BASE][v0].nsp"), true)
	assertExists(t, filepath.Join(release, "grp-test.nfo"), true)
	assertExists(t, filepath.Join(release, "grp.sfv"), true)
	assertExists(t, filepath.Join(release, "Proof", "grp-proof.jpg"), true)
}

// A folder with several games, or with other folders, is no game's own: only the files named as
// a game go with it.
func TestSharedFolderKeepsItsExtras(t *testing.T) {
	library := t.TempDir()
	shared := filepath.Join(library, "Switch")
	local, remote := fixtureLibrary(t, shared)
	writeFixture(t, filepath.Join(shared, "base.nfo"), "nfo")
	writeFixture(t, filepath.Join(shared, "notes.nfo"), "notes")

	if _, err := OrganizeByFolders(library, local, remote, organizeOptions(), false, nil); err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(library, "Test Game", "Test Game [0100E95004039000][BASE][v0].nfo"), true)
	assertExists(t, filepath.Join(shared, "notes.nfo"), true)

	library = t.TempDir()
	local, remote, release := releaseLibrary(t, library)
	writeFixture(t, filepath.Join(release, "Sample", "sample.txt"), "sample")
	if _, err := OrganizeByFolders(library, local, remote, organizeOptions(), false, nil); err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(library, "Test Game", "Test Game [0100E95004039000][BASE][v0].nfo"), true)
	assertExists(t, filepath.Join(release, "grp.sfv"), true)
	assertExists(t, filepath.Join(release, "Proof", "grp-proof.jpg"), true)
}

// A game moved to the Base subfolder of its own folder stays in that folder: its images do not
// go down with it.
func TestGameMovedIntoItsSubfolderLeavesItsExtras(t *testing.T) {
	library := t.TempDir()
	local, remote := fixtureLibrary(t, library)
	folder := filepath.Join(library, "Test Game")
	if err := os.Remove(filepath.Join(library, "base.nsp")); err != nil {
		t.Fatal(err)
	}
	local.TitlesMap[groupID].File.ExtendedInfo = writeFixture(t, filepath.Join(folder, "base.nsp"), "base")
	writeFixture(t, filepath.Join(folder, "folder.jpg"), "cover")
	options := organizeOptions()
	options.BaseFolder = "Base"
	options.KeepGameFolders = true

	if _, err := OrganizeByFolders(library, local, remote, options, false, nil); err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(folder, "Base", "Test Game [0100E95004039000][BASE][v0].nsp"), true)
	assertExists(t, filepath.Join(folder, "folder.jpg"), true)
}

// Placing a new file takes its NFO along, though only the game is in the list of new files.
func TestNewFileTakesItsExtras(t *testing.T) {
	library := t.TempDir()
	local, remote := fixtureLibrary(t, library)
	writeFixture(t, filepath.Join(library, "base.nfo"), "nfo")
	writeFixture(t, filepath.Join(library, "update.nfo"), "nfo")

	ops, err := OrganizeNewFiles(library, []string{library}, []string{filepath.Join(library, "base.nsp")}, local, remote, organizeOptions(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if moves := countKind(ops, OP_MOVE); moves != 2 {
		t.Fatalf("the new file and its NFO move: %+v", ops)
	}
	assertExists(t, filepath.Join(library, "Test Game", "Test Game [0100E95004039000][BASE][v0].nfo"), true)
	assertExists(t, filepath.Join(library, "update.nfo"), true)
}
