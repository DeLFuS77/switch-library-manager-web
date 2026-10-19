package web

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// Missing ones can be narrowed like the Library: region, genre, players, language, demos.
func TestMissingGamesFilters(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	title := func(id string, name string, region string, genre string, players int, language string) *db.SwitchTitle {
		return &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: name, Region: region, Genres: []string{genre}, Players: players, Languages: []string{language}}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": title("0100000000010000", "Racing Party", "US", "Racing", 4, "en"),
		"0100000000020": title("0100000000020000", "Quiet Puzzle", "JP", "Puzzle", 1, "ja"),
		"0100000000030": title("0100000000030000", "Puzzle Duo", "US", "Puzzle", 2, "en"),
	}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})

	names := func(set func(f *TitleItemFilter)) []string {
		filter := defaultFilter()
		set(filter)
		filter.Normalize()
		items, _, _ := web.getMissingGamesWithFacets(filter, "en")
		result := []string{}
		for _, item := range items {
			result = append(result, item.Name)
		}
		return result
	}
	if got := names(func(f *TitleItemFilter) { f.Genre = "Puzzle" }); len(got) != 2 {
		t.Fatalf("genre: %v", got)
	}
	if got := names(func(f *TitleItemFilter) { f.Genre = "Puzzle"; f.Players = "2" }); len(got) != 1 || got[0] != "Puzzle Duo" {
		t.Fatalf("genre and players: %v", got)
	}
	if got := names(func(f *TitleItemFilter) { f.Region = "jp" }); len(got) != 1 || got[0] != "Quiet Puzzle" {
		t.Fatalf("region: %v", got)
	}
	if got := names(func(f *TitleItemFilter) { f.GameLanguage = "en"; f.Keyword = "racing" }); len(got) != 1 || got[0] != "Racing Party" {
		t.Fatalf("language and search: %v", got)
	}
	filter := defaultFilter()
	filter.Genre = "Puzzle"
	_, _, facets := web.getMissingGamesWithFacets(filter, "en")
	if len(facets.Genres) != 2 || len(facets.Regions) != 2 || facets.All != 2 {
		t.Fatalf("each filter counted before its own choice: %+v", facets)
	}

	web.HandleMissing()
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/missing.html?genre=Puzzle", nil))
	body := page.Body.String()
	if page.Code != 200 || !strings.Contains(body, "filter-panel") || !strings.Contains(body, "active-filter") || strings.Contains(body, "Racing Party") {
		t.Fatalf("the page shows the panel and the active filter: %d", page.Code)
	}
}
