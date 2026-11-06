package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// ownedLibrary is a library with one game of the Switch, three missing ones of a series and of
// other genres, and games of the Switch 2: one out, one still to come and one without a date.
func ownedLibrary(t *testing.T) *Web {
	t.Helper()
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	title := func(id string, name string, release int, genre string) *db.SwitchTitle {
		return &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: name, Region: "US", Publisher: "Lunar", ReleaseDate: release,
			Genres: []string{genre}, IconUrl: "https://example.com/" + id + ".jpg"}, Dlc: map[string]db.TitleAttributes{}}
	}
	runner2 := title("0100000000020000", "Moon Runner 2", 20220101, "Racing")
	runner2.Dlc["0100000000021001"] = db.TitleAttributes{Id: "0100000000021001", Name: "Moon Pack"}
	future := time.Now().AddDate(0, 2, 0)
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": title("0100000000010000", "Moon Runner", 20200101, "Racing"),
		"0100000000020": runner2,
		"0100000000030": title("0100000000030000", "Quiet Puzzle", 20210101, "Puzzle"),
		"0400000000040": title("0400000000040000", "Star Kart", 20250605, "Racing"),
		"0400000000050": title("0400000000050000", "Star Kart Rally", dateNumber(future), "Racing"),
		"0400000000060": title("0400000000060000", "Comet Rally", 0, "Racing"),
	}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": ownedGame("0100000000010000"),
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})
	web.HandleOwned()
	return web
}

func markOwned(t *testing.T, web *Web, id string, owned bool) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	form := url.Values{"id": {id}, "owned": {map[bool]string{true: "true", false: "false"}[owned]}}
	request := httptest.NewRequest(http.MethodPost, "/owned", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	web.router.ServeHTTP(recorder, request)
	return recorder
}

func missingNames(web *Web) []string {
	all := &TitleItemFilter{}
	all.Normalize()
	missing, _, _ := web.getMissingGamesWithFacets(all, "en")
	return itemNames(missing)
}

// A game marked "I have it" is a game of the library without a file: it is missing nowhere, the
// library shows it with its mark and without updates or DLC to ask for, and it leaves the
// wishlist.
func TestOwnedGamesAreNotMissing(t *testing.T) {
	web := ownedLibrary(t)
	web.wishes().Set("0100000000020000", true)
	if got := missingNames(web); !slices.Equal(got, []string{"Moon Runner 2", "Quiet Puzzle"}) {
		t.Fatalf("missing before: %v", got)
	}

	recorder := markOwned(t, web, "0100000000020000", true)
	var answer struct {
		Owned bool `json:"owned"`
		Count int  `json:"count"`
	}
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &answer) != nil || !answer.Owned || answer.Count != 1 {
		t.Fatalf("marking: %d %s", recorder.Code, recorder.Body)
	}
	if web.wishes().Has("0100000000020000") {
		t.Error("a game one has is not wished")
	}
	if got := missingNames(web); !slices.Equal(got, []string{"Quiet Puzzle"}) {
		t.Errorf("missing: %v", got)
	}
	if count := web.missingGamesCount(); count != 1 {
		t.Errorf("missing games counted: %d", count)
	}
	if stats := web.buildStatistics("en"); stats.MissingGames != 1 || stats.Games != 2 || stats.GamesWithFile != 1 || stats.GamesUpToDatePct != 100 {
		t.Errorf("statistics: %d missing, %d games, %d with a file, %d%% up to date", stats.MissingGames, stats.Games, stats.GamesWithFile, stats.GamesUpToDatePct)
	}

	library, _, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	if got := itemNames(library); !slices.Equal(got, []string{"Moon Runner", "Moon Runner 2"}) {
		t.Fatalf("library: %v", got)
	}
	for _, item := range library {
		if item.Name == "Moon Runner 2" && (!item.NoFile || item.Selectable || item.UpdateAvailable || item.MissingDlcCount != 0 || item.KnownDlc != 1 || item.Added.IsZero()) {
			t.Errorf("the game without a file: %+v", item)
		}
	}
	if facets.Complete != 1 || facets.All != 2 {
		t.Errorf("a game without a file is neither up to date nor behind: %+v", facets)
	}
	complete := defaultFilter()
	complete.Status = STATUS_COMPLETE
	if items, _ := web.getLibrary(complete, "en"); !slices.Equal(itemNames(items), []string{"Moon Runner"}) {
		t.Errorf("up to date: %v", itemNames(items))
	}

	// the series counts it as a game one has
	found := false
	for _, saga := range web.sagaProgress("en") {
		if saga.Total == 2 {
			found = true
			if saga.Owned != 2 {
				t.Errorf("series: %d of %d", saga.Owned, saga.Total)
			}
		}
	}
	if !found {
		t.Error("the series of Moon Runner")
	}
	for _, recommendation := range web.recommendationsAt("en", time.Now()) {
		if recommendation.Item.Name == "Moon Runner 2" {
			t.Error("a game one has is not suggested")
		}
	}

	// only games: no update or DLC, nor a wrong ID
	for _, id := range []string{"0100000000020800", "0100000000021001", "nope", ""} {
		if recorder := markOwned(t, web, id, true); recorder.Code != http.StatusBadRequest {
			t.Errorf("%q: %d", id, recorder.Code)
		}
	}

	// the mark taken away, it is missing again
	markOwned(t, web, "0100000000020000", false)
	if got := missingNames(web); !slices.Equal(got, []string{"Moon Runner 2", "Quiet Puzzle"}) {
		t.Errorf("missing after: %v", got)
	}
	if web.owned().Count() != 0 {
		t.Error("the mark is taken away")
	}
}

