package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// countsTestWeb is a library with a game of the Switch 2, a demo, a game of two regions it does
// not have and a copy from another region of a game it has; the demos are hidden and each game
// counts once.
func countsTestWeb(t *testing.T) *Web {
	t.Helper()
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	withSwitch2Game(t, localDB)
	switchDB.TitlesMap["0100000000060"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000060000", Name: "Known Game", Region: "KR"}}
	switchDB.TitlesMap["0100000000080"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000080000", Name: "Far Game", Region: "US"}}
	switchDB.TitlesMap["0100000000090"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000090000", Name: "Far Game", Region: "EU"}}
	switchDB.TitlesMap["0100000000070"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000070000", Name: "Tiny Trip", IsDemo: true}}
	localDB.TitlesMap["0100000000070"] = &db.SwitchGameFiles{BaseExist: true,
		File:    db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Tiny Trip [0100000000070000][v0].nsp", BaseFolder: t.TempDir(), Size: 4}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000070000"}},
		Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	web.state.set(switchDB, localDB)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.HideDemoGames = true
		s.CountRegionsOnce = true
	})
	return web
}

// Each number of the top of the Library is the number of games its click shows: the views, the
// consoles beside them and the cards of the status, whatever else is chosen.
func TestEachNumberIsWhatItsClickShows(t *testing.T) {
	web := countsTestWeb(t)
	everything := &TitleItemFilter{}
	everything.Normalize()
	all := func(change func(*TitleItemFilter)) (*TitleItemFilter, int, LibraryFacets) {
		filter := *everything
		change(&filter)
		_, p, facets := web.getLibraryWithFacets(&filter, "en")
		return &filter, p.NumItems, facets
	}

	// the views: All is My games and Missing, as their lists show them
	views := web.libraryViews("all", "en", "")
	_, shown, facets := all(func(*TitleItemFilter) {})
	if views.All() != shown || facets.All != shown {
		t.Fatalf("All counts %d, its list shows %d (%d)", views.All(), shown, facets.All)
	}
	if _, mine, _ := web.getLibraryWithFacets(defaultFilter(), "en"); views.Mine != mine.NumItems {
		t.Fatalf("My games counts %d, its list shows %d", views.Mine, mine.NumItems)
	}
	missing, p, _ := web.getMissingGamesWithFacets(defaultFilter(), "en")
	if views.Missing != p.NumItems || web.missingGamesCount() != p.NumItems {
		t.Fatalf("Missing counts %d and %d, its list shows %d", views.Missing, web.missingGamesCount(), p.NumItems)
	}
	// a game missing in another region of one the library has is not missing; one of two
	// regions it does not have is one card, with the flag of the other region
	far := 0
	for _, item := range missing {
		if item.OriginalName == "Known Game" {
			t.Fatalf("a region of a game the library has is not missing: %+v", item)
		}
		if item.OriginalName == "Far Game" {
			far++
			if len(item.MissingFlags) != 1 {
				t.Fatalf("one card of both regions: %+v", item)
			}
		}
	}
	if far != 1 {
		t.Fatalf("a game of two regions is one card: %d", far)
	}

	// the consoles: the hidden demo is in neither, and each counts what its choice shows
	if facets.Switch1+facets.Switch2 != shown || facets.Switch2 != 1 {
		t.Fatalf("the consoles count %d and %d of %d games", facets.Switch1, facets.Switch2, shown)
	}
	for _, status := range []string{"", STATUS_UPDATE, STATUS_COMPLETE} {
		_, both, facets := all(func(f *TitleItemFilter) { f.Status = status })
		// Any status counts the games it shows, whatever status is chosen
		if facets.All != shown {
			t.Errorf("status %q: Any status counts %d, it shows %d", status, facets.All, shown)
		}
		_, switch1, chosen := all(func(f *TitleItemFilter) { f.Status, f.Console = status, CONSOLE_SWITCH })
		_, switch2, _ := all(func(f *TitleItemFilter) { f.Status, f.Console = status, CONSOLE_SWITCH2 })
		if facets.Switch1 != switch1 || facets.Switch2 != switch2 || chosen.Switch1 != switch1 || chosen.Switch2 != switch2 || switch1+switch2 != both {
			t.Errorf("status %q: the consoles count %d and %d (%d and %d chosen), their choices show %d and %d", status, facets.Switch1, facets.Switch2, chosen.Switch1, chosen.Switch2, switch1, switch2)
		}
	}
	// the Missing games: each console counts its games with the kind chosen
	for _, kind := range []string{"", KIND_GAME} {
		filter := defaultFilter()
		filter.Kind = kind
		_, p, facets := web.getMissingGamesWithFacets(filter, "en")
		if facets.Switch1+facets.Switch2 != p.NumItems {
			t.Errorf("kind %q: the consoles of the Missing games count %d and %d of %d", kind, facets.Switch1, facets.Switch2, p.NumItems)
		}
	}

	// the views count the games of the console chosen, as they keep it
	if views := web.libraryViews("mine", "en", CONSOLE_SWITCH2); views.Mine != 1 || views.Missing != 0 {
		t.Fatalf("the views of the Switch 2: %+v", views)
	}

	// the cards of the status: those of the library, their size and the share up to date
	_, _, facets = all(func(*TitleItemFilter) {})
	// the game of the Switch 2, whose updates no list knows, is not in the share up to date
	if facets.Owned+facets.Missing != facets.All || facets.Owned != views.Mine || facets.WithFile != facets.Owned-1 || facets.Size == 0 {
		t.Fatalf("the cards of the status: %+v", facets)
	}
	if facets.DlcMissing != 1 || facets.UpToDatePercent() != percent(int64(facets.Complete), int64(facets.WithFile)) {
		t.Fatalf("the DLC missing and the share up to date: %+v", facets)
	}
}

// The panel of filters puts what is yours first, offers only the choices that have games, waits
// for a game marked to offer My game, shows the first choices of a long list and the others a
// click away, and tells how many games the filters leave.
func TestFilterPanelInTheOrderOfUse(t *testing.T) {
	set, err := parseTemplates(os.DirFS(".."), "resources/partials/filter.html", "resources/partials/pagination.html")
	if err != nil {
		t.Fatal(err)
	}
	render := func(filter *TitleItemFilter, facets LibraryFacets) string {
		var out strings.Builder
		data := map[string]any{"Filter": filter, "Facets": facets, "Library": true, "Count": 1234}
		if err := set["es"].ExecuteTemplate(&out, "filterPanel", data); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	genres := []NamedCount{}
	for _, name := range []string{"Action", "Adventure", "Arcade", "Puzzle", "Simulation", "RPG", "Strategy", "Other", "Platformer", "Shooter"} {
		genres = append(genres, NamedCount{Name: name, Count: 3})
	}
	facets := LibraryFacets{Favorites: 2, Recent: 1, Games: 5, Genres: genres, Play: map[string]int{}}
	out := render(defaultFilter(), facets)
	yours, game, file := strings.Index(out, ">Lo tuyo<"), strings.Index(out, ">El juego<"), strings.Index(out, ">El archivo<")
	if yours < 0 || file < yours || game < file {
		t.Fatalf("the parts in their order: %d %d %d", yours, file, game)
	}
	for _, want := range []string{">Mostrar solo<", "Favoritos", "Añadidos en los últimos 30 días", "«Mi partida» saldrá aquí", "Ver 2 más", "1234 juegos"} {
		if !strings.Contains(out, want) {
			t.Errorf("the panel misses %q", want)
		}
	}
	for _, unwanted := range []string{"Sin carátula", "No reconocidos", "Con trucos para mi versión", "Borrar filtros", "<details class=\"filter-more\" open>"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the panel has %q", unwanted)
		}
	}
	// a genre of the list folded, chosen: the list is open, and the filters can be taken off
	chosen := defaultFilter()
	chosen.Genre = "Shooter"
	if out := render(chosen, facets); !strings.Contains(out, `<details class="filter-more" open>`) || !strings.Contains(out, "Borrar filtros") {
		t.Error("the choice made in the list folded shows")
	}
	facets.PlayUsed = true
	if out := render(defaultFilter(), facets); !strings.Contains(out, ">Mi partida<") || strings.Contains(out, "«Mi partida» saldrá aquí") {
		t.Error("My game with a game marked")
	}
	// the choices with a help tell it on their name
	facets.CheatsOther = 3
	if out := render(defaultFilter(), facets); !strings.Contains(out, `<span class="help-name">Con trucos para otra versión</span><span class="form-text">`) {
		t.Error("the help of the cheats for another version")
	}
}

// The recommendations are folded on one line, and stay open on the next pages once opened.
func TestRecommendationsFolded(t *testing.T) {
	set, err := parseTemplates(os.DirFS(".."), "resources/partials/card.html")
	if err != nil {
		t.Fatal(err)
	}
	render := func(open bool) string {
		var out strings.Builder
		items := []Recommendation{{Item: TitleItem{Id: "0100000000080000", Name: "Far Game", ImageUrl: "/far.jpg"}, Genre: "RPG"}}
		if err := set["es"].ExecuteTemplate(&out, "recommendations", map[string]any{"Items": items, "Open": open}); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := render(false); !strings.Contains(out, `<details class="recommendations rec-fold mb-3" data-rec-fold>`) || !strings.Contains(out, `<img src="/far.jpg"`) || !strings.Contains(out, "Te puede gustar") {
		t.Fatalf("folded: %s", out)
	}
	if out := render(true); !strings.Contains(out, "data-rec-fold open>") {
		t.Fatalf("open: %s", out)
	}

	// the cookie of the recommendations opened reaches the filter of the page
	web := newTestWeb(t)
	seen := false
	web.HandleFiltered("/test.html", func(filter *TitleItemFilter, lang string) any {
		seen = filter.RecsOpen()
		return nil
	}, os.DirFS(".."), "resources/partials/pagination.html")
	request := httptest.NewRequest("GET", "/test.html", nil)
	request.AddCookie(&http.Cookie{Name: recsCookie, Value: "open"})
	web.router.ServeHTTP(httptest.NewRecorder(), request)
	if !seen {
		t.Fatal("the recommendations opened are remembered")
	}
}
