package switchfs_test

import (
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A game rebuilt to stay small once installed stores its program with the LZ4 of the compressed
// storage of its NCA: reading the NSP tells it, and that it was looked at.
func TestReadNspMetadataTellsNcaLz4(t *testing.T) {
	keysDir := t.TempDir()
	if err := testnsp.WriteKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	defer settings.InitSwitchKeys(t.TempDir())

	for _, lz4 := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "game.nsp")
		if _, err := testnsp.WriteMultiTitleNsp(path, []testnsp.Title{
			{Id: 0x0100000000010000, Type: switchfs.ContentMetaType_Application, Version: 65536, Content: "the game ", Lz4: lz4},
			{Id: 0x0100000000011001, Type: switchfs.ContentMetaType_AddOnContent, Version: 1, Content: "the dlc ", Lz4: lz4},
		}); err != nil {
			t.Fatal(err)
		}
		metadata, err := switchfs.ReadNspMetadata(path)
		if err != nil {
			t.Fatal(err)
		}
		game, dlc := metadata["0100000000010000"], metadata["0100000000011001"]
		if game == nil || !game.NcaLz4Read || game.NcaLz4 != lz4 || game.Version != 65536 {
			t.Fatalf("game with LZ4 %v: %+v", lz4, game)
		}
		// a DLC is not looked at: only games and updates are offered to compress
		if dlc == nil || dlc.NcaLz4Read || dlc.NcaLz4 {
			t.Fatalf("DLC: %+v", dlc)
		}
	}
}
