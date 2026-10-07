package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

func newTestWeb(t *testing.T) *Web {
	t.Helper()
	web := &Web{router: mux.NewRouter(), dataFolder: t.TempDir(), sugarLogger: zap.NewNop().Sugar()}
	// the processed titles are kept open, and Windows cannot remove an open file
	t.Cleanup(func() {
		// files written in the background after a scan
		waitForBackgroundWork(web)
		web.afterScanWork.Wait()
		if web.store != nil {
			web.store.Close()
		}
	})
	return web
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

	if items, p := web.getLibrary(filter, "en"); len(items) != 0 || p.NumItems != 0 {
		t.Fatal("library should be empty")
	}
	if items, _ := web.getMissingGames(filter, "en"); len(items) != 0 {
		t.Fatal("missing games should be empty")
	}
	if items, _ := web.getMissingUpdates(filter, "en"); len(items) != 0 {
		t.Fatal("updates should be empty")
	}
	if items, _ := web.getMissingDLC(filter, "en"); len(items) != 0 {
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

	items, _ := web.getLibrary(filter, "en")
	if len(items) != 2 {
		t.Fatalf("expected 2 library items, got %+v", items)
	}
	// sorted by name: "00" (split file) before "Known"
	if items[0].Id != "0100000000030000" || items[1].Name != "Known" || items[1].Type != "NSP" {
		t.Fatalf("names should fall back to the file name, got %+v", items)
	}
	if items, _ := web.getMissingGames(filter, "en"); len(items) != 0 {
		t.Fatal("missing games need the titles database")
	}
	if items, _ := web.getMissingUpdates(filter, "en"); len(items) != 0 {
		t.Fatal("updates need the titles database")
	}
	if items, _ := web.getMissingDLC(filter, "en"); len(items) != 0 {
		t.Fatal("DLC needs the titles database")
	}
}

func TestPagesWithDatabases(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	filter := defaultFilter()

	library, _ := web.getLibrary(filter, "en")
	if len(library) != 2 || library[0].Type != "SPLIT" || library[1].Name != "Known Game" {
		t.Fatalf("unexpected library: %+v", library)
	}

	filter.SortOrder = "desc"
	if library, _ := web.getLibrary(filter, "en"); library[0].Name != "Known Game" {
		t.Fatalf("descending order not applied: %+v", library)
	}
	filter.SortOrder = "asc"

	filter.Keyword = "known"
	if library, _ := web.getLibrary(filter, "en"); len(library) != 1 || library[0].Region != "US" || library[0].ReleaseDate.IsZero() {
		t.Fatalf("search did not find the game with its details: %+v", library)
	}
	filter.Keyword = ""

	missing, _ := web.getMissingGames(filter, "en")
	names := []string{}
	for _, item := range missing {
		names = append(names, item.Name)
	}
	if strings.Join(names, ",") != "Not Owned" {
		t.Fatalf("unexpected missing games: %v", names)
	}

	updates, _ := web.getMissingUpdates(filter, "en")
	if len(updates) != 1 || updates[0].LocalUpdate != 65536 || updates[0].LatestUpdate != 131072 {
		t.Fatalf("unexpected updates: %+v", updates)
	}

	dlc, _ := web.getMissingDLC(filter, "en")
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

	items, p := web.getLibrary(filter, "en")
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
				web.getLibrary(defaultFilter(), "en")
				web.getMissingUpdates(defaultFilter(), "en")
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

func TestSameOriginOnly(t *testing.T) {
	handler := sameOriginOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"get from another site", http.MethodGet, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusNoContent},
		{"post without browser headers", http.MethodPost, nil, http.StatusNoContent},
		{"post same origin", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://example.com"}, http.StatusNoContent},
		{"post from another site", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"post from same site subdomain", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"post with foreign origin", http.MethodPost, map[string]string{"Origin": "http://evil.test"}, http.StatusForbidden},
		{"post with null origin", http.MethodPost, map[string]string{"Origin": "null"}, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, "http://example.com/sync", nil)
			for k, v := range tt.headers {
				request.Header.Set(k, v)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tt.want {
				t.Fatalf("got %d, want %d", recorder.Code, tt.want)
			}
		})
	}
}

func TestOrganizeEndpoints(t *testing.T) {
	web := newTestWeb(t)
	web.router.Use(sameOriginOnly)
	web.handleOrganizeActions()

	post := func(path string, action string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("action="+action))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range headers {
			request.Header.Set(k, v)
		}
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, request)
		return recorder
	}

	if r := post("/organize/preview", "organize", nil); r.Code != http.StatusBadRequest {
		t.Fatalf("preview without library: %d %s", r.Code, r.Body.String())
	}

	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)

	if r := post("/organize/preview", "bogus", nil); r.Code != http.StatusBadRequest {
		t.Fatalf("unknown action: %d", r.Code)
	}
	if r := post("/organize/run", "cleanup", map[string]string{"Sec-Fetch-Site": "cross-site"}); r.Code != http.StatusForbidden {
		t.Fatalf("cross-site run must be rejected: %d", r.Code)
	}

	r := post("/organize/preview", "cleanup", nil)
	var response OrganizeResponse
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil || r.Code != http.StatusOK || !response.DryRun {
		t.Fatalf("cleanup preview: %d %s", r.Code, r.Body.String())
	}

	// a running synchronization blocks file changes
	web.state.startSync()
	if r := post("/organize/run", "cleanup", nil); r.Code != http.StatusConflict {
		t.Fatalf("run during sync: %d", r.Code)
	}
	web.state.endSync()
}

