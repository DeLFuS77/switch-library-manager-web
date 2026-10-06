package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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
	hide := settings.ReadSettings(web.dataFolder).HideDemoGames
	defer settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HideDemoGames = hide })
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HideDemoGames = !s.HideDemoGames })
	if web.derived("x", compute) != 3 {
		t.Fatal("settings that change the lists must compute the value again")
	}
	// the time of the last synchronization changes no list
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.LastSyncTime = time.Now() })
	if web.derived("x", compute) != 3 {
		t.Fatal("settings that change no list keep the values")
	}

	// an invalidation while a value is computed: nothing breaks and the value is not kept
	value := web.derived("y", func() any {
		web.invalidateDerived()
		return "stale"
	})
	if value != "stale" || web.derived("y", func() any { return "fresh" }) != "fresh" {
		t.Fatal("a value computed before an invalidation is not kept")
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
	if strings.Count(first, `class="item-row"`) != 24 || !strings.Contains(first, "pagination") {
		t.Fatalf("24 issues per page expected, got %v rows", strings.Count(first, `class="item-row"`))
	}
	search := get("?q=junk+07")
	if strings.Count(search, `class="item-row"`) != 1 || !strings.Contains(search, "junk 07.txt") {
		t.Fatalf("search: %v rows", strings.Count(search, `class="item-row"`))
	}
	if none := get("?q=nothing-like-this"); !strings.Contains(none, "No results") {
		t.Fatal("a search without results says so")
	}
}

func TestDesignShowsOverviewDlcProgressAndRings(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.state.set(testDatabases(t))
	web.HandleIndex()
	web.HandleTitle()
	web.HandleStatistics()

	get := func(path string) string {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %v", path, recorder.Code)
		}
		return recorder.Body.String()
	}

	library := get("/index.html")
	for _, want := range []string{"library-overview", "cover-ribbon", "DLC 1/2", `aria-valuenow="50"`} {
		if !strings.Contains(library, want) {
			t.Errorf("library page misses %q", want)
		}
	}
	title := get("/title/0100000000010000.html")
	if !strings.Contains(title, "1 of 2 DLC") || !strings.Contains(title, `class="dlc-progress-bar"`) || !strings.Contains(title, "width: 50%") {
		t.Error("the game page must show the DLC progress")
	}
	if !strings.Contains(title, `class="fact-value font-monospace fact-id">0100000000010000<`) || !strings.Contains(title, `class="item-list"`) {
		t.Error("the game page shows the title ID on one line and the files as a list")
	}
	stats := get("/statistics.html")
	if !strings.Contains(stats, "conic-gradient(var(--slm-chart-1)") || !strings.Contains(stats, "--pct:") {
		t.Error("the statistics page must draw the donut and the gauge")
	}
}
