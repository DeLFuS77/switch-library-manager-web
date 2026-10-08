package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
			if r.URL.Query().Get("client_secret") != "good-secret" {
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
			if strings.Contains(string(body), "Unknown") {
				io.WriteString(w, `[]`)
				return
			}
			io.WriteString(w, `[{"id":1,"name":"Star Odyssey Deluxe","slug":"star-odyssey-deluxe"},{"id":2,"name":"Star Odyssey","slug":"star-odyssey"}]`)
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
	if value.Main() != 54000 || value.Url() != "https://www.igdb.com/games/star-odyssey" || !value.Checked.Equal(now) {
		t.Fatalf("main %d, url %s", value.Main(), value.Url())
	}
	if value, err := client.lookup(context.Background(), "Unknown Game", now); err != nil || value.Found || value.Known() {
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

	if !web.lookupTimesToBeat(TRIGGER_MANUAL) {
		t.Fatal("the lookup starts")
	}
	for deadline := time.Now().Add(20 * time.Second); web.timeToBeatRunning.Load() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	library := web.buildLibrary(DEFAULT_LANGUAGE)
	if len(library) == 0 {
		t.Fatal("no library")
	}
	value, ok := web.timesToBeat().get(library[0].Id)
	if !ok || !value.Known() {
		t.Fatalf("kept: %+v %v", value, ok)
	}
	if item := web.buildLibrary(DEFAULT_LANGUAGE)[0]; item.TimeToBeat != 54000 {
		t.Fatalf("the library knows the time: %d", item.TimeToBeat)
	}

	// the next run has nothing to look up
	before := requests.Load()
	if web.lookupTimesToBeat(TRIGGER_MANUAL) || requests.Load() != before {
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
	if web.lookupTimesToBeat(TRIGGER_MANUAL) {
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