func TestSyncProgress(t *testing.T) {
	web := newTestWeb(t)
	web.HandleSynchronize()

	status := func() SyncProgress {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sync", nil))
		var progress SyncProgress
		if err := json.Unmarshal(recorder.Body.Bytes(), &progress); err != nil {
			t.Fatalf("invalid status %q: %v", recorder.Body.String(), err)
		}
		return progress
	}

	if s := status(); s.Synchronizing {
		t.Fatalf("unexpected status: %+v", s)
	}

	web.state.startSync()
	web.UpdateProgress(3, 10, "Reading a.nsp")
	if s := status(); !s.Synchronizing || s.Current != 3 || s.Total != 10 || s.Message != "Reading a.nsp" {
		t.Fatalf("unexpected status: %+v", s)
	}

	// steps with an unknown total keep the last position but update the message
	web.UpdateProgress(-1, -1, "Found b.nsp")
	if s := status(); s.Current != 3 || s.Total != 10 || s.Message != "Found b.nsp" {
		t.Fatalf("unexpected status: %+v", s)
	}
	web.state.endSync()
}

func TestBasicAuth(t *testing.T) {
	handler := envAuth(t, "admin", "secret").middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name, user, password string
		send                 bool
		want                 int
	}{
		{"no credentials", "", "", false, http.StatusUnauthorized},
		{"wrong password", "admin", "nope", true, http.StatusUnauthorized},
		{"wrong user", "root", "secret", true, http.StatusUnauthorized},
		{"valid", "admin", "secret", true, http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/index.html", nil)
			if tt.send {
				request.SetBasicAuth(tt.user, tt.password)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tt.want {
				t.Fatalf("got %d, want %d", recorder.Code, tt.want)
			}
			if tt.want == http.StatusUnauthorized && recorder.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing WWW-Authenticate header")
			}
		})
	}
}

func TestAuthFromEnv(t *testing.T) {
	t.Setenv("SLM_AUTH_USERNAME", "")
	t.Setenv("SLM_AUTH_PASSWORD", "")
	if _, _, enabled, err := authFromEnv(); enabled || err != nil {
		t.Fatalf("auth should be disabled: %v %v", enabled, err)
	}

	t.Setenv("SLM_AUTH_USERNAME", "admin")
	if _, _, _, err := authFromEnv(); err == nil {
		t.Fatal("a username without password must be rejected")
	}

	t.Setenv("SLM_AUTH_PASSWORD", "secret")
	if user, password, enabled, err := authFromEnv(); !enabled || err != nil || user != "admin" || password != "secret" {
		t.Fatalf("unexpected result: %q %q %v %v", user, password, enabled, err)
	}
}

func TestTitleDetail(t *testing.T) {
	web := newTestWeb(t)

	if _, ok := web.getTitleDetail("0100000000010000", "en"); ok {
		t.Fatal("no databases: title should not be found")
	}

	web.state.set(testDatabases(t))

	for _, id := range []string{"0100000000010000", "0100000000010800", "0100000000011002", "0100000000010000"} {
		detail, ok := web.getTitleDetail(id, "en")
		if !ok {
			t.Fatalf("%s: not found", id)
		}
		if detail.Id != "0100000000010000" || detail.Name != "Known Game" || !detail.Owned || detail.Region != "US" {
			t.Fatalf("%s: unexpected detail %+v", id, detail)
		}
		if detail.Base == nil || detail.Base.DownloadUrl != "/api/titles/0100000000010000" || len(detail.Updates) != 1 {
			t.Fatalf("%s: unexpected files %+v %+v", id, detail.Base, detail.Updates)
		}
		if !detail.UpdateMissing || detail.LocalUpdate != 65536 || detail.LatestUpdate != 131072 {
			t.Fatalf("%s: unexpected update state %+v", id, detail)
		}
		if len(detail.Dlc) != 2 || detail.MissingDlc != 1 || !detail.Dlc[1].Owned || detail.Dlc[1].File == nil || detail.Dlc[0].Owned {
			t.Fatalf("%s: unexpected DLC %+v", id, detail.Dlc)
		}
	}

	missing, ok := web.getTitleDetail("0100000000020000", "en")
	if !ok || missing.Owned || missing.Name != "Not Owned" || missing.Base != nil || missing.MissingDlc != 0 {
		t.Fatalf("game not in the library: %+v", missing)
	}

	unknown, ok := web.getTitleDetail("0100000000030000", "en")
	if !ok || !unknown.Owned || unknown.Name != "00" {
		t.Fatalf("game unknown to the titles database: %+v", unknown)
	}

	orphan, ok := web.getTitleDetail("0100000000040800", "en")
	if !ok || orphan.Owned || orphan.Id != "0100000000040000" {
		t.Fatalf("update without base game: %+v", orphan)
	}

	for _, id := range []string{"ffffffffffff0000", "nope", "0100000000010001"} {
		if _, ok := web.getTitleDetail(id, "en"); ok {
			t.Fatalf("%s should not be found", id)
		}
	}
}

