package web

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// The Library and the missing games are filtered by publisher (#183), whatever the case of its
// letters; each publisher is counted, before its own choice, with its number of games.
func TestPublisherFilter(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.TitlesMap["0100000000010"].Attributes.Publisher = "Nintendo"
	web.state.set(switchDB, localDB)

	_, _, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	if len(facets.Publishers) != 1 || facets.Publishers[0].Name != "Nintendo" || facets.Publishers[0].Count != 1 {
		t.Fatalf("the publishers counted: %+v", facets.Publishers)
	}
	filter := defaultFilter()
	filter.Publisher = "  nintendo "
	filter.Normalize()
	items, _, facets := web.getLibraryWithFacets(filter, "en")
	if filter.Publisher != "nintendo" || len(items) != 1 || items[0].Name != "Known Game" || len(facets.Publishers) != 1 {
		t.Fatalf("filtered by publisher: %q %+v %+v", filter.Publisher, items, facets.Publishers)
	}
	if filter.SecondaryCount() != 1 || !filter.Filters() || filter.Plain() || filter.query().Get("publisher") != "nintendo" {
		t.Fatalf("the filter counted and kept in the address: %+v", filter.query())
	}
	other := defaultFilter()
	other.Publisher = "Nobody"
	if items, _, _ := web.getLibraryWithFacets(other, "en"); len(items) != 0 {
		t.Fatalf("a publisher with no game: %+v", items)
	}
	if long := cleanPublisher(strings.Repeat("é", 300)); len([]rune(long)) != maxPublisher {
		t.Fatalf("a publisher too long: %d", len([]rune(long)))
	}
	if query := cleanSmartQuery("publisher=Nintendo&per_page=48"); !strings.Contains(query, "publisher=Nintendo") || strings.Contains(query, "per_page") {
		t.Fatalf("a smart collection keeps it: %q", query)
	}
}

func TestPublisherFilterOfTheMissingGamesAndItsPage(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	title := func(id string, name string, publisher string) *db.SwitchTitle {
		return &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: name, Region: "US", Publisher: publisher}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": title("0100000000010000", "Racing Party", "Gridline"),
		"0100000000020": title("0100000000020000", "Quiet Puzzle", "Hollow Oak"),
		"0100000000030": title("0100000000030000", "Puzzle Duo", "Gridline"),
	}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})

	filter := defaultFilter()
	filter.Publisher = "Gridline"
	items, _, facets := web.getMissingGamesWithFacets(filter, "en")
	if len(items) != 2 || len(facets.Publishers) != 2 || facets.Publishers[0].Name != "Gridline" || facets.Publishers[0].Count != 2 {
		t.Fatalf("the missing games of a publisher: %+v %+v", items, facets.Publishers)
	}

	web.HandleMissing()
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/missing.html?publisher=Gridline", nil))
	body := page.Body.String()
	if page.Code != 200 || !strings.Contains(body, "bi-building") || !strings.Contains(body, `publisher=Gridline%7CHollow&#43;Oak&amp;`) ||
		!strings.Contains(body, "active-filter") || strings.Contains(body, "Quiet Puzzle</") {
		t.Fatalf("the page: %d", page.Code)
	}
}

// Several publishers are chosen at once (#214): a game of any of them is kept, each choice adds
// its publisher or takes it away, and each one has its own tag to take it away.
func TestPublisherFilterTakesSeveral(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	title := func(id string, name string, publisher string) *db.SwitchTitle {
		return &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: name, Region: "US", Publisher: publisher}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": title("0100000000010000", "Racing Party", "Gridline"),
		"0100000000020": title("0100000000020000", "Quiet Puzzle", "Hollow Oak"),
		"0100000000030": title("0100000000030000", "Puzzle Duo", "Gridline of America"),
	}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})

	filter := defaultFilter()
	filter.Publisher = " gridline | Gridline of  America|GRIDLINE||"
	filter.Normalize()
	if filter.Publisher != "gridline|Gridline of America" || len(filter.Publishers()) != 2 || filter.SecondaryCount() != 1 {
		t.Fatalf("the publishers cleaned: %q", filter.Publisher)
	}
	items, _, facets := web.getMissingGamesWithFacets(filter, "en")
	if len(items) != 2 || len(facets.Publishers) != 3 {
		t.Fatalf("the games of two publishers: %+v %+v", items, facets.Publishers)
	}
	if !filter.PublisherChosen("Gridline") || filter.PublisherChosen("Hollow Oak") ||
		filter.PublisherToggled("Hollow Oak") != "gridline|Gridline of America|Hollow Oak" || filter.PublisherToggled("GRIDLINE") != "Gridline of America" {
		t.Fatalf("a choice adds or takes away its publisher: %q %q", filter.PublisherToggled("Hollow Oak"), filter.PublisherToggled("GRIDLINE"))
	}
	many := defaultFilter()
	for i := 0; i < maxPublishers+10; i++ {
		many.Publisher += strings.Repeat("a", i+1) + publisherSeparator
	}
	many.Normalize()
	if len(many.Publishers()) != maxPublishers {
		t.Fatalf("too many publishers: %d", len(many.Publishers()))
	}

	web.HandleMissing()
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/missing.html?publisher=Gridline%7CHollow+Oak", nil))
	body := page.Body.String()
	if page.Code != 200 || strings.Count(body, `class="active-filter"`) != 2 || !strings.Contains(body, `href="?per_page=24&amp;publisher=Hollow&#43;Oak&amp;`) ||
		!strings.Contains(body, `publisher=Gridline%7CHollow&#43;Oak%7CGridline&#43;of&#43;America`) || !strings.Contains(body, "Quiet Puzzle") ||
		strings.Contains(body, "Puzzle Duo</") || !strings.Contains(body, "data-publisher-group") {
		t.Fatalf("the page: %d", page.Code)
	}
}
