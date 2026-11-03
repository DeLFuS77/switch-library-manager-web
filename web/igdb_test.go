package web

import (
	"context"
	"errors"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeIgdb answers like Twitch and IGDB: a token for the right keys, two Switch games named
// like the search and the times of the second one.
func fakeIgdb(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case strings.HasPrefix(r.URL.Path, "/token"):
			if r.FormValue("client_secret") != "good-secret" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			io.WriteString(w, `{"access_token":"abc","expires_in":3600}`)
		case r.Header.Get("Authorization") != "Bearer abc" || r.Header.Get("Client-ID") != "my-id":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/v4/games":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "platforms = (130)") {
				t.Errorf("only Switch games are searched: %s", body)
			}
			if !strings.Contains(string(body), "aggregated_rating,aggregated_rating_count,rating,rating_count") {
				t.Errorf("the scores are asked with the game: %s", body)
			}
			if strings.Contains(string(body), "Unknown") {
				io.WriteString(w, `[]`)
				return
			}
			io.WriteString(w, `[{"id":1,"name":"Star Odyssey Deluxe","slug":"star-odyssey-deluxe","rating":40,"rating_count":2},`+
				`{"id":2,"name":"Star Odyssey","slug":"star-odyssey","collections":[77],"aggregated_rating":84.6,"aggregated_rating_count":23,"rating":78.4,"rating_count":140}]`)
		case r.URL.Path == "/v4/collections":
			io.WriteString(w, `[{"id":77,"name":"Star Odyssey","games":[{"name":"Star Odyssey","platforms":[130]},{"name":"Star Odyssey II","platforms":[6,130]},{"name":"Star Odyssey PC","platforms":[6]}]}]`)
		case r.URL.Path == "/v4/game_time_to_beats":
			io.WriteString(w, `[{"hastily":36000,"normally":54000,"completely":90000,"count":12}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	savedToken, savedApi := igdbTokenUrl, igdbApiUrl
	igdbTokenUrl, igdbApiUrl = server.URL+"/token", server.URL+"/v4/"
	t.Cleanup(func() { igdbTokenUrl, igdbApiUrl = savedToken, savedApi })
	return server, requests
}

func TestIgdbLookup(t *testing.T) {
	fakeIgdb(t)
	client := newIgdbClient("my-id", "good-secret")
	now := time.Now()
	value, err := client.lookup(context.Background(), "Star Odyssey", now)
	if err != nil || !value.Known() || value.GameId != 2 || value.Normally != 54000 || value.Count != 12 {
		t.Fatalf("lookup: %+v %v", value, err)
	}
	if len(value.Collections) != 1 || value.Collections[0] != 77 || !value.SeriesChecked {
		t.Fatalf("the series of the game: %+v", value)
	}
	if value.Main() != 54000 || value.Slug != "star-odyssey" || !value.Checked.Equal(now) {
		t.Fatalf("main %d, slug %s", value.Main(), value.Slug)
	}
	if value.Critics() != 85 || value.CriticCount != 23 || value.Users() != 78 || value.UserCount != 140 || !value.HasScores() || !value.ScoresChecked {
		t.Fatalf("the scores of the game: %+v", value)
	}
	if value, err := client.lookup(context.Background(), "Unknown Game", now); err != nil || value.Found || value.Known() || value.HasScores() {
		t.Fatalf("a game IGDB does not know: %+v %v", value, err)
	}

	if _, err := newIgdbClient("my-id", "bad").lookup(context.Background(), "Star Odyssey", now); !errors.Is(err, errIgdbCredentials) {
		t.Fatalf("wrong keys: %v", err)
	}
}

func TestIgdbIsNotFasterThanAllowed(t *testing.T) {
	fakeIgdb(t)
	client := newIgdbClient("my-id", "good-secret")
	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := client.lookup(context.Background(), "Star Odyssey", start); err != nil {
			t.Fatal(err)
		}
	}
	// six requests to IGDB, at least five gaps between them
	if elapsed := time.Since(start); elapsed < 5*igdbRequestGap {
		t.Fatalf("too fast: %v", elapsed)
	}
}

func TestApicalypseText(t *testing.T) {
	if got := apicalypseText(`Say "hi"\ now`); strings.ContainsAny(got, `"\`) {
		t.Fatalf("quotes are removed: %q", got)
	}
}