func TestIgnoreEndpoint(t *testing.T) {
	web := newTestWeb(t)
	web.router.Use(sameOriginOnly)
	web.HandleIgnore()
	switchDB, localDB := testDatabases(t)
	// a newer version of the owned DLC exists
	game := switchDB.TitlesMap["0100000000010"]
	ownedDlc := game.Dlc["0100000000011001"]
	ownedDlc.Version = "65536"
	game.Dlc["0100000000011001"] = ownedDlc
	web.state.set(switchDB, localDB)

	post := func(form string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/ignore", strings.NewReader(form))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range headers {
			request.Header.Set(k, v)
		}
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, request)
		return recorder
	}
	missingDlc := func() int {
		detail, _ := web.getTitleDetail("0100000000010000", "en")
		return detail.MissingDlc
	}
	updatesIgnored := func() bool {
		detail, _ := web.getTitleDetail("0100000000010000", "en")
		return detail.UpdatesIgnored
	}

	for _, form := range []string{"kind=dlc&id=nope", "kind=other&id=0100000000011002"} {
		if r := post(form, nil); r.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d", form, r.Code)
		}
	}
	if r := post("kind=dlc&id=0100000000011002", map[string]string{"Sec-Fetch-Site": "cross-site"}); r.Code != http.StatusForbidden {
		t.Fatalf("cross-site ignore must be rejected: %d", r.Code)
	}

	if missingDlc() != 1 {
		t.Fatal("expected one missing DLC before ignoring")
	}
	// ignoring twice must not add duplicates, lower case IDs are accepted
	for i := 0; i < 2; i++ {
		if r := post("kind=dlc&id=0100000000011002&ignored=true", nil); r.Code != http.StatusOK {
			t.Fatalf("ignore DLC: %d %s", r.Code, r.Body.String())
		}
	}
	if missingDlc() != 0 {
		t.Fatal("ignored DLC is still missing")
	}
	if items, _ := web.getMissingDLC(defaultFilter(), "en"); len(items) != 0 {
		t.Fatalf("ignored DLC still listed: %+v", items)
	}

	if detail, _ := web.getTitleDetail("0100000000010000", "en"); !detail.Dlc[1].UpdateAvailable {
		t.Fatal("the DLC update should be offered")
	}

	post("kind=update&id=0100000000010000&ignored=true", nil)
	if !updatesIgnored() {
		t.Fatal("updates should be ignored")
	}
	if detail, _ := web.getTitleDetail("0100000000010000", "en"); detail.Dlc[1].UpdateAvailable {
		t.Fatal("DLC updates of a game with ignored updates should not be offered")
	}
	if items, _ := web.getMissingUpdates(defaultFilter(), "en"); len(items) != 0 {
		t.Fatalf("ignored updates still listed: %+v", items)
	}

	// restore, also leaves the shared settings clean for other tests
	post("kind=dlc&id=0100000000011002&ignored=false", nil)
	post("kind=update&id=0100000000010000&ignored=false", nil)
	if missingDlc() != 1 || updatesIgnored() {
		t.Fatal("restoring did not work")
	}
}

func TestSetIgnoredAndFormatSize(t *testing.T) {
	list := setIgnored([]string{"0100000000011001", " 0100000000011002 "}, "0100000000011002", true)
	if strings.Join(list, ",") != "0100000000011001,0100000000011002" {
		t.Fatalf("unexpected list: %v", list)
	}
	if list := setIgnored(list, "0100000000011001", false); strings.Join(list, ",") != "0100000000011002" {
		t.Fatalf("unexpected list: %v", list)
	}
	for size, want := range map[int64]string{512: "512 B", 2048: "2.0 KB", 213637397: "203.7 MB", 16 << 30: "16.0 GB"} {
		if got := formatSize(size); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", size, got, want)
		}
	}
}

func TestSyncSchedule(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		interval int
		last     time.Time
		due      bool
	}{
		{"disabled", 0, time.Time{}, false},
		{"disabled with old sync", 0, now.Add(-1000 * time.Hour), false},
		{"never synchronized", 24, time.Time{}, true},
		{"not yet", 24, now.Add(-23 * time.Hour), false},
		{"exactly due", 24, now.Add(-24 * time.Hour), true},
		{"overdue", 6, now.Add(-7 * time.Hour), true},
		{"weekly not yet", 168, now.Add(-6 * 24 * time.Hour), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &settings.AppSettings{SyncIntervalHours: tt.interval, LastSyncTime: tt.last}
			if got := syncDue(s, now); got != tt.due {
				t.Fatalf("syncDue = %v, want %v", got, tt.due)
			}
		})
	}

	s := &settings.AppSettings{SyncIntervalHours: 12, LastSyncTime: now}
	if next := nextSyncTime(s); !next.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("unexpected next sync %v", next)
	}

	label := funcMap["intervalLabel"].(func(int) string)
	for hours, want := range map[int]string{0: "Disabled", 6: "Every 6 hours", 24: "Every day", 168: "Every 7 days"} {
		if got := label(hours); got != want {
			t.Errorf("intervalLabel(%d) = %q, want %q", hours, got, want)
		}
	}
}