// The cards and the page of a game show the mark: a button next to the heart of a missing game,
// filled with "I have it · no file" in the Library, and a button on the page of the game, which
// then has My game.
func TestOwnedMarkIsShown(t *testing.T) {
	web := ownedLibrary(t)
	web.HandleMissing()
	web.HandleIndex()
	web.HandleTitle()
	get := func(path string) string {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, recorder.Code)
		}
		return recorder.Body.String()
	}

	missing := get("/missing.html")
	for _, want := range []string{`class="own-toggle editor-only next-to-heart" data-own="0100000000020000" data-owned="false"`, `<use href="#own-off">`, `<symbol id="own-on"`} {
		if !strings.Contains(missing, want) {
			t.Errorf("Missing Games misses %q", want)
		}
	}
	if page := get("/title/0100000000020000.html"); !strings.Contains(page, `data-own="0100000000020000" data-owned="false"`) || strings.Contains(page, `id="playTitle"`) || strings.Contains(page, `data-fav=`) {
		t.Error("the page of a missing game: the button, without My game nor Favorite")
	}

	markOwned(t, web, "0100000000020000", true)
	library := get("/index.html?missing=0")
	for _, want := range []string{"no-file-card is-owned-card", `class="own-toggle editor-only is-owned" data-own="0100000000020000" data-owned="true"`, "I have it · no file", "DLC not counted"} {
		if !strings.Contains(library, want) {
			t.Errorf("the Library misses %q", want)
		}
	}
	page := get("/title/0100000000020000.html")
	for _, want := range []string{`btn-own" data-own="0100000000020000" data-owned="true"`, `id="playTitle"`, `data-fav="0100000000020000"`, "I have it · no file"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page of the game misses %q", want)
		}
	}
	if strings.Contains(page, `data-wish=`) {
		t.Error("a game one has is not wished from its page")
	}
	// a game with its file has no button
	if page := get("/title/0100000000010000.html"); strings.Contains(page, `data-own=`) {
		t.Error("a game of the library with its file")
	}
}

// The games of the Switch 2 are missing only with "I have a Switch 2", once they are out: a game
// without a date shows "No date". Upcoming releases does not change.
func TestSwitch2SettingShowsItsGames(t *testing.T) {
	web := ownedLibrary(t)
	if got := missingNames(web); slices.ContainsFunc(got, func(name string) bool { return strings.Contains(name, "Star") || strings.Contains(name, "Comet") }) {
		t.Fatalf("without the setting: %v", got)
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HasSwitch2 = true })
	if got := missingNames(web); !slices.Equal(got, []string{"Comet Rally", "Moon Runner 2", "Quiet Puzzle", "Star Kart"}) {
		t.Fatalf("with the setting: %v", got)
	}
	if count := web.missingGamesCount(); count != 4 {
		t.Errorf("counted: %d", count)
	}
	for _, game := range web.upcomingGamesAt("en", time.Now()) {
		if db.Switch2(game.Item.Id) {
			t.Errorf("upcoming: %s", game.Item.Name)
		}
	}
	web.HandleMissing()
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing.html", nil))
	page := recorder.Body.String()
	for _, want := range []string{`<span class="badge badge-switch2">Switch 2</span>`, "No date"} {
		if !strings.Contains(page, want) {
			t.Errorf("Missing Games misses %q", want)
		}
	}

	// a game of the Switch 2 marked shows in the library, whatever the setting
	markOwned(t, web, "0400000000060000", true)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HasSwitch2 = false })
	if items, _ := web.getLibrary(defaultFilter(), "en"); !slices.Contains(itemNames(items), "Comet Rally") {
		t.Errorf("library: %v", itemNames(items))
	}
}

