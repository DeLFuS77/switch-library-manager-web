package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

func newTestWeb(t *testing.T) *Web {
	t.Helper()
	return &Web{router: mux.NewRouter(), dataFolder: t.TempDir(), sugarLogger: zap.NewNop().Sugar()}
}

func defaultFilter() *TitleItemFilter {
	f := &TitleItemFilter{}
	f.Normalize()
	return f
}

func testDatabases(t *testing.T) (*db.SwitchTitlesDB, *db.LocalSwitchFilesDB) {
	t.Helper()
	romDir := t.TempDir()
	file := func(name string) db.ExtendedFileInfo {
		if err := os.WriteFile(filepath.Join(romDir, name), []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
		return db.ExtendedFileInfo{FileName: name, BaseFolder: romDir, Size: 4}
	}

	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": {
			Attributes: db.TitleAttributes{Id: "0100000000010000", Name: "Known Game", Region: "US", ReleaseDate: 20171027},
			Updates:    map[int]string{65536: "2018-01-01", 131072: "2019-01-01"},
			Dlc: map[string]db.TitleAttributes{
				"0100000000011001": {Id: "0100000000011001", Name: "Owned DLC"},
				"0100000000011002": {Id: "0100000000011002", Name: "Missing DLC"},
			},
		},
		"0100000000020": {Attributes: db.TitleAttributes{Id: "0100000000020000", Name: "Not Owned"}},
	}}

	localDB := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {
				BaseExist: true,
				File:      db.SwitchFileInfo{ExtendedInfo: file("Known [0100000000010000][v0].nsp"), Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"}},
				Updates: map[int]db.SwitchFileInfo{
					65536: {ExtendedInfo: file("Known [0100000000010800][v65536].nsp"), Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010800", Version: 65536}},
				},
				Dlc: map[string]db.SwitchFileInfo{
					"0100000000011001": {ExtendedInfo: file("Known DLC [0100000000011001][v0].nsp"), Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000011001"}},
				},
				LatestUpdate: 65536,
			},
			// a game unknown to the titles database, split into parts without extension
			"0100000000030": {
				BaseExist: true,
				IsSplit:   true,
				File:      db.SwitchFileInfo{ExtendedInfo: file("00"), Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000030000"}},
				Updates:   map[int]db.SwitchFileInfo{},
				Dlc:       map[string]db.SwitchFileInfo{},
			},
			// only an update, the base game is missing
			"0100000000040": {
				Updates: map[int]db.SwitchFileInfo{
					65536: {ExtendedInfo: file("Orphan [0100000000040800][v65536].nsp"), Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000040800"}},
				},
				Dlc: map[string]db.SwitchFileInfo{},
			},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			{FileName: "readme.txt", BaseFolder: romDir}: {ReasonText: "file type is not supported"},
		},
	}
	return switchDB, localDB
}

func TestNormalizeFilter(t *testing.T) {
	f := &TitleItemFilter{Keyword: "  zelda ", Page: -3, PerPage: 1000000, SortBy: "drop table", SortOrder: "sideways"}
	f.Normalize()
	if f.Keyword != "zelda" || f.Page != 1 || f.PerPage != 24 || f.SortBy != "name" || f.SortOrder != "asc" {
		t.Fatalf("unexpected normalized filter: %+v", f)
	}

	valid := &TitleItemFilter{Page: 3, PerPage: 96, SortBy: "release_date", SortOrder: "desc"}
	valid.Normalize()
	if valid.Page != 3 || valid.PerPage != 96 || valid.SortBy != "release_date" || valid.SortOrder != "desc" {
		t.Fatalf("valid values were changed: %+v", valid)
	}
}

func TestPageUrlKeepsSearchAndEscapes(t *testing.T) {
	f := defaultFilter()
	f.Keyword = `mario & "luigi"`
	got := string(funcMap["pageUrl"].(func(*TitleItemFilter, int) template.URL)(f, 2))
	for _, want := range []string{"q=mario+%26+%22luigi%22", "page=2", "per_page=24", "sort_by=name", "sort_order=asc"} {
		if !strings.Contains(got, want) {
			t.Errorf("pageUrl = %q, missing %q", got, want)
		}
	}
}

func TestPagesWithoutDatabases(t *testing.T) {
	web := newTestWeb(t)
	filter := defaultFilter()

	if items, p := web.getLibrary(filter); len(items) != 0 || p.NumItems != 0 {
		t.Fatal("library should be empty")
	}
	if items, _ := web.getMissingGames(filter); len(items) != 0 {
		t.Fatal("missing games should be empty")
	}
	if items, _ := web.getMissingUpdates(filter); len(items) != 0 {
		t.Fatal("updates should be empty")
	}
	if items, _ := web.getMissingDLC(filter); len(items) != 0 {
		t.Fatal("DLC should be empty")
	}
	if issues := web.getIssues(); len(issues) != 0 {
		t.Fatal("issues should be empty")
	}
}

// While synchronizing for the first time, or if the titles download failed, only the local
// library is available. This used to panic.
func TestPagesWithoutTitlesDatabase(t *testing.T) {
	web := newTestWeb(t)
	_, localDB := testDatabases(t)
	web.state.set(nil, localDB)
	filter := defaultFilter()

	items, _ := web.getLibrary(filter)
	if len(items) != 2 {
		t.Fatalf("expected 2 library items, got %+v", items)
	}
	// sorted by name: "00" (split file) before "Known"
	if items[0].Id != "0100000000030000" || items[1].Name != "Known" || items[1].Type != "NSP" {
		t.Fatalf("names should fall back to the file name, got %+v", items)
	}
	if items, _ := web.getMissingGames(filter); len(items) != 0 {
		t.Fatal("missing games need the titles database")
	}
	if items, _ := web.getMissingUpdates(filter); len(items) != 0 {
		t.Fatal("updates need the titles database")
	}
	if items, _ := web.getMissingDLC(filter); len(items) != 0 {
		t.Fatal("DLC needs the titles database")
	}
}

func TestPagesWithDatabases(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	filter := defaultFilter()

	library, _ := web.getLibrary(filter)
	if len(library) != 2 || library[0].Type != "SPLIT" || library[1].Name != "Known Game" {
		t.Fatalf("unexpected library: %+v", library)
	}

	filter.SortOrder = "desc"
	if library, _ := web.getLibrary(filter); library[0].Name != "Known Game" {
		t.Fatalf("descending order not applied: %+v", library)
	}
	filter.SortOrder = "asc"

	filter.Keyword = "known"
	if library, _ := web.getLibrary(filter); len(library) != 1 || library[0].Region != "US" || library[0].ReleaseDate.IsZero() {
		t.Fatalf("search did not find the game with its details: %+v", library)
	}
	filter.Keyword = ""

	missing, _ := web.getMissingGames(filter)
	names := []string{}
	for _, item := range missing {
		names = append(names, item.Name)
	}
	if strings.Join(names, ",") != "Not Owned" {
		t.Fatalf("unexpected missing games: %v", names)
	}

	updates, _ := web.getMissingUpdates(filter)
	if len(updates) != 1 || updates[0].LocalUpdate != 65536 || updates[0].LatestUpdate != 131072 {
		t.Fatalf("unexpected updates: %+v", updates)
	}

	dlc, _ := web.getMissingDLC(filter)
	if len(dlc) != 1 || len(dlc[0].MissingDLC) != 1 || !strings.Contains(dlc[0].MissingDLC[0], "Missing DLC") {
		t.Fatalf("unexpected missing DLC: %+v", dlc)
	}

	issues := web.getIssues()
	if len(issues) != 2 {
		t.Fatalf("expected the orphan update and readme.txt as issues, got %+v", issues)
	}
}

func TestPaginationBeyondLastPage(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	filter := defaultFilter()
	filter.PerPage = 12
	filter.Page = 50

	items, p := web.getLibrary(filter)
	if len(items) != 2 || p.CurrentPage != 1 {
		t.Fatalf("page beyond the end should show the last page: %d items, %+v", len(items), p)
	}
}

func TestApi(t *testing.T) {
	web := newTestWeb(t)
	web.HandleApi()

	get := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder
	}

	if r := get("/api/titles"); r.Code != http.StatusOK || strings.TrimSpace(r.Body.String()) != "{}" {
		t.Fatalf("empty state: %d %s", r.Code, r.Body.String())
	}

	// local library only: used to panic on the titles database lookup
	_, localDB := testDatabases(t)
	web.state.set(nil, localDB)
	r := get("/api/titles")
	var titles map[string]ApiTitleItem
	if err := json.Unmarshal(r.Body.Bytes(), &titles); err != nil || len(titles) != 2 {
		t.Fatalf("titles without titles database: %v %s", err, r.Body.String())
	}
	if titles["0100000000030000"].Type != "" {
		t.Fatalf("file without extension should have no type: %+v", titles["0100000000030000"])
	}

	web.state.set(testDatabases(t))
	r = get("/api/titles")
	titles = nil
	if err := json.Unmarshal(r.Body.Bytes(), &titles); err != nil {
		t.Fatal(err)
	}
	game := titles["0100000000010000"]
	if game.Region != "US" || game.Dlc["0100000000011001"].Name != "Owned DLC" || game.LatestUpdate.Version != 65536 {
		t.Fatalf("unexpected title: %+v", game)
	}

	downloads := map[string]int{
		"/api/titles/0100000000010000":                      http.StatusOK,
		"/api/titles/0100000000010000/updates/65536":        http.StatusOK,
		"/api/titles/0100000000010000/dlc/0100000000011001": http.StatusOK,
		"/api/titles/0100000000010000/dlc/0100000000011002": http.StatusNotFound,
		"/api/titles/0100000000010000/updates/1":            http.StatusNotFound,
		"/api/titles/0100000000010000/updates/x":            http.StatusNotFound,
		"/api/titles/ffffffffffffffff":                      http.StatusNotFound,
		"/api/titles/0100000000040000/updates/65536":        http.StatusNotFound,
	}
	for path, want := range downloads {
		if r := get(path); r.Code != want {
			t.Errorf("GET %s = %d, want %d", path, r.Code, want)
		}
	}
}

func TestSyncCanOnlyRunOnce(t *testing.T) {
	var state WebState
	if !state.startSync() {
		t.Fatal("first sync should start")
	}
	if state.startSync() {
		t.Fatal("second sync should be rejected while the first is running")
	}
	if !state.IsSynchronizing() {
		t.Fatal("sync should be reported as running")
	}
	state.endSync()
	if state.IsSynchronizing() || !state.startSync() {
		t.Fatal("sync should be possible again after the first finished")
	}
}

// Run with -race: pages are rendered while a synchronization replaces the databases.
func TestConcurrentReadsDuringSync(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				web.getLibrary(defaultFilter())
				web.getMissingUpdates(defaultFilter())
				web.getIssues()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				web.state.set(nil, localDB)
				web.state.set(switchDB, localDB)
			}
		}()
	}
	wg.Wait()
}