func TestHoursText(t *testing.T) {
	for seconds, want := range map[int]string{0: "", 1200: "<1 h", 5400: "~1.5 h", 36000: "~10 h", 200000: "~56 h"} {
		if got := hoursText(seconds); got != want {
			t.Errorf("hoursText(%d) = %q, want %q", seconds, got, want)
		}
	}
}

func TestTimesToBeatAreLookedUpAndKept(t *testing.T) {
	_, requests := fakeIgdb(t)
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	t.Setenv(IGDB_CLIENT_ID_ENV, "my-id")
	t.Setenv(IGDB_CLIENT_SECRET_ENV, "good-secret")

	if !web.lookupTimesToBeat(tasklog.TRIGGER_MANUAL) {
		t.Fatal("the lookup starts")
	}
	for deadline := time.Now().Add(20 * time.Second); web.timeToBeatRunning.Load() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	library := web.buildLibrary(i18n.DEFAULT_LANGUAGE)
	if len(library) == 0 {
		t.Fatal("no library")
	}
	value, ok := web.timesToBeat().get(library[0].Id)
	if !ok || !value.Known() {
		t.Fatalf("kept: %+v %v", value, ok)
	}
	if item := web.buildLibrary(i18n.DEFAULT_LANGUAGE)[0]; item.TimeToBeat != 54000 {
		t.Fatalf("the library knows the time: %d", item.TimeToBeat)
	}

	// the next run has nothing to look up
	before := requests.Load()
	if web.lookupTimesToBeat(tasklog.TRIGGER_MANUAL) || requests.Load() != before {
		t.Fatal("games looked up recently are not looked up again")
	}

	// a new web reads the kept times
	again := &Web{dataFolder: web.dataFolder}
	if value, ok := again.timesToBeat().get(library[0].Id); !ok || value.Normally != 54000 {
		t.Fatalf("read from the file: %+v", value)
	}
}

func TestNoLookupWithoutKeys(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	t.Setenv(IGDB_CLIENT_ID_ENV, "")
	t.Setenv(IGDB_CLIENT_SECRET_ENV, "")
	if web.lookupTimesToBeat(tasklog.TRIGGER_MANUAL) {
		t.Fatal("nothing without the keys")
	}
}

func TestSortByDurationPutsUnknownLast(t *testing.T) {
	items := []TitleItem{{Name: "C"}, {Name: "A", TimeToBeat: 50}, {Name: "B", TimeToBeat: 10}, {Name: "D"}}
	for _, order := range []string{"asc", "desc"} {
		sorted := append([]TitleItem(nil), items...)
		if err := sortItems(&TitleItemFilter{SortBy: "duration", SortOrder: order}, sorted); err != nil {
			t.Fatal(err)
		}
		names := ""
		for _, item := range sorted {
			names += item.Name
		}
		want := map[string]string{"asc": "BACD", "desc": "ABCD"}[order]
		if names != want {
			t.Errorf("%s: %s, want %s", order, names, want)
		}
	}
}

func TestIgdbCollectionsAreKept(t *testing.T) {
	fakeIgdb(t)
	web := newTestWeb(t)
	web.timesToBeat().set("0100000000010000", TimeToBeat{Found: true, Checked: time.Now(), SeriesChecked: true, Collections: []int{77, 78}})
	if missing := web.missingIgdbCollections(time.Now()); len(missing) != 2 {
		t.Fatalf("missing: %v", missing)
	}
	if err := web.lookupIgdbCollections(context.Background(), newIgdbClient("my-id", "good-secret")); err != nil {
		t.Fatal(err)
	}
	collections := web.igdbCollections().snapshot()
	if got := collections[77]; got.Name != "Star Odyssey" || len(got.Members) != 2 || got.Members[1] != "Star Odyssey II" {
		t.Fatalf("only the Switch games of the collection: %+v", got)
	}
	// a collection IGDB does not know any more is not asked again soon
	if _, ok := collections[78]; !ok || len(web.missingIgdbCollections(time.Now())) != 0 {
		t.Fatalf("all asked: %v", collections)
	}
}

// Games looked up before the series were asked are looked up once again.
func TestOldLookupsAreRepeatedForTheSeries(t *testing.T) {
	web := newTestWeb(t)
	web.timesToBeat().set("0100000000010000", TimeToBeat{Found: true, Checked: time.Now()})
	if !web.timesToBeat().needsLookup("0100000000010000", time.Now()) {
		t.Fatal("a game without its series is looked up again")
	}
}

