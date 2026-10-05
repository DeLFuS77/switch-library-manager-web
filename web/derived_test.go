package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestDerivedValuesFollowTheLibraryAndTheSettings(t *testing.T) {
	web := newTestWeb(t)
	calls := 0
	compute := func() any {
		calls++
		return calls
	}

	if web.derived("x", compute) != 1 || web.derived("x", compute) != 1 {
		t.Fatal("the value must be computed once")
	}
	web.state.set(testDatabases(t))
	if web.derived("x", compute) != 2 {
		t.Fatal("a new library must compute the value again")
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {})
	if web.derived("x", compute) != 3 {
		t.Fatal("saved settings must compute the value again")
	}
}

func TestIgnoringUpdatesRefreshesTheCachedPages(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	original := settings.ReadSettings(web.dataFolder).IgnoreUpdateTitleIds
	defer settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.IgnoreUpdateTitleIds = original })

	if items, _ := web.getMissingUpdates(defaultFilter(), "en"); len(items) != 1 || web.navCounts().Updates != 1 {
		t.Fatalf("one missing update expected: %v", len(items))
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.IgnoreUpdateTitleIds = append(s.IgnoreUpdateTitleIds, "0100000000010000")
	})
	if items, _ := web.getMissingUpdates(defaultFilter(), "en"); len(items) != 0 || web.navCounts().Updates != 0 {
		t.Fatalf("an ignored update must disappear at once: %v", len(items))
	}
}

func TestCachedListsAreNotShared(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))

	page, _ := web.getMissingGames(defaultFilter(), "en")
	if len(page) == 0 {
		t.Fatal("a missing game is expected")
	}
	page[0].Name = "changed by a caller"
	again, _ := web.getMissingGames(defaultFilter(), "en")
	if again[0].Name == "changed by a caller" {
		t.Fatal("a page must be a copy of the cached list")
	}

	desc := defaultFilter()
	desc.SortOrder = "desc"
	ascLibrary, _ := web.getLibrary(defaultFilter(), "en")
	descLibrary, _ := web.getLibrary(desc, "en")
	if len(ascLibrary) < 2 || ascLibrary[0].Id != descLibrary[len(descLibrary)-1].Id {
		t.Fatal("each sort order has its own sorted list")
	}
}

func TestIssuesPageIsPaginatedAndSearchable(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	_, localDB := testDatabases(t)
	for i := 0; i < 60; i++ {
		localDB.Skipped[db.ExtendedFileInfo{FileName: fmt.Sprintf("junk %02d.txt", i), BaseFolder: "/roms"}] = db.SkippedFile{ReasonText: "file type is not supported"}
	}
	web.state.set(nil, localDB)
	web.HandleIssues()

	get := func(query string) string {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/issues.html"+query, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %v", query, recorder.Code)
		}
		return recorder.Body.String()
	}

	first := get("")
	if strings.Count(first, "<tr>") != 24+1 || !strings.Contains(first, "pagination") {
		t.Fatalf("24 issues per page expected, got %v rows", strings.Count(first, "<tr>")-1)
	}
	search := get("?q=junk+07")
	if strings.Count(search, "<tr>") != 1+1 || !strings.Contains(search, "junk 07.txt") {
		t.Fatalf("search: %v rows", strings.Count(search, "<tr>")-1)
	}
	if none := get("?q=nothing-like-this"); !strings.Contains(none, "No results") {
		t.Fatal("a search without results says so")
	}
}