func TestHealthCheckBypassesAuthentication(t *testing.T) {
	protected := envAuth(t, "admin", "secret").middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	handler := withHealthCheck(protected)

	for path, want := range map[string]int{"/healthz": http.StatusOK, "/index.html": http.StatusUnauthorized, "/healthz/x": http.StatusUnauthorized} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != want {
			t.Errorf("GET %s = %d, want %d", path, recorder.Code, want)
		}
	}
}

func TestExport(t *testing.T) {
	web := newTestWeb(t)
	web.HandleExport()

	get := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder
	}

	if r := get("/export/library.json"); r.Code != http.StatusOK || strings.TrimSpace(r.Body.String()) != "[]" {
		t.Fatalf("empty library: %d %q", r.Code, r.Body.String())
	}

	web.state.set(testDatabases(t))

	r := get("/export/library.json")
	if !strings.Contains(r.Header().Get("Content-Disposition"), ".json") {
		t.Fatalf("missing download name: %v", r.Header())
	}
	var titles []ExportTitle
	if err := json.Unmarshal(r.Body.Bytes(), &titles); err != nil {
		t.Fatal(err)
	}
	// sorted by name: the split file "00" first, the orphan update is not a library game
	if len(titles) != 2 || titles[1].Name != "Known Game" {
		t.Fatalf("unexpected export: %+v", titles)
	}
	game := titles[1]
	if game.Id != "0100000000010000" || len(game.Files) != 2 || game.DlcOwned != 1 || game.DlcMissing != 1 ||
		!game.UpdateMissing || game.LatestUpdate != 131072 || game.Size != 12 || game.ReleaseDate != "2017-10-27" {
		t.Fatalf("unexpected game: %+v", game)
	}

	r = get("/export/library.csv")
	body := r.Body.String()
	if !strings.HasPrefix(body, "\xef\xbb\xbf") || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/csv") {
		t.Fatal("CSV must be UTF-8 with a byte order mark")
	}
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(body, "\xef\xbb\xbf")), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "Title ID,Name") || !strings.HasPrefix(lines[2], "0100000000010000,Known Game,") {
		t.Fatalf("unexpected CSV:\n%s", body)
	}

	if csvSafe("=HYPERLINK(1)") != "'=HYPERLINK(1)" || csvSafe("Zelda") != "Zelda" || csvSafe("") != "" {
		t.Fatal("formula cells must be neutralised")
	}
}

func TestLocalizedTitleNames(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.Localized = map[string]map[string]db.LocalizedTitle{
		"es": {
			"0100000000010000": {Name: "Juego Conocido", Description: "Descripción en español"},
			"0100000000011002": {Name: "DLC que falta"},
			"0100000000020000": {Name: "No lo tengo"},
		},
	}
	web.state.set(switchDB, localDB)

	filter := defaultFilter()
	library, _ := web.getLibrary(filter, "es")
	if library[1].Name != "Juego Conocido" {
		t.Fatalf("library name not translated: %+v", library)
	}
	if english, _ := web.getLibrary(filter, "en"); english[1].Name != "Known Game" {
		t.Fatalf("English name changed: %+v", english)
	}

	// the search finds a game by its translated and by its original name
	for _, keyword := range []string{"conocido", "known"} {
		filter.Keyword = keyword
		if items, _ := web.getLibrary(filter, "es"); len(items) != 1 {
			t.Fatalf("search %q: %+v", keyword, items)
		}
	}
	filter.Keyword = ""

	if missing, _ := web.getMissingGames(filter, "es"); len(missing) != 1 || missing[0].Name != "No lo tengo" {
		t.Fatalf("missing game name not translated: %+v", missing)
	}
	if dlc, _ := web.getMissingDLC(filter, "es"); dlc[0].Name != "Juego Conocido" || dlc[0].MissingDLCItems[0].Name != "DLC que falta" {
		t.Fatalf("DLC names not translated: %+v", dlc)
	}

	detail, _ := web.getTitleDetail("0100000000010000", "es")
	if detail.Name != "Juego Conocido" || detail.Description != "Descripción en español" {
		t.Fatalf("game page not translated: %q %q", detail.Name, detail.Description)
	}
	// no translation: the English name of the owned DLC is kept
	if detail.Dlc[1].Name != "Owned DLC" || detail.Dlc[0].Name != "DLC que falta" {
		t.Fatalf("unexpected DLC names: %+v", detail.Dlc)
	}

	// the export keeps the original names
	if export := web.getExport(); export[1].Name != "Known Game" {
		t.Fatalf("export should not be translated: %+v", export[1])
	}
}

