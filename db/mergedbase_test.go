package db

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A game rebuilt with its update in it takes the version of the update: it is the game of the
// library and holds the update, and the game as it was stays, never deleted as its duplicate.
func TestMergedBaseIsAnotherVersion(t *testing.T) {
	keysDir := t.TempDir()
	if err := testnsp.WriteKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	defer settings.InitSwitchKeys(t.TempDir())

	// both orders: the scan reads the files of a folder in any order
	for _, names := range [][2]string{{"a.nsp", "b.nsp"}, {"b.nsp", "a.nsp"}} {
		original, merged := names[0], names[1]
		folder := t.TempDir()
		for name, title := range map[string]testnsp.Title{
			original: {Id: 0x0100000000010000, Type: switchfs.ContentMetaType_Application, Version: 0, Content: "the game "},
			merged:   {Id: 0x0100000000010000, Type: switchfs.ContentMetaType_Application, Version: 131072, Content: "the game updated ", Lz4: true},
		} {
			if _, err := testnsp.WriteMultiTitleNsp(filepath.Join(folder, "Game "+name), []testnsp.Title{title}); err != nil {
				t.Fatal(err)
			}
		}
		manager, err := NewLocalSwitchDBManager(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		localDB, err := manager.CreateLocalSwitchFilesDB(nil, t.TempDir(), []string{folder}, nil, true, true)
		manager.Close()
		if err != nil {
			t.Fatal(err)
		}
		game := localDB.TitlesMap["0100000000010"]
		if game == nil || !strings.HasSuffix(game.File.ExtendedInfo.FileName, merged) || game.UpdateLevel() != 131072 || game.LatestUpdate != 0 {
			t.Fatalf("the merged game is the game, with its update (%s merged): %+v", merged, game)
		}
		if !game.File.Metadata.NcaLz4 {
			t.Fatal("the merged game is stored with the LZ4 of its NCA")
		}
		found := false
		for file, skipped := range localDB.Skipped {
			if strings.HasSuffix(file.FileName, original) {
				found = true
				if skipped.ReasonCode != REASON_DUPLICATE || !skipped.OtherVersion || !KeptInOtherFormat(file, skipped) {
					t.Fatalf("the game as it was stays as another version: %+v", skipped)
				}
			}
		}
		if !found {
			t.Fatal("the game as it was is listed")
		}
	}
}

// Data of an NSP cached before the app looked at the LZ4 of its NCA is read again, once; an NSZ
// is compressed already and a DLC is not looked at.
func TestNeedsNcaLz4(t *testing.T) {
	old := map[string]*switchfs.ContentMetaAttributes{"0100000000010000": {TitleId: "0100000000010000", Type: "BASE"}}
	if !needsNcaLz4("Game.nsp", old) || needsNcaLz4("Game.nsz", old) {
		t.Fatal("an NSP cached before is read again, an NSZ is not")
	}
	old["0100000000010000"].NcaLz4Read = true
	if needsNcaLz4("Game.nsp", old) {
		t.Fatal("read once")
	}
	dlc := map[string]*switchfs.ContentMetaAttributes{"0100000000011001": {TitleId: "0100000000011001", Type: "DLC"}}
	if needsNcaLz4("Dlc.nsp", dlc) {
		t.Fatal("a DLC is not looked at")
	}
}
