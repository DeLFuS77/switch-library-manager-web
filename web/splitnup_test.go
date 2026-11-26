package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A NUP split in parts is checked by File check like a NUP whole, by its first part and with the
// size of all its parts; the parts of a split NSP are not.
func TestSplitNupIsAFileToCheck(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	nupFolder := filepath.Join(dir, "Made-up Game [0400AAAA00010000][v0].nup")
	nspFolder := filepath.Join(dir, "Other Game [0100000000020000][v0].nsp")
	for folder, parts := range map[string][]int{nupFolder: {100, 100, 40}, nspFolder: {100, 30}} {
		os.MkdirAll(folder, 0o755)
		for i, size := range parts {
			os.WriteFile(filepath.Join(folder, "0"+string(rune('0'+i))), make([]byte, size), 0o644)
		}
	}
	split := func(folder string, id string) *db.SwitchGameFiles {
		return &db.SwitchGameFiles{BaseExist: true, IsSplit: true,
			File:    db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "00", BaseFolder: folder, Size: 100}, Metadata: &switchfs.ContentMetaAttributes{TitleId: id}},
			Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0400aaaa00010": split(nupFolder, "0400aaaa00010000"),
		"0100000000020": split(nspFolder, "0100000000020000"),
	}})
	files := web.libraryFiles(verifyExtensions...)
	if len(files) != 1 || files[0].Path != filepath.Join(nupFolder, "00") || files[0].Size != 240 {
		t.Fatalf("the files to check: %+v", files)
	}
}