func TestStatistics(t *testing.T) {
	web := newTestWeb(t)
	if stats := web.getStatistics("en"); stats.Games != 0 || stats.TotalSize != 0 {
		t.Fatalf("empty library: %+v", stats)
	}

	web.state.set(testDatabases(t))
	stats := web.getStatistics("en")

	// two games (one split), one update of the known game, one orphan update, one DLC; 4 bytes each
	if stats.Games != 2 || stats.Updates != 2 || stats.Dlc != 1 || stats.TotalSize != 20 {
		t.Fatalf("unexpected counts: %+v", stats)
	}
	if stats.ByContent[0].Size != 8 || stats.ByContent[1].Size != 8 || stats.ByContent[2].Size != 4 || stats.ByContent[0].Percent != 40 {
		t.Fatalf("unexpected space by content: %+v", stats.ByContent)
	}
	if len(stats.ByFormat) != 2 || stats.ByFormat[0].Label != "NSP" || stats.ByFormat[0].Count != 4 || stats.ByFormat[1].Label != "?" {
		t.Fatalf("unexpected space by format: %+v", stats.ByFormat)
	}
	if stats.GamesWithUpdate != 1 || stats.GamesUpToDate != 1 || stats.GamesUpToDatePct != 50 {
		t.Fatalf("unexpected update status: %+v", stats)
	}
	if stats.MissingDlc != 1 || stats.GamesMissingDlc != 1 || stats.MissingGames != 1 || stats.Issues != 2 {
		t.Fatalf("unexpected missing counts: %+v", stats)
	}
	if len(stats.Largest) != 2 || stats.Largest[0].Name != "Known Game" || stats.Largest[0].Size != 12 {
		t.Fatalf("unexpected largest games: %+v", stats.Largest)
	}
}

func TestNotifications(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)

	var requests []map[string]any
	var paths []string
	failing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, body)
		paths = append(paths, r.URL.Path)
	}))
	defer server.Close()
	oldTelegram := telegramApiUrl
	telegramApiUrl = server.URL
	defer func() { telegramApiUrl = oldTelegram }()

	original := settings.ReadSettings(web.dataFolder).Notifications
	defer settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Notifications = original })
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Notifications = settings.NotificationOptions{WebhookUrl: server.URL + "/hook", NotifyUpdates: true, NotifyDlc: true}
	})

	items := web.availableItems("en")
	if len(items) != 2 || items[0].Kind != "dlc" || items[1].Kind != "update" || items[1].Detail != "v131072" {
		t.Fatalf("unexpected items: %+v", items)
	}

	// the first check only records what is already missing
	web.notifyChanges()
	if len(requests) != 0 {
		t.Fatalf("existing items must not be reported: %+v", requests)
	}

	// a new DLC is released
	game := switchDB.TitlesMap["0100000000010"]
	game.Dlc["0100000000011003"] = db.TitleAttributes{Id: "0100000000011003", Name: "Brand New DLC"}
	// changed in place: a new titles database would be a new state
	web.invalidateDerived()
	web.notifyChanges()
	if len(requests) != 1 || !strings.Contains(requests[0]["message"].(string), "Brand New DLC") || strings.Contains(requests[0]["message"].(string), "Missing DLC") {
		t.Fatalf("only the new DLC should be reported: %+v", requests)
	}
	web.notifyChanges()
	if len(requests) != 1 {
		t.Fatal("an item must be reported only once")
	}

	// a failed delivery is retried by the next check
	game.Dlc["0100000000011004"] = db.TitleAttributes{Id: "0100000000011004", Name: "Another DLC"}
	web.invalidateDerived()
	failing = true
	web.notifyChanges()
	failing = false
	web.notifyChanges()
	if len(requests) != 2 || !strings.Contains(requests[1]["message"].(string), "Another DLC") {
		t.Fatalf("failed notification was not retried: %+v", requests)
	}

	// Telegram
	telegram := settings.NotificationOptions{TelegramBotToken: "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ", TelegramChatId: "-100123"}
	if err := sendNotification(telegram, "es", []NotificationItem{{Kind: "update", Name: "Juego", Detail: "v2"}}); err != nil {
		t.Fatal(err)
	}
	last := requests[len(requests)-1]
	if paths[len(paths)-1] != "/bot123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ/sendMessage" || last["chat_id"] != "-100123" || !strings.Contains(last["text"].(string), "Actualización v2 de Juego") {
		t.Fatalf("unexpected Telegram request: %v %+v", paths[len(paths)-1], last)
	}
}

func TestValidateNotifications(t *testing.T) {
	valid := settings.NotificationOptions{
		DiscordWebhookUrl: "https://discord.com/api/webhooks/123/abc-DEF_1",
		TelegramBotToken:  "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		TelegramChatId:    "@my_channel",
		WebhookUrl:        "http://192.168.1.10:8080/notify",
	}
	if errs := validateNotifications(valid, "en"); len(errs) != 0 {
		t.Fatalf("valid options rejected: %+v", errs)
	}
	invalid := []settings.NotificationOptions{
		{DiscordWebhookUrl: "https://evil.example/api/webhooks/1/x"},
		{TelegramBotToken: "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ"},
		{TelegramBotToken: "not-a-token", TelegramChatId: "1"},
		{TelegramBotToken: "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ", TelegramChatId: "abc"},
		{WebhookUrl: "ftp://server/file"},
	}
	for _, options := range invalid {
		if errs := validateNotifications(options, "en"); len(errs) == 0 {
			t.Errorf("invalid options accepted: %+v", options)
		}
	}
}

