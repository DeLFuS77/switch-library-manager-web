package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
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
	wanted.Status = userdata.STATUS_WANTED
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
	if web.wishes().Count() != 0 {
		t.Fatal("owned games leave the wishlist")
	}
}

func TestApiWishlist(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.TitlesMap["0100000000020"].Attributes.ReleaseDate = 20200314
	switchDB.TitlesMap["0100000000020"].Attributes.Region = "EU"
	switchDB.TitlesMap["0100000000050"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000050000", Name: "Coming", ReleaseDate: 209912}}
	web.state.set(switchDB, localDB)
	web.HandleWishlist()

	get := func() []ApiWishItem {
		t.Helper()
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/wishlist", nil))
		if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%d %s", recorder.Code, recorder.Header().Get("Content-Type"))
		}
		items := []ApiWishItem{}
		if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	if items := get(); len(items) != 0 {
		t.Fatalf("an empty wishlist is an empty list: %+v", items)
	}

	web.wishes().Set("0100000000020000", true)
	time.Sleep(2 * time.Millisecond)
	web.wishes().Set("0100000000050000", true)
	time.Sleep(2 * time.Millisecond)
	// a game the titles database does not know keeps its ID
	web.wishes().Set("01000000000F0000", true)

	items := get()
	if len(items) != 3 || items[0].Id != "0100000000020000" || items[1].Id != "0100000000050000" || items[2].Id != "01000000000F0000" {
		t.Fatalf("the first wished first: %+v", items)
	}
	if game := items[0]; game.Name != "Not Owned" || game.Region != "EU" || game.ReleaseDate != "2020-03-14" || !game.Released || game.AddedAt.IsZero() {
		t.Fatalf("released game: %+v", game)
	}
	if coming := items[1]; coming.ReleaseDate != "2099-12" || coming.Released {
		t.Fatalf("a game not out yet: %+v", coming)
	}
	if unknown := items[2]; unknown.Name != "" || unknown.Released {
		t.Fatalf("unknown game: %+v", unknown)
	}

	// read only
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/wishlist", nil))
	if recorder.Code == http.StatusOK {
		t.Fatal("the wishlist is changed on /wishlist, not on the API")
	}
}

func TestReleaseDateText(t *testing.T) {
	for value, want := range map[int]string{0: "", 2026: "2026", 202603: "2026-03", 20260314: "2026-03-14"} {
		if got := releaseDateText(value); got != want {
			t.Errorf("%d: %q, want %q", value, got, want)
		}
	}
}