// Games looked up before the scores were asked are looked up once again, and then not for a
// while.
func TestOldLookupsAreRepeatedForTheScores(t *testing.T) {
	web := newTestWeb(t)
	store := web.timesToBeat()
	store.set("0100000000010000", TimeToBeat{Found: true, Checked: time.Now(), SeriesChecked: true})
	if !store.needsLookup("0100000000010000", time.Now()) {
		t.Fatal("a game without its scores is looked up again")
	}
	store.set("0100000000010000", TimeToBeat{Found: true, Checked: time.Now(), SeriesChecked: true, ScoresChecked: true})
	if store.needsLookup("0100000000010000", time.Now()) {
		t.Fatal("a game with its scores waits")
	}
}

func TestScoresNeedReviewsOrVotes(t *testing.T) {
	for _, c := range []struct {
		value          TimeToBeat
		critics, users int
	}{
		{TimeToBeat{Found: true, CriticScore: 84.5, CriticCount: 3, UserScore: 70.2, UserCount: 9}, 85, 70},
		// a score without reviews or votes is not shown
		{TimeToBeat{Found: true, CriticScore: 90, UserScore: 60}, 0, 0},
		{TimeToBeat{CriticScore: 90, CriticCount: 3}, 0, 0},
	} {
		if c.value.Critics() != c.critics || c.value.Users() != c.users || c.value.HasScores() != (c.critics > 0 || c.users > 0) {
			t.Errorf("%+v: critics %d, users %d", c.value, c.value.Critics(), c.value.Users())
		}
	}
}

func TestScoreLevels(t *testing.T) {
	for score, want := range map[int]string{100: "top", 90: "top", 89: "high", 75: "high", 74: "mid", 50: "mid", 49: "low", 1: "low"} {
		if got := scoreLevel(score); got != want {
			t.Errorf("scoreLevel(%d) = %q, want %q", score, got, want)
		}
	}
}

// The game page shows the scores under the publisher, without the reviews and votes they come from
// and with no link out, and nothing without them.
func TestGamePageShowsTheScores(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.state.set(testDatabases(t))
	web.HandleTitle()
	get := func() string {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/title/0100000000010000.html", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("the game page: %d", recorder.Code)
		}
		return recorder.Body.String()
	}

	if page := get(); strings.Contains(page, `class="title-scores"`) {
		t.Fatal("no scores without IGDB")
	}
	web.timesToBeat().set("0100000000010000", TimeToBeat{Found: true, Checked: time.Now(), Slug: "star-odyssey",
		CriticScore: 93.6, CriticCount: 23, UserScore: 61, UserCount: 1})
	web.invalidateDerived()
	page := get()
	for _, want := range []string{`<div class="title-scores"`, `class="score score-top">94<`, "Critics", `class="score score-mid">61<`, "Players"} {
		if !strings.Contains(page, want) {
			t.Errorf("the game page misses %q", want)
		}
	}
	for _, unwanted := range []string{`<a class="title-scores"`, "23 reviews", "1 vote"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("the game page shows %q", unwanted)
		}
	}

	// only the score there is
	web.timesToBeat().set("0100000000010000", TimeToBeat{Found: true, Checked: time.Now(), UserScore: 40, UserCount: 1200})
	web.invalidateDerived()
	if page := get(); strings.Contains(page, "Critics") || !strings.Contains(page, `class="score score-low">40<`) || strings.Contains(page, "1,200 votes") {
		t.Error("the game page shows only the score of the users")
	}
}

func TestSortByScorePutsUnknownLast(t *testing.T) {
	items := []TitleItem{{Name: "C"}, {Name: "A", Score: 91}, {Name: "B", Score: 72}, {Name: "D"}}
	for order, want := range map[string]string{"asc": "BACD", "desc": "ABCD"} {
		sorted := append([]TitleItem(nil), items...)
		if err := sortItems(&TitleItemFilter{SortBy: "score", SortOrder: order}, sorted); err != nil {
			t.Fatal(err)
		}
		names := ""
		for _, item := range sorted {
			names += item.Name
		}
		if names != want {
			t.Errorf("%s: %s, want %s", order, names, want)
		}
	}
}