func TestArchive(t *testing.T) {
	web := newTestWeb(t)
	web.HandleArchive()
	web.state.set(testDatabases(t))

	get := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder
	}

	if r := get("/api/titles/ffffffffffff0000/archive.zip"); r.Code != http.StatusNotFound {
		t.Fatalf("unknown game: %d", r.Code)
	}

	r := get("/api/titles/0100000000010000/archive.zip")
	if r.Code != http.StatusOK || !strings.Contains(r.Header().Get("Content-Disposition"), "Known Game [0100000000010000].zip") {
		t.Fatalf("unexpected response: %d %v", r.Code, r.Header())
	}
	archive, err := zip.NewReader(bytes.NewReader(r.Body.Bytes()), int64(r.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, file := range archive.File {
		names = append(names, file.Name)
		if file.Method != zip.Store {
			t.Errorf("%s is compressed", file.Name)
		}
		content, _ := file.Open()
		data, _ := io.ReadAll(content)
		content.Close()
		if string(data) != "data" {
			t.Errorf("%s has unexpected content %q", file.Name, data)
		}
	}
	want := []string{
		"Known Game [0100000000010000]/Known [0100000000010000][v0].nsp",
		"Known Game [0100000000010000]/Updates/Known [0100000000010800][v65536].nsp",
		"Known Game [0100000000010000]/DLC/Known DLC [0100000000011001][v0].nsp",
	}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected entries:\n%s", strings.Join(names, "\n"))
	}

	detail, _ := web.getTitleDetail("0100000000010000", "en")
	if detail.ArchiveFiles != 3 || detail.ArchiveSize != 12 {
		t.Fatalf("unexpected archive summary: %d files, %d bytes", detail.ArchiveFiles, detail.ArchiveSize)
	}

	// a multi content file holding the base and its update is included once
	local := &db.SwitchGameFiles{BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{BaseFolder: "/roms", FileName: "all.xci"}}}
	local.Updates = map[int]db.SwitchFileInfo{65536: {ExtendedInfo: local.File.ExtendedInfo}}
	if entries := archiveEntries(local); len(entries) != 1 {
		t.Fatalf("multi content file listed %d times", len(entries))
	}
	if safeFileName(`Bad: name? <x>`) != "Bad name x" || safeFileName("  ") != "game" {
		t.Fatal("unexpected safe file name")
	}
}

func TestLocalImageUrl(t *testing.T) {
	_, localDB := testDatabases(t)
	localDB.TitlesMap["0100000000010"].Icon = "icon.jpg"
	for id, want := range map[string]string{
		"0100000000010000": "/i/icon.jpg", // base game
		"0100000000011002": "/i/icon.jpg", // its DLC
		"0100000000020000": "",            // not in the library
		"invalid":          "",
	} {
		if got := localImageUrl(localDB, id); got != want {
			t.Errorf("localImageUrl(%q) = %q, want %q", id, got, want)
		}
	}
	if localImageUrl(nil, "0100000000010000") != "" {
		t.Error("no library: no image")
	}
}

func TestApiStatistics(t *testing.T) {
	web := newTestWeb(t)
	web.HandleApiDocs()
	web.state.set(testDatabases(t))

	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/statistics", nil))
	var stats map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &stats); err != nil {
		t.Fatalf("invalid JSON %q: %v", recorder.Body.String(), err)
	}
	if stats["games"] != float64(2) || stats["totalSize"] != float64(20) || stats["gamesWithUpdate"] != float64(1) || stats["synchronizing"] != false {
		t.Fatalf("unexpected statistics: %v", stats)
	}
	if largest := stats["largest"].([]any); len(largest) != 2 || largest[0].(map[string]any)["name"] != "Known Game" {
		t.Fatalf("unexpected largest games: %v", stats["largest"])
	}
}

// Every API endpoint must be described in openapi.json.
func TestOpenApiDocumentsEveryEndpoint(t *testing.T) {
	data, err := os.ReadFile("../resources/static/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}

	web := newTestWeb(t)
	web.HandleApi()
	web.HandleArchive()
	web.HandleExport()
	web.HandleSynchronize()
	web.HandleIgnore()
	web.HandleApiDocs()
	documented := 0
	web.router.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		path, _ := route.GetPathTemplate()
		methods, _ := route.GetMethods()
		operations, ok := spec.Paths[path]
		if !ok {
			t.Errorf("%s is not documented", path)
			return nil
		}
		for _, method := range methods {
			if _, ok := operations[strings.ToLower(method)]; !ok {
				t.Errorf("%s %s is not documented", method, path)
			}
		}
		documented++
		return nil
	})
	if documented < 10 {
		t.Fatalf("only %d routes checked", documented)
	}
	if _, ok := spec.Paths["/healthz"]; !ok {
		t.Error("/healthz is not documented")
	}
}

