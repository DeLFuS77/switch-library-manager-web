package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestWishlist(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.TitlesMap["0100000000020"].Attributes.ReleaseDate = 20200101
	switchDB.TitlesMap["0100000000020"].Dlc = map[string]db.TitleAttributes{"0100000000021001": {Id: "0100000000021001", Name: "Expansion"}}
	web.state.set(switchDB, localDB)
	web.HandleWishlist()

	if response := postForm(web, "/wishlist", url.Values{"id": {"0100000000020000"}, "wanted": {"true"}}); response.Code != http.StatusOK {
		t.Fatalf("add: %d", response.Code)
	}
	if response := postForm(web, "/wishlist", url.Values{"id": {"../etc"}, "wanted": {"true"}}); response.Code != http.StatusBadRequest {
		t.Fatal("only title IDs can be wished")
	}

	all, _ := web.getMissingGames(defaultFilter(), "en")
	wanted := defaultFilter()
	wanted.Status = STATUS_WANTED
	wished, _ := web.getMissingGames(wanted, "en")
	if len(wished) != 1 || !wished[0].Wished || len(all) < len(wished) {
		t.Fatalf("wishlist filter: %d of %d", len(wished), len(all))
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Notifications = settings.NotificationOptions{NotifyWishlist: true}
	})
	items := web.availableItems("en")
	kinds := map[string]bool{}
	for _, item := range items {
		kinds[item.Kind] = true
	}
	if !kinds["wish"] || !kinds["wishdlc"] {
		t.Fatalf("a released wished game and its DLC are notified: %+v", items)
	}

	// a game that is now in the library leaves the wishlist
	localDB.TitlesMap["0100000000020"] = &db.SwitchGameFiles{BaseExist: true, Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	web.forgetOwnedWishes()
	if web.wishes().count() != 0 {
		t.Fatal("owned games leave the wishlist")
	}
}
