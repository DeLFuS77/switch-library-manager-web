package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

func TestRegionalDuplicates(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.TitlesMap["0100000000010"].Attributes.Languages = []string{"en", "ja"}
	// the Japanese copy of the same game, with Spanish
	switchDB.TitlesMap["0100000000050"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000050000", Name: "Known Game（ノウン）", Region: "JP", Languages: []string{"es", "ja"}}}
	localDB.TitlesMap["0100000000050"] = &db.SwitchGameFiles{
		BaseExist: true,
		File:      db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Known JP.nsp", Size: 100}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000050000"}},
		Updates:   map[int]db.SwitchFileInfo{},
		Dlc:       map[string]db.SwitchFileInfo{},
	}
	web.state.set(switchDB, localDB)

	groups := web.regionalDuplicates("es")
	if len(groups) != 1 || len(groups[0].Copies) != 2 {
		t.Fatalf("one game with two copies: %+v", groups)
	}
	keep := groups[0].Copies[0]
	if !keep.Keep || keep.Id != "0100000000050000" || keep.Reasons[0] != REGION_REASON_LANGUAGE || groups[0].Savable == 0 {
		t.Fatalf("the copy in Spanish is suggested for a Spanish interface: %+v", groups[0])
	}
	if groups := web.regionalDuplicates("en"); groups[0].Copies[0].Id != "0100000000010000" {
		t.Fatalf("the copy with the update and the DLC for an English interface: %+v", groups[0])
	}
}

// regionTestWeb has the Known Game twice: the American copy of the test library, and a
// Japanese copy with its files on disk.
func regionTestWeb(t *testing.T) (*Web, string) {
	t.Helper()
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.TitlesMap["0100000000010"].Attributes.Languages = []string{"en", "ja"}
	switchDB.TitlesMap["0100000000050"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000050000", Name: "Known Game（ノウン）", Region: "JP", Languages: []string{"es", "ja"}}}
	folder := t.TempDir()
	for _, name := range []string{"Known JP.nsp", "Known JP DLC.nsp"} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	localDB.TitlesMap["0100000000050"] = &db.SwitchGameFiles{
		BaseExist: true,
		File:      db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Known JP.nsp", BaseFolder: folder, Size: 4}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000050000"}},
		Updates:   map[int]db.SwitchFileInfo{},
		Dlc:       map[string]db.SwitchFileInfo{"0100000000051001": {ExtendedInfo: db.ExtendedFileInfo{FileName: "Known JP DLC.nsp", BaseFolder: folder, Size: 4}}},
	}
	web.state.set(switchDB, localDB)
	return web, folder
}

func TestRegionalDuplicateDamagedIsNotKept(t *testing.T) {
	web, folder := regionTestWeb(t)
	// the Spanish copy would be kept for a Spanish interface, but it is damaged
	path := filepath.Join(folder, "Known JP.nsp")
	info, _ := os.Stat(path)
	web.verifications().set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false, Reason: "bad"})
	group := web.buildRegionalDuplicates("es")[0]
	if group.Copies[0].Id != "0100000000010000" || group.Copies[0].Reasons[0] != REGION_REASON_SOUND || !group.Copies[1].Damaged {
		t.Fatalf("the sound copy is kept: %+v", group.Copies)
	}
}

func TestRemoveRegionCopy(t *testing.T) {
	web, folder := regionTestWeb(t)
	deleted, freed, err := web.removeRegionCopy("0100000000050000", "en")
	if err != nil || deleted != 2 || freed != 8 {
		t.Fatalf("the copy and its DLC: %d %d %v", deleted, freed, err)
	}
	for _, name := range []string{"Known JP.nsp", "Known JP DLC.nsp"} {
		if _, err := os.Stat(filepath.Join(folder, name)); !os.IsNotExist(err) {
			t.Fatalf("%s is deleted", name)
		}
	}
	// an unknown game, or a game with a single copy, is never deleted
	if _, _, err := web.removeRegionCopy("0100000000099000", "en"); err != errRegionCopy {
		t.Fatalf("not a copy of another region: %v", err)
	}
	web.state.set(testDatabases(t))
	if _, _, err := web.removeRegionCopy("0100000000010000", "en"); err != errRegionCopy {
		t.Fatalf("the only copy: %v", err)
	}
}
