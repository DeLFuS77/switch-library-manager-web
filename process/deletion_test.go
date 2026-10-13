package process

import (
	"os"
	"path/filepath"
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
	skipped := false
	for _, op := range operations {
		skipped = skipped || op.Kind == OP_SKIP && op.From == filepath.Join(folder, pack.FileName)
	}
	if !skipped {
		t.Fatalf("the pack is reported as kept: %+v", operations)
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
