package web

import (
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// A DLC that comes out later is not missing yet: not on the page of its game, which tells the day
// it comes out, nor in the library or on the DLC page.
func TestDLCNotOutYetIsNotMissing(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	coming := "0100000000011003"
	switchDB.TitlesMap["0100000000010"].Dlc[coming] = db.TitleAttributes{Id: coming, Name: "Coming DLC", ReleaseDate: 29991231}
	web.state.set(switchDB, localDB)

	detail, ok := web.getTitleDetail("0100000000010000", "en")
	if !ok {
		t.Fatal("not found")
	}
	if detail.MissingDlc != 1 || detail.DlcOwned != 1 || detail.DlcTotal != 2 || len(detail.Dlc) != 3 {
		t.Fatalf("missing %d, %d of %d, %d listed", detail.MissingDlc, detail.DlcOwned, detail.DlcTotal, len(detail.Dlc))
	}
	for _, dlc := range detail.Dlc {
		if want := map[string]int{coming: 29991231}[dlc.Id]; dlc.ComesOut != want {
			t.Errorf("%s comes out on %d, want %d", dlc.Id, dlc.ComesOut, want)
		}
	}

	for _, item := range web.buildLibrary("en") {
		if item.Id == "0100000000010000" && (item.MissingDlcCount != 1 || item.DlcTotal != 2) {
			t.Errorf("library: %d missing of %d", item.MissingDlcCount, item.DlcTotal)
		}
	}
	missing := web.buildMissingDLC("en")
	if len(missing) != 1 || len(missing[0].MissingDLCItems) != 1 || missing[0].MissingDLCItems[0].Id != "0100000000011002" || missing[0].DlcTotal != 2 {
		t.Fatalf("DLC page: %+v", missing)
	}
}
