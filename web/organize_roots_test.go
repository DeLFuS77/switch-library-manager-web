package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Organize with the folders kept takes every library folder as one: a game in an extra folder
// is no game with a folder of its own there, and its update does not leave the library folder
// for a folder below the extra one.
func TestOrganizeTakesEveryLibraryFolder(t *testing.T) {
	web := newTestWeb(t)
	library, extra := t.TempDir(), t.TempDir()
	file := func(name string) db.ExtendedFileInfo {
		if err := os.WriteFile(filepath.Join(extra, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		return db.ExtendedFileInfo{FileName: name, BaseFolder: extra, Size: int64(len(name))}
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Folder, s.ScanFolders = library, []string{extra}
		s.OrganizeOptions.CreateFolderPerGame, s.OrganizeOptions.KeepGameFolders = true, true
		s.OrganizeOptions.UpdatesFolder = "Updates"
	})
	localDB := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": {BaseExist: true,
			File:    db.SwitchFileInfo{ExtendedInfo: file("Game [0100000000010000][v0].nsp"), Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"}},
			Updates: map[int]db.SwitchFileInfo{65536: {ExtendedInfo: file("Game [0100000000010800][v65536].nsp"), Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010800", Version: 65536}}},
			Dlc:     map[string]db.SwitchFileInfo{}, LatestUpdate: 65536},
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, localDB)

	operations, err := web.organize(ORGANIZE_ACTION_ORGANIZE, true)
	if err != nil {
		t.Fatal(err)
	}
	moves := 0
	for _, op := range operations {
		if op.Kind == process.OP_MOVE {
			moves++
			if !strings.HasPrefix(op.To, library) {
				t.Fatalf("into the library folder: %+v", op)
			}
		}
	}
	if moves != 2 {
		t.Fatalf("the game and its update: %+v", operations)
	}
}
