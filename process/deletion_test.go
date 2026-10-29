package process

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

func writeFile(t *testing.T, folder string, name string, size int) db.ExtendedFileInfo {
	t.Helper()
	if err := os.WriteFile(filepath.Join(folder, name), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return db.ExtendedFileInfo{FileName: name, BaseFolder: folder, Size: int64(size)}
}

func info(file db.ExtendedFileInfo, titleId string, version int) db.SwitchFileInfo {
	return db.SwitchFileInfo{ExtendedInfo: file, Metadata: &switchfs.ContentMetaAttributes{TitleId: titleId, Version: version}}
}

// A file with several contents (a complete pack, a card with its update) is listed by the
// scan as an old update or a duplicate for one of them. It must never be deleted while
// it also holds the game, its latest update or a DLC kept by the library.
func TestFilesHoldingKeptContentAreNeverDeleted(t *testing.T) {
	folder := t.TempDir()
	pack := writeFile(t, folder, "Game (pack).nsp", 30)
	update := writeFile(t, folder, "Game [v131072].nsp", 10)
	oldUpdate := writeFile(t, folder, "Game [v65536].nsp", 5)
	library := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {
				BaseExist:    true,
				MultiContent: true,
				File:         info(pack, "0100000000010000", 0),
				Updates: map[int]db.SwitchFileInfo{
					65536:  info(oldUpdate, "0100000000010800", 65536),
					131072: info(update, "0100000000010800", 131072),
				},
				LatestUpdate: 131072,
				Dlc:          map[string]db.SwitchFileInfo{"0100000000011001": info(pack, "0100000000011001", 0)},
			},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			// the update inside the pack is older than the separate one
			pack:      {ReasonCode: db.REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally"},
			oldUpdate: {ReasonCode: db.REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally"},
		},
	}
	operations := DeleteOldUpdates(folder, library, settings.OrganizeOptions{DeleteDuplicateFiles: true}, false, nil)

	if _, err := os.Stat(filepath.Join(folder, pack.FileName)); err != nil {
		t.Fatalf("the pack holds the game and its DLC: %+v", operations)
	}
	if _, err := os.Stat(filepath.Join(folder, oldUpdate.FileName)); !os.IsNotExist(err) {
		t.Fatalf("the old update alone is deleted: %+v", operations)
	}
	// the pack is the game: it is not listed as an old update kept, run after run
	for _, op := range operations {
		if op.From == filepath.Join(folder, pack.FileName) {
			t.Fatalf("the pack is not listed: %+v", operations)
		}
	}
}

func TestDuplicateHoldingADlcIsKept(t *testing.T) {
	folder := t.TempDir()
	base := writeFile(t, folder, "Game.nsp", 10)
	card := writeFile(t, folder, "Game.xci", 20)
	library := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {
				BaseExist: true,
				File:      info(base, "0100000000010000", 0),
				Updates:   map[int]db.SwitchFileInfo{},
				// the card is a duplicate of the game, but also the only file of the DLC
				Dlc: map[string]db.SwitchFileInfo{"0100000000011001": info(card, "0100000000011001", 0)},
			},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			card: {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file"},
		},
	}
	DeleteOldUpdates(folder, library, settings.OrganizeOptions{DeleteDuplicateFiles: true}, false, nil)
	if _, err := os.Stat(filepath.Join(folder, card.FileName)); err != nil {
		t.Fatal("the card holds the only copy of the DLC")
	}
	if len(FilesInUse(library)) != 2 {
		t.Fatalf("in use: %v", FilesInUse(library))
	}
}

// A duplicate that is the copy that stays, reached by another path (a library scanned before
// the scan knew), is never deleted: both would be gone. The quarantine is not asked either.
func TestDuplicateThatIsTheKeptFileIsNotDeleted(t *testing.T) {
	folder := t.TempDir()
	game := writeFile(t, folder, "Game [0100000000010000][v0].nsp", 30)
	alias := otherPath(t, folder)
	seen := db.ExtendedFileInfo{FileName: game.FileName, BaseFolder: alias, Size: game.Size}
	library := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {BaseExist: true, File: info(game, "0100000000010000", 0), Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			seen: {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + filepath.Join(folder, game.FileName) + ")"},
		},
	}
	discarded := []string{}
	discard := func(path string, _ string) error {
		discarded = append(discarded, path)
		return os.Remove(path)
	}
	for _, dryRun := range []bool{true, false} {
		operations := DeleteOldUpdatesTo(discard, folder, library, settings.OrganizeOptions{DeleteDuplicateFiles: true}, dryRun, nil)
		if len(operations) != 1 || operations[0].Kind != OP_SKIP || operations[0].Reason != REASON_SAME_FILE {
			t.Fatalf("dry run %v: kept with why: %+v", dryRun, operations)
		}
	}
	if _, err := os.Stat(filepath.Join(folder, game.FileName)); err != nil || len(discarded) != 0 {
		t.Fatalf("the only copy stays: %v %v", err, discarded)
	}
}

// otherPath is folder written in another way that reaches the same folder: with \?\ in front
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

// A library folder inside another one (settings of an older version, or edited by hand) is
// never deleted when the cleanup leaves it empty: every scan after failed, as with a folder that
// cannot be reached. The other folders it leaves empty go.
func TestCleanupKeepsALibraryFolderLeftEmpty(t *testing.T) {
	root := t.TempDir()
	nested, other := filepath.Join(root, "New"), filepath.Join(root, "Other")
	for _, folder := range []string{nested, other} {
		if err := os.Mkdir(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	update := writeFile(t, root, "Game [v131072].nsp", 10)
	oldInNested := writeFile(t, nested, "Game [v65536].nsp", 5)
	oldInOther := writeFile(t, other, "Game [v0].nsp", 4)
	library := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {Updates: map[int]db.SwitchFileInfo{131072: info(update, "0100000000010800", 131072)}, LatestUpdate: 131072, Dlc: map[string]db.SwitchFileInfo{}},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			oldInNested: {ReasonCode: db.REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally"},
			oldInOther:  {ReasonCode: db.REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally"},
		},
	}
	options := settings.OrganizeOptions{DeleteEmptyFolders: true}
	operations := CleanupLibrary(nil, root, []string{root, nested}, library, options, false, nil)
	if len(failedOps(operations)) != 0 {
		t.Fatalf("no errors: %+v", failedOps(operations))
	}
	if _, err := os.Stat(filepath.Join(nested, oldInNested.FileName)); !os.IsNotExist(err) {
		t.Fatalf("the old update goes: %+v", operations)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("the library folder stays: %+v", operations)
	}
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatalf("the folder left empty goes: %+v", operations)
	}
}
