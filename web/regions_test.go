package web

import (
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