// titles.switch2.json adds the games of the Switch 2 titles.json does not list: the ones it lists
// win, a game it knows only by its DLC takes the name of the list, and a game taken out of the
// list goes away.
func TestSwitch2ListJoinsTheTitles(t *testing.T) {
	web := newTestWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.PreferredRegion = "EU" })
	write := func(content string) {
		if err := os.WriteFile(filepath.Join(web.dataFolder, SWITCH2_JSON_FILENAME), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"titles": {
		"0400C3F00006E000": {"name": "Mario Kart World (list)", "regions": ["EU", "US"]},
		"040077201BE50000": {"name": "Kirby Air Riders", "regions": ["JP", "US", "EU"], "sold": ["digital"]},
		"0400AAA000001000": {"name": "Only DLC", "regions": ["JP"]},
		"0400CA40208C9001": {"name": "A DLC", "regions": ["US"]},
		"0100000000010000": {"name": "A game of the Switch"},
		"0400BBB000002000": {"name": " - "}
	}}`)
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0400c3f00006e": {Attributes: db.TitleAttributes{Id: "0400C3F00006E000", Name: "Mario Kart World", Region: "US", ReleaseDate: 20250605}},
		"0400aaa000001": {Dlc: map[string]db.TitleAttributes{"0400aaa000002001": {Id: "0400AAA000002001", Name: "Pack"}}},
	}, Versions: map[string]map[int]string{}}
	web.applySwitch2Titles(switchDB)

	if title := switchDB.TitlesMap["0400c3f00006e"]; title.Attributes.Name != "Mario Kart World" || title.FromSwitch2List {
		t.Errorf("titles.json wins: %+v", title)
	}
	kirby := switchDB.TitlesMap["040077201be50"]
	if kirby == nil || kirby.Attributes.Name != "Kirby Air Riders" || kirby.Attributes.Id != "040077201BE50000" || kirby.Attributes.Region != "EU" || !kirby.FromSwitch2List {
		t.Fatalf("a game of the list: %+v", kirby)
	}
	if title := switchDB.TitlesMap["0400aaa000001"]; title.Attributes.Name != "Only DLC" || len(title.Dlc) != 1 || title.Attributes.Region != "JP" {
		t.Errorf("a game known by its DLC: %+v", title)
	}
	if len(switchDB.TitlesMap) != 3 {
		t.Errorf("only the games of the Switch 2 with a name: %d titles", len(switchDB.TitlesMap))
	}

	// what IGDB told of it is put in its title, without changing the database
	web.igdbTitles().set("040077201be50", IgdbTitle{Found: true, Checked: time.Now(), ReleaseDate: 20251120, Summary: "Race on stars.",
		Cover: "https://images.igdb.com/igdb/image/upload/t_cover_big/abc.jpg", Genres: []string{"Racing"}, Publisher: "Nintendo"})
	enriched := web.withLibraryTitles(switchDB, nil)
	if got := enriched.TitlesMap["040077201be50"].Attributes; got.ReleaseDate != 20251120 || got.Description != "Race on stars." || got.IconUrl == "" || got.Publisher != "Nintendo" {
		t.Errorf("with IGDB: %+v", got)
	}
	if switchDB.TitlesMap["040077201be50"].Attributes.IconUrl != "" {
		t.Error("the shared database is not changed")
	}
	if again := web.withLibraryTitles(enriched, nil); again != enriched {
		t.Error("nothing new: the same database")
	}

	// the list changed
	write(`{"titles": {"0400C3F00006E000": {"name": "Mario Kart World"}}}`)
	web.applySwitch2Titles(switchDB)
	if _, ok := switchDB.TitlesMap["040077201be50"]; ok {
		t.Error("a game taken out of the list goes away")
	}
	if title := switchDB.TitlesMap["0400aaa000001"]; title == nil || title.Attributes.Id != "" || len(title.Dlc) != 1 {
		t.Errorf("its DLC stay, without the game: %+v", title)
	}
}

// The games of the Switch 2 are searched on IGDB among the games of the Switch 2: its platform is
// asked once by its slug, and the games of the list get what IGDB tells of them.
func TestSwitch2GamesAreLookedUpOnTheirPlatform(t *testing.T) {
	platforms, games := &atomic.Int32{}, &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/token":
			io.WriteString(w, `{"access_token":"abc","expires_in":3600}`)
		case "/v4/platforms":
			platforms.Add(1)
			if !strings.Contains(string(body), `where slug = "switch-2";`) {
				t.Errorf("the platform is asked by its slug: %s", body)
			}
			io.WriteString(w, `[{"id":509,"slug":"switch-2"}]`)
		case "/v4/games":
			games.Add(1)
			if !strings.Contains(string(body), "platforms = (509)") {
				t.Errorf("a game of the Switch 2 is searched on its platform: %s", body)
			}
			io.WriteString(w, `[{"name":"Kirby Air Riders","first_release_date":1763596800,"summary":"Race on stars.","cover":{"image_id":"abc"},`+
				`"genres":[{"name":"Racing"}],"involved_companies":[{"company":{"name":"Nintendo"},"publisher":true}]}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	savedToken, savedApi := igdbTokenUrl, igdbApiUrl
	igdbTokenUrl, igdbApiUrl = server.URL+"/token", server.URL+"/v4/"
	defer func() { igdbTokenUrl, igdbApiUrl = savedToken, savedApi }()

	web := newTestWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HasSwitch2 = true })
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"040077201be50": {Attributes: db.TitleAttributes{Id: "040077201BE50000", Name: "Kirby Air Riders"}, FromSwitch2List: true},
		"0400000000060": {Attributes: db.TitleAttributes{Id: "0400000000060000", Name: "Comet Rally"}, FromSwitch2List: true},
	}}
	web.state.set(switchDB, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})
	if pending := web.switch2TitlesToLookUp(time.Now()); len(pending) != 2 {
		t.Fatalf("to look up: %v", pending)
	}
	client := newIgdbClient("my-id", "good-secret")
	// each game asked is told, with how many there are: the task shows it instead of 0/0
	told := []string{}
	tell := func(done int, total int, name string) {
		told = append(told, fmt.Sprintf("%d/%d %s", done, total, name))
	}
	if err := web.lookupLibraryTitles(context.Background(), client, tell); err != nil {
		t.Fatal(err)
	}
	if len(told) != 2 || told[0] != "0/2 Comet Rally" || told[1] != "1/2 Kirby Air Riders" {
		t.Errorf("told: %q", told)
	}
	if platforms.Load() != 1 || games.Load() != 2 {
		t.Errorf("asked: %d platforms, %d games", platforms.Load(), games.Load())
	}
	current, _ := web.state.get()
	if got := current.TitlesMap["040077201be50"].Attributes; got.ReleaseDate != 20251120 || got.IconUrl != "https://images.igdb.com/igdb/image/upload/t_cover_big/abc.jpg" || got.Description != "Race on stars." {
		t.Errorf("the title with IGDB: %+v", got)
	}
	// found with a date gone by: not asked again
	if pending := web.switch2TitlesToLookUp(time.Now().Add(30 * 24 * time.Hour)); len(pending) != 0 {
		t.Errorf("asked again: %v", pending)
	}

	// without the setting, only the games marked
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HasSwitch2 = false })
	web.igdbTitles().set("0400000000060", IgdbTitle{Found: true, Version: igdbTitleVersion, Checked: time.Now().Add(-40 * 24 * time.Hour)})
	if pending := web.switch2TitlesToLookUp(time.Now()); len(pending) != 0 {
		t.Errorf("without the setting: %v", pending)
	}
	web.owned().Set("0400000000060000", true)
	if pending := web.switch2TitlesToLookUp(time.Now()); len(pending) != 1 || pending["0400000000060"] != "Comet Rally" {
		t.Errorf("a game without a date is asked again after a month: %v", pending)
	}
	// not every week: the date of a game IGDB lists on the Switch only does not come
	web.igdbTitles().set("0400000000060", IgdbTitle{Found: true, Version: igdbTitleVersion, Checked: time.Now().Add(-10 * 24 * time.Hour)})
	if pending := web.switch2TitlesToLookUp(time.Now()); len(pending) != 0 {
		t.Errorf("a game without a date is not asked again after a week: %v", pending)
	}
	// a date still to come is asked again after a week, as it comes closer
	web.igdbTitles().set("0400000000060", IgdbTitle{Found: true, Version: igdbTitleVersion, ReleaseDate: 29991231,
		Checked: time.Now().Add(-10 * 24 * time.Hour)})
	if pending := web.switch2TitlesToLookUp(time.Now()); len(pending) != 1 {
		t.Errorf("a date still to come is asked again after a week: %v", pending)
	}

	// a game of the Switch is still searched on the Switch
	if platform, err := client.platformOf(context.Background(), "0100000000010000"); err != nil || platform != igdbSwitchPlatform {
		t.Errorf("the Switch: %d %v", platform, err)
	}
}

// The marks go in the backups and come back with them.
func TestOwnedGoesInTheBackup(t *testing.T) {
	web := newTestWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.HasSwitch2 = true })
	web.owned().Set("0400C3F00006E000", true)
	buffer := &bytes.Buffer{}
	if err := web.writeBackup(buffer); err != nil {
		t.Fatal(err)
	}
	web.owned().Set("0400C3F00006E000", false)
	web.owned().Set("0100000000010000", true)
	if err := web.restoreBackup(buffer.Bytes(), nil); err != nil {
		t.Fatal(err)
	}
	if !web.owned().Has("0400C3F00006E000") || web.owned().Has("0100000000010000") {
		t.Errorf("restored: %v", web.owned().Ids())
	}
	if !settings.ReadSettings(web.dataFolder).HasSwitch2 {
		t.Error("the setting is restored")
	}
}
