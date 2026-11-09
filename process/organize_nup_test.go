package process

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A NUP with a game of the Switch 2 and its update is one file: Organize moves it once, into the
// folder of its game, named with the update it holds and keeping its extension, and a second run
// leaves it where it is.
func TestNupIsOrganizedAsOneFile(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	nup := writeFixture(t, filepath.Join(source, "zelda-oot.nup"), "nup")
	local := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0400486024dec": {BaseExist: true, MultiContent: true, LatestUpdate: 131072,
			File: db.SwitchFileInfo{ExtendedInfo: nup, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0400486024dec000", Version: 65536, Type: "BASE"}},
			Updates: map[int]db.SwitchFileInfo{
				131072: {ExtendedInfo: nup, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0400486024dec800", Version: 131072, Type: "UPD"}},
			},
			Dlc: map[string]db.SwitchFileInfo{}},
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}
	remote := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0400486024dec": {Attributes: db.TitleAttributes{Id: "0400486024DEC000", Name: "The Legend of Zelda™: Ocarina of Time"}},
	}}

	ops, err := OrganizeByFolders(target, local, remote, organizeOptions(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if failed := failedOps(ops); len(failed) > 0 {
		t.Fatalf("failed: %+v", failed)
	}
	moved := []string{}
	for _, op := range ops {
		if op.Kind == OP_MOVE {
			if op.From != filepath.Join(source, "zelda-oot.nup") {
				t.Fatalf("only the NUP moves: %+v", op)
			}
			moved = append(moved, op.To)
		}
	}
	if len(moved) != 1 {
		t.Fatalf("the NUP moves once: %v", moved)
	}
	to := moved[0]
	if filepath.Base(filepath.Dir(to)) != "The Legend of Zelda Ocarina of Time" || !strings.HasSuffix(to, "[0400486024DEC000][BASE][v131072].nup") {
		t.Fatalf("moved to %s", to)
	}
	assertExists(t, to, true)

	// the next scan finds it where the first run put it
	relocate(local, filepath.Join(source, "zelda-oot.nup"), to)
	ops, err = OrganizeByFolders(target, local, remote, organizeOptions(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := countKind(ops, OP_MOVE); n != 0 {
		t.Fatalf("a second run moves nothing: %+v", ops)
	}
}

// The cleanup deletes a duplicate of the same format, never a NUP for an NSP of the same game nor
// the other way round, and does not list them on every run.
func TestCleanupKeepsANupAndAnNspOfAGame(t *testing.T) {
	folder := t.TempDir()
	nsp := writeFile(t, folder, "Game [0100000000010000][v0].nsp", 30)
	nup := writeFile(t, folder, "Game [0100000000010000][v0].nup", 20)
	copy := writeFile(t, folder, "Game [0100000000010000][v0] (1).nsp", 30)
	nupGame := writeFile(t, folder, "Other [0100000000020000][v0].nup", 20)
	otherNsp := writeFile(t, folder, "Other [0100000000020000][v0].nsp", 30)
	library := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {BaseExist: true, File: info(nsp, "0100000000010000", 0), Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}},
			"0100000000020": {BaseExist: true, File: info(nupGame, "0100000000020000", 0), Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}},
		},
		// the scan lists the copy of the other format as such, never as a duplicate
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			nup:      {ReasonCode: db.REASON_OTHER_FORMAT, ReasonText: "same game in the other format, a NUP for emulators and an NSP or XCI to install: both kept (" + filepath.Join(folder, nsp.FileName) + ")"},
			copy:     {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + filepath.Join(folder, nsp.FileName) + ")"},
			otherNsp: {ReasonCode: db.REASON_OTHER_FORMAT, ReasonText: "same game in the other format, a NUP for emulators and an NSP or XCI to install: both kept (" + filepath.Join(folder, nupGame.FileName) + ")"},
		},
	}
	operations := DeleteOldUpdates(folder, library, settings.OrganizeOptions{DeleteDuplicateFiles: true}, false, nil)
	assertExists(t, filepath.Join(folder, copy.FileName), false)
	for _, file := range []db.ExtendedFileInfo{nsp, nup, nupGame, otherNsp} {
		assertExists(t, filepath.Join(folder, file.FileName), true)
	}
	for _, op := range operations {
		if op.From == filepath.Join(folder, nup.FileName) || op.From == filepath.Join(folder, otherNsp.FileName) {
			t.Fatalf("not listed: %+v", operations)
		}
	}
}

// A library cached by an older version lists the NUP of a game kept in an NSP as its duplicate:
// the cleanup keeps it all the same.
func TestCleanupKeepsANupListedAsADuplicateBefore(t *testing.T) {
	folder := t.TempDir()
	nsp := writeFile(t, folder, "Game [0100000000010000][v0].nsp", 30)
	nup := writeFile(t, folder, "Game [0100000000010000][v0].nup", 20)
	library := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {BaseExist: true, File: info(nsp, "0100000000010000", 0), Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			nup: {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + filepath.Join(folder, nsp.FileName) + ")"},
		},
	}
	operations := DeleteOldUpdates(folder, library, settings.OrganizeOptions{DeleteDuplicateFiles: true}, false, nil)
	assertExists(t, filepath.Join(folder, nup.FileName), true)
	if len(operations) != 0 {
		t.Fatalf("nothing to do: %+v", operations)
	}
}