func TestScoreFilter(t *testing.T) {
	odd := &TitleItemFilter{Score: "85"}
	odd.Normalize()
	if odd.Score != "" {
		t.Fatalf("only 70, 80 and 90: %q", odd.Score)
	}
	filter := &TitleItemFilter{Score: "80", SortBy: "score"}
	filter.Normalize()
	if filter.Score != "80" || filter.SortBy != "score" || !filter.Filters() || filter.SecondaryCount() != 1 || filter.query().Get("score") != "80" {
		t.Fatalf("the filter: %+v", filter)
	}
	for _, c := range []struct {
		score  int
		filter string
		want   bool
	}{{85, "80", true}, {80, "80", true}, {79, "80", false}, {0, "70", false}, {0, "", true}} {
		if scoreMatches(c.score, c.filter) != c.want {
			t.Errorf("scoreMatches(%d, %q) != %v", c.score, c.filter, c.want)
		}
	}
	if query := cleanSmartQuery("?score=90&sort_by=score&sort_order=desc"); !strings.Contains(query, "score=90") || !strings.Contains(query, "sort_by=score") {
		t.Fatalf("a smart collection keeps the score: %q", query)
	}
}

// The library counts its games by score and shows only those of the score chosen.
func TestLibraryFiltersByScore(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	web.timesToBeat().set("0100000000010000", TimeToBeat{Found: true, Checked: time.Now(), CriticScore: 86, CriticCount: 12})
	web.invalidateDerived()
	all, _, facets := web.getLibraryWithFacets(&TitleItemFilter{SortBy: "name", SortOrder: "asc", PerPage: 24, Page: 1, WithMissing: "0"}, i18n.DEFAULT_LANGUAGE)
	if len(all) < 2 || facets.Score70 != 1 || facets.Score80 != 1 || facets.Score90 != 0 {
		t.Fatalf("%d games, scores %d/%d/%d", len(all), facets.Score70, facets.Score80, facets.Score90)
	}
	items, _, _ := web.getLibraryWithFacets(&TitleItemFilter{Score: "80", SortBy: "name", SortOrder: "asc", PerPage: 24, Page: 1, WithMissing: "0"}, i18n.DEFAULT_LANGUAGE)
	if len(items) != 1 || items[0].Id != "0100000000010000" || items[0].Score != 86 {
		t.Fatalf("only the game of 80 or more: %+v", items)
	}
}

// With scores, Statistics has a tab of its own: the averages, the games by score, the best ones
// and the ratings of the user against the scores of the same games.
func TestStatisticsShowTheScores(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.state.set(testDatabases(t))
	web.HandleStatistics()
	get := func() string {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/statistics.html", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("the statistics page: %d", recorder.Code)
		}
		return recorder.Body.String()
	}

	if page := get(); strings.Contains(page, `id="tabScores"`) {
		t.Fatal("no scores tab without IGDB")
	}
	web.timesToBeat().set("0100000000010000", TimeToBeat{Found: true, Checked: time.Now(), CriticScore: 93.6, CriticCount: 23, UserScore: 61, UserCount: 4})
	web.invalidateDerived()
	page := get()
	for _, want := range []string{`id="tabScores"`, `id="panelScores"`, `data-count="94"`, `data-count="61"`,
		`aria-label="90 or more" aria-valuenow="1"`, `aria-label="Under 50" aria-valuenow="0"`,
		`class="score score-sm score-top">94<`, `href="/title/0100000000010000.html">Known Game<`, "Rate your games with the stars"} {
		if !strings.Contains(page, want) {
			t.Errorf("the statistics page misses %q", want)
		}
	}

	// two stars are 40, far under the 94 of IGDB
	if err := web.play().Set("", "0100000000010000", userdata.PlayEntry{Rating: 2}); err != nil {
		t.Fatal(err)
	}
	page = get()
	for _, want := range []string{`class="score-versus-value">40<`, "IGDB in that game", "You rate lower than IGDB", `title="2 of 5"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the comparison misses %q", want)
		}
	}
	if strings.Contains(page, "You rate higher than IGDB") || strings.Contains(page, "Rate your games with the stars") {
		t.Error("only the games rated lower, and no hint once there are ratings")
	}

	// close to IGDB: no list
	if err := web.play().Set("", "0100000000010000", userdata.PlayEntry{Rating: 5}); err != nil {
		t.Fatal(err)
	}
	if page := get(); !strings.Contains(page, "Your ratings are close to the ones of IGDB.") || strings.Contains(page, "You rate lower than IGDB") {
		t.Error("a rating of 100 against 94 is close")
	}
}
