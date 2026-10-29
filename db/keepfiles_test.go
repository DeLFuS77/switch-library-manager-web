package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A copy chosen in Issues that the app compressed or converted since stays the copy chosen, by
// the name it took in its folder: by its old name only, the choice of the settings decided again
// and the Space page offered to delete the very copy chosen.
func TestTheCopyChosenStaysChosenOnceConverted(t *testing.T) {
	romDir := t.TempDir()
	card := filepath.Join(romDir, "Game [0100000000010000][v0].xci")
	for name, size := range map[string]int{"Game [0100000000010000][v0].xcz": 30, "Other [0100000000010000][v0].nsz": 20} {
		if err := os.WriteFile(filepath.Join(romDir, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewLocalSwitchDBManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	dataFolder := t.TempDir()
	// the card was chosen, and compressed since: the XCZ is the copy chosen now
	settings.UpdateSettings(dataFolder, func(s *settings.AppSettings) {
		s.OrganizeOptions.KeepDuplicate = settings.KEEP_NSP
		s.OrganizeOptions.KeepFiles = []string{card}
	})
	localDB, err := manager.CreateLocalSwitchFilesDB(nil, dataFolder, []string{romDir}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if game := localDB.TitlesMap["0100000000010"]; game == nil || filepath.Ext(game.File.ExtendedInfo.FileName) != ".xcz" {
		t.Fatalf("the compressed card is the copy chosen: %#v", game)
	}
}
