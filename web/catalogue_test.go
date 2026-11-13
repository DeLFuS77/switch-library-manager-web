package web

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// catalogueLibrary is a library of count made-up games, "Game 001" to "Game <count>", every
// tenth of the region JP, and two games of the titles database it does not have.
func catalogueLibrary(t *testing.T, count int) (*db.SwitchTitlesDB, *db.LocalSwitchFilesDB, string) {
	t.Helper()
	romDir := t.TempDir()
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}
	localDB := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}
	for i := 1; i <= count; i++ {
		id := fmt.Sprintf("0100000000%03X000", i)
		region := "US"
		if i%10 == 0 {
			region = "JP"
		}
		switchDB.TitlesMap[id[:13]] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: fmt.Sprintf("Game %03d", i), Region: region}}
		localDB.TitlesMap[id[:13]] = &db.SwitchGameFiles{
			BaseExist: true,
			File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: fmt.Sprintf("Game %03d [%s][v0].nsp", i, id), BaseFolder: romDir, Size: int64(i) << 20},
				Metadata: &switchfs.ContentMetaAttributes{TitleId: id, Ncap: &switchfs.Nacp{DisplayVersion: "1.0.0"}}},
			Updates: map[int]db.SwitchFileInfo{},
			Dlc:     map[string]db.SwitchFileInfo{},
		}
	}
	switchDB.TitlesMap["0100000000F01"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000F01000", Name: "Wanted One", Region: "US"}}
	switchDB.TitlesMap["0100000000F02"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000F02000", Name: "Wanted Two", Region: "JP"}}
	return switchDB, localDB, romDir
}

// catalogueGames reads the games a catalogue page gives its script.
func catalogueGames(t *testing.T, body string) []catalogueGame {
	t.Helper()
	match := regexp.MustCompile(`(?s)<script type="application/json" id="catalogueGames">(.*?)</script>`).FindStringSubmatch(body)
	if match == nil {
		t.Fatal("the page has no games for its script")
	}
	games := []catalogueGame{}
	if err := json.Unmarshal([]byte(match[1]), &games); err != nil {
		t.Fatalf("the games are not JSON: %v", err)
	}
	return games
}

// The catalogue has every game of the filter of the Library, in its order, without its pages,
// and no path of a file.
func TestCatalogueHasTheGamesOfTheFilter(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	switchDB, localDB, romDir := catalogueLibrary(t, 130)
	web.state.set(switchDB, localDB)
	web.HandleCatalogue()
	get := func(query string) (int, string) {
		page := httptest.NewRecorder()
		web.router.ServeHTTP(page, httptest.NewRequest("GET", "/catalogue.html?"+query, nil))
		return page.Code, page.Body.String()
	}

	// My games, a page of 12 of the list: the catalogue has all 130, in the order chosen
	code, body := get("missing=0&per_page=12&page=3&sort_by=name&sort_order=desc")
	if code != 200 {
		t.Fatalf("the catalogue answers %d", code)
	}
	games := catalogueGames(t, body)
	if len(games) != 130 || games[0].Name != "Game 130" || games[129].Name != "Game 001" {
		t.Fatalf("every game of My games, in the order of the list: %d, %+v", len(games), games[:1])
	}
	if games[0].Version != "1.0.0" || games[0].Size == "" || games[0].Region != "JP" || games[0].Missing {
		t.Fatalf("a game has its version, size and region: %+v", games[0])
	}
	if !strings.Contains(body, "My library") || !strings.Contains(body, "My games") || !strings.Contains(body, "130 games") {
		t.Fatal("the sheet tells the view and the number of games")
	}
	if strings.Contains(body, romDir) || strings.Contains(body, ".nsp") || strings.Contains(body, strings.ReplaceAll(romDir, `\`, `\\`)) {
		t.Fatal("the sheet gives no path of a file")
	}
	// back to the same list, the console both
	if !strings.Contains(body, html.EscapeString("/index.html?console=all&missing=0&per_page=12&sort_by=name&sort_order=desc")) {
		t.Fatal("the page goes back to the list it is of")
	}

	// the same filter as the Library: the search and the region, then All with the missing ones
	if games := catalogueGames(t, func() string { _, body := get("missing=0&q=0100000000042"); return body }()); len(games) != 1 || games[0].Name != "Game 066" {
		t.Fatalf("the search of the Library: %+v", games)
	}
	_, body = get("missing=0&region=jp")
	if games := catalogueGames(t, body); len(games) != 13 || !strings.Contains(body, `<span class="catalogue-part">JP</span>`) {
		t.Fatalf("the region of the Library: %d games", len(games))
	}
	_, body = get("region=jp")
	games = catalogueGames(t, body)
	missing := 0
	for _, game := range games {
		if game.Missing {
			missing++
		}
	}
	if len(games) != 14 || missing != 1 || !strings.Contains(body, `<span class="catalogue-part">All</span>`) {
		t.Fatalf("All has the games of the library and those it does not have: %d games, %d missing", len(games), missing)
	}

	// what the filter of the Library has is what the catalogue has
	filter := defaultFilter()
	filter.Region, filter.SortBy, filter.PerPage = "US", "size", 96
	filter.Normalize()
	want, p, _ := web.getLibraryWithFacets(filter, "en")
	got := web.catalogue(filter, "en").Games
	if p.NumItems != 117 || len(got) != p.NumItems || len(want) != 96 || got[0].Id != want[0].Id || got[95].Id != want[95].Id {
		t.Fatalf("the catalogue is the list of the Library without its pages: %d of %d", len(got), p.NumItems)
	}
}

// The catalogue of the Missing games is of their filter: the region, the wishlist.
func TestCatalogueOfTheMissingGames(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	switchDB, localDB, _ := catalogueLibrary(t, 3)
	web.state.set(switchDB, localDB)
	web.HandleCatalogue()

	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/catalogue.html?list=missing&region=jp", nil))
	body := page.Body.String()
	games := catalogueGames(t, body)
	if page.Code != 200 || len(games) != 1 || games[0].Name != "Wanted Two" || games[0].Missing {
		t.Fatalf("the missing games of the region: %d %+v", page.Code, games)
	}
	if !strings.Contains(body, "Missing Games") || !strings.Contains(body, html.EscapeString("/missing.html?console=all&per_page=24&region=jp")) {
		t.Fatal("the sheet is of the Missing games, and goes back to them")
	}

	page = httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/catalogue.html?list=missing&status=wanted", nil))
	if body := page.Body.String(); len(catalogueGames(t, body)) != 0 || !strings.Contains(body, "Wishlist") || !strings.Contains(body, "No game matches these filters.") {
		t.Fatal("an empty wishlist is an empty sheet")
	}
}

// The button of the toolbar of the Library and of the Missing games opens the catalogue with
// their filter.
func TestCatalogueButtonKeepsTheFilter(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	switchDB, localDB, _ := catalogueLibrary(t, 3)
	web.state.set(switchDB, localDB)
	web.HandleIndex()
	web.HandleMissing()
	link := regexp.MustCompile(`class="btn btn-outline-secondary rounded-pill catalogue-link" href="([^"]+)"`)
	href := func(path string) string {
		page := httptest.NewRecorder()
		web.router.ServeHTTP(page, httptest.NewRequest("GET", path, nil))
		match := link.FindStringSubmatch(page.Body.String())
		if page.Code != 200 || match == nil {
			t.Fatalf("%s has no button of the catalogue (%d)", path, page.Code)
		}
		return html.UnescapeString(match[1])
	}

	got := href("/index.html?missing=0&q=game&sort_by=size&sort_order=desc&region=us&page=2&per_page=12")
	if !strings.HasPrefix(got, "/catalogue.html?") || strings.Contains(got, "page=") || strings.Contains(got, "list=") {
		t.Fatalf("the catalogue of the Library, without its pages: %s", got)
	}
	for _, want := range []string{"missing=0", "q=game", "sort_by=size", "sort_order=desc", "region=us"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s misses %s", got, want)
		}
	}
	if got := href("/missing.html?status=wanted&region=jp"); !strings.Contains(got, "list=missing") || !strings.Contains(got, "status=wanted") || !strings.Contains(got, "region=jp") {
		t.Fatalf("the catalogue of the Missing games: %s", got)
	}
}