func TestNavCountsAndCardStatus(t *testing.T) {
	web := newTestWeb(t)
	if counts := web.navCounts(); counts != (NavCounts{}) {
		t.Fatalf("empty state: %+v", counts)
	}
	web.state.set(testDatabases(t))

	counts := web.navCounts()
	if counts.Updates != 1 || counts.Dlc != 1 || counts.Issues != 2 {
		t.Fatalf("unexpected counts: %+v", counts)
	}
	page := web.globalPageData("index")
	if !page.HasLibrary || page.Counts != counts {
		t.Fatalf("unexpected page data: %+v", page)
	}

	library, _ := web.getLibrary(defaultFilter(), "en")
	known := library[1]
	if known.Name != "Known Game" || !known.UpdateAvailable || known.MissingDlcCount != 1 {
		t.Fatalf("unexpected card status: %+v", known)
	}
	if library[0].UpdateAvailable || library[0].MissingDlcCount != 0 {
		t.Fatalf("a game unknown to the titles database has no status: %+v", library[0])
	}
}

func TestLibraryStatusAndFormatFilters(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))

	all, _, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	if len(all) != 2 || facets.All != 2 || facets.Update != 1 || facets.Dlc != 1 || facets.Complete != 0 {
		t.Fatalf("unexpected facets: %+v", facets)
	}
	if len(facets.Formats) == 0 {
		t.Fatal("the formats of the library must be offered")
	}

	filter := defaultFilter()
	filter.Status = STATUS_UPDATE
	items, p, facets := web.getLibraryWithFacets(filter, "en")
	if len(items) != 1 || items[0].Name != "Known Game" || p.NumItems != 1 {
		t.Fatalf("update filter: %+v", items)
	}
	if facets.All != 2 {
		t.Fatalf("the counts must not depend on the status filter: %+v", facets)
	}

	filter.Status = STATUS_COMPLETE
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) != 0 {
		t.Fatalf("no game is complete: %+v", items)
	}

	filter = defaultFilter()
	filter.Format = "nope"
	filter.Normalize()
	if items, _, facets := web.getLibraryWithFacets(filter, "en"); len(items) != 0 || facets.All != 0 || len(facets.Formats) == 0 {
		t.Fatalf("an unknown format matches nothing but keeps the format list: %+v %+v", items, facets)
	}

	filter.Format = strings.ToLower(all[1].Type)
	filter.Normalize()
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) == 0 {
		t.Fatalf("filtering by an existing format (%s) must find games", filter.Format)
	}
}

func TestFilterQueryKeepsOtherFilters(t *testing.T) {
	f := &TitleItemFilter{Keyword: "zelda", Status: STATUS_DLC, Format: "NSZ", PerPage: 48, SortBy: "name", SortOrder: "desc", Page: 3}
	query := f.query("status", STATUS_UPDATE).Encode()
	for _, want := range []string{"q=zelda", "status=update", "format=nsz", "per_page=48", "sort_order=desc"} {
		if !strings.Contains(query, want) {
			t.Errorf("%q misses %q", query, want)
		}
	}
	if strings.Contains(query, "page=3") {
		t.Errorf("changing a filter must go back to the first page: %q", query)
	}
	if cleared := f.query("q", "", "status", "", "format", "").Encode(); strings.Contains(cleared, "q=") || strings.Contains(cleared, "status=") || strings.Contains(cleared, "format=") {
		t.Errorf("empty values must be removed: %q", cleared)
	}
}

func TestSetupStatus(t *testing.T) {
	web := newTestWeb(t)
	original := *settings.ReadSettings(web.dataFolder)
	defer settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Folder, s.ScanFolders = original.Folder, original.ScanFolders
	})

	missing := filepath.Join(t.TempDir(), "missing")
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Folder, s.ScanFolders = missing, []string{}
	})
	status := web.setupStatus()
	if status.TitlesDatabase || status.Library || status.Folders || len(status.MissingFolders) != 1 || status.MissingFolders[0] != missing {
		t.Fatalf("nothing set up: %+v", status)
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Folder = t.TempDir()
	})
	web.state.set(testDatabases(t))
	status = web.setupStatus()
	if !status.TitlesDatabase || !status.Library || !status.Folders || len(status.MissingFolders) != 0 {
		t.Fatalf("set up: %+v", status)
	}
	if status.Total != 4 || status.Done < 3 || status.Percent != status.Done*25 {
		t.Fatalf("progress: %+v", status)
	}
}

func TestReleaseDatesWithOnlyTheYear(t *testing.T) {
	for value, want := range map[int]string{20171027: "2017-10-27", 2019: "2019-01-01", 202403: "2024-03-01"} {
		got, err := intToTime(value)
		if err != nil || got.Format("2006-01-02") != want {
			t.Errorf("intToTime(%d) = %v, %v", value, got, err)
		}
	}
}

func TestLibraryKindRegionAndExtraFilters(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.TitlesMap["0100000000010"].Attributes.IsDemo = true
	web.state.set(switchDB, localDB)

	_, _, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	if facets.Demos != 1 || facets.Games != 1 || facets.Unknown != 1 || facets.NoCover != 2 || len(facets.Regions) != 1 {
		t.Fatalf("unexpected facets: %+v", facets)
	}

	filter := defaultFilter()
	filter.Kind = KIND_DEMO
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) != 1 || !items[0].Demo {
		t.Fatalf("demos only: %+v", items)
	}
	filter.Kind = KIND_GAME
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) != 1 || items[0].Demo {
		t.Fatalf("games only: %+v", items)
	}

	filter = defaultFilter()
	filter.Extra = EXTRA_UNKNOWN
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) != 1 || items[0].Known {
		t.Fatalf("not recognized: %+v", items)
	}

	filter = defaultFilter()
	filter.Region = "us"
	filter.Normalize()
	if items, _, facets := web.getLibraryWithFacets(filter, "en"); len(items) != 1 || items[0].Region != "US" || len(facets.Regions) != 1 {
		t.Fatalf("region: %+v", items)
	}

	filter = &TitleItemFilter{Kind: "x", Extra: "y", Region: "<script>"}
	filter.Normalize()
	if filter.Kind != "" || filter.Extra != "" || filter.Region != "" {
		t.Fatalf("invalid values must be dropped: %+v", filter)
	}

	for _, name := range []string{"Game <Demo>", "Game (Demo)", "Game [Trial Version]", "Game Demo Version"} {
		if !isDemo(nil, name) {
			t.Errorf("%q is a demo", name)
		}
	}
	for _, name := range []string{"Demolition Crew", "Demon Slayer", "Game"} {
		if isDemo(nil, name) {
			t.Errorf("%q is not a demo", name)
		}
	}
}

func TestHiddenDemosLeaveEveryList(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.TitlesMap["0100000000010"].Attributes.IsDemo = true
	web.state.set(switchDB, localDB)
	if len(web.missingUpdates()) == 0 || len(web.missingDLC()) == 0 {
		t.Fatal("the demo has a missing update and DLC while demos are shown")
	}

	hide := settings.ReadSettings(web.dataFolder).HideDemoGames
	defer settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HideDemoGames = hide })
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HideDemoGames = true })
	if len(web.missingUpdates()) != 0 || len(web.missingDLC()) != 0 {
		t.Fatal("hidden demos leave the missing updates and DLC")
	}
	items, _, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	if len(items) != 1 || items[0].Demo || !facets.DemosHidden || facets.Demos != 1 {
		t.Fatalf("hidden demos leave the library but are still counted: %+v %+v", items, facets)
	}
	filter := defaultFilter()
	filter.Kind = KIND_DEMO
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) != 1 || !items[0].Demo {
		t.Fatalf("the kind filter still shows the demos: %+v", items)
	}
}

func TestNamesSortWithoutLeadingSigns(t *testing.T) {
	items := []TitleItem{{Name: "Zelda"}, {Name: "\"GUDETAMARUN\""}, {Name: "#Anagrams"}, {Name: "apple"}, {Name: "!!!"}}
	sort.Stable(TitleItemByName(items))
	got := []string{}
	for _, item := range items {
		got = append(got, item.Name)
	}
	if strings.Join(got, "|") != "!!!|#Anagrams|apple|\"GUDETAMARUN\"|Zelda" {
		t.Fatalf("quotes and signs are not sorted first: %v", got)
	}
}

func TestLibraryGenrePlayersAndLanguageFilters(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	known := switchDB.TitlesMap["0100000000010"]
	known.Attributes.Genres = []string{"Action", "RPG"}
	known.Attributes.Players = 4
	known.Attributes.Languages = []string{"en", "es"}
	known.Attributes.Publisher = "Nintendo"
	web.state.set(switchDB, localDB)

	_, _, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	if len(facets.Genres) != 2 || facets.FourPlayers != 1 || facets.TwoPlayers != 1 || len(facets.Languages) != 2 {
		t.Fatalf("unexpected facets: %+v", facets)
	}
	for _, filter := range []*TitleItemFilter{{Genre: "RPG"}, {Players: "4"}, {GameLanguage: "ES"}} {
		filter.PerPage, filter.SortBy, filter.SortOrder, filter.Page = 24, "name", "asc", 1
		filter.Normalize()
		if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) != 1 || items[0].Name != "Known Game" {
			t.Fatalf("%+v: %+v", filter, items)
		}
	}
	filter := &TitleItemFilter{Genre: "Nope", Players: "3", GameLanguage: "xx"}
	filter.Normalize()
	if filter.Genre != "" || filter.Players != "" || filter.GameLanguage != "" {
		t.Fatalf("unknown values are dropped: %+v", filter)
	}

	stats := web.buildStatistics("en")
	if len(stats.ByGenre) != 2 || stats.ByPublisher[0].Name != "Nintendo" || len(stats.ByYear) != 1 || stats.ByYear[0].Name != "2017" {
		t.Fatalf("statistics by genre, publisher and year: %+v %+v %+v", stats.ByGenre, stats.ByPublisher, stats.ByYear)
	}
}

// waitForBackgroundWork waits until no scan, and no work after a scan, has run for a moment:
// a scan can start a little after the action that asks for it.
func waitForBackgroundWork(web *Web) {
	if !web.backgroundStarted.Load() {
		return
	}
	quiet := time.Time{}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if web.backgroundWork.Load() != 0 || web.state.IsSynchronizing() {
			quiet = time.Time{}
			continue
		}
		if quiet.IsZero() {
			quiet = time.Now()
		} else if time.Since(quiet) > 300*time.Millisecond {
			return
		}
	}
}
