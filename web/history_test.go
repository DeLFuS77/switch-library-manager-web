package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestLibraryHistory(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)
	history := web.history()

	contents := libraryContents(switchDB, localDB)
	for _, key := range []string{"game:0100000000010000", "update:0100000000010800:65536", "dlc:0100000000011001", "game:0100000000030000"} {
		if _, ok := contents[key]; !ok {
			t.Fatalf("%s is in the library: %v", key, contents)
		}
	}
	if contents["dlc:0100000000011001"].name != "Owned DLC" {
		t.Fatalf("DLC names come from the titles database: %+v", contents["dlc:0100000000011001"])
	}

	day1 := time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)
	events, first, err := history.record(contents, day1)
	if err != nil || !first || len(events) != 0 {
		t.Fatalf("the first scan only remembers the library: %v %v %v", events, first, err)
	}
	if !history.added("game:0100000000010000").Equal(day1) {
		t.Fatal("the date a game was found is remembered")
	}

	// a game leaves, a DLC arrives
	delete(contents, "game:0100000000030000")
	contents["dlc:0100000000011002"] = libraryContent{kind: HISTORY_DLC, id: "0100000000011002", name: "Missing DLC"}
	day2 := day1.Add(24 * time.Hour)
	events, first, err = history.record(contents, day2)
	if err != nil || first || len(events) != 2 {
		t.Fatalf("two changes: %+v", events)
	}
	if !events[0].Added || events[0].Id != "0100000000011002" || events[1].Added || events[1].Id != "0100000000030000" || events[1].Name == "" {
		t.Fatalf("additions first, removals keep the name: %+v", events)
	}

	// nothing changed the same day: nothing to save again
	if events, _, _ := history.record(contents, day2.Add(time.Hour)); len(events) != 0 {
		t.Fatalf("no changes: %+v", events)
	}
	if days := history.days(); len(days) != 2 || days[1].Dlc != 2 {
		t.Fatalf("one entry per day: %+v", days)
	}
	if chart := historyChart(history.days()); chart == nil || chart.Points == "" || chart.Max == 0 {
		t.Fatalf("two days draw a chart: %+v", chart)
	}

	// the history survives a restart
	if _, err := os.Stat(filepath.Join(web.dataFolder, HISTORY_FILENAME)); err != nil {
		t.Fatal(err)
	}
	history.reload()
	if len(history.recent(10)) != 2 || !history.added("game:0100000000010000").Equal(day1) {
		t.Fatal("the history is read again")
	}
}

func TestNewGamesAreNotified(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	messages := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		json.NewDecoder(r.Body).Decode(&body)
		messages = append(messages, body["message"].(string))
	}))
	defer server.Close()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Notifications = settings.NotificationOptions{WebhookUrl: server.URL, NotifyNewGames: true}
	})

	web.notifyNewContent([]HistoryEvent{
		{Added: true, Kind: HISTORY_GAME, Id: "0100000000010000", Name: "Known Game"},
		{Added: true, Kind: HISTORY_UPDATE, Id: "0100000000010800", Name: "Known Game", Version: 65536},
		{Kind: HISTORY_GAME, Id: "0100000000030000", Name: "Gone"},
	})
	if len(messages) != 1 || !strings.Contains(messages[0], "Known Game") || strings.Contains(messages[0], "Gone") || strings.Contains(messages[0], "65536") {
		t.Fatalf("only new games and DLC are reported: %v", messages)
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Notifications.NotifyNewGames = false })
	web.notifyNewContent([]HistoryEvent{{Added: true, Kind: HISTORY_GAME, Id: "0100000000010000", Name: "Known Game"}})
	if len(messages) != 1 {
		t.Fatal("nothing is sent when the option is off")
	}
}

func TestLibrarySortsBySizeAndDateAdded(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)
	contents := libraryContents(switchDB, localDB)
	old := time.Now().AddDate(0, 0, -90)
	web.history().record(contents, old)
	// the unknown game left and came back recently
	delete(contents, "game:0100000000030000")
	web.history().record(contents, old)
	contents = libraryContents(switchDB, localDB)
	web.history().record(contents, time.Now())
	web.invalidateDerived()

	filter := defaultFilter()
	filter.SortBy, filter.SortOrder = "added", "desc"
	items, _, facets := web.getLibraryWithFacets(filter, "en")
	if len(items) != 2 || items[0].Id != "0100000000030000" || facets.Recent != 1 {
		t.Fatalf("newest first, one recent game: %+v %+v", items, facets)
	}

	filter.SortBy = "size"
	items, _, _ = web.getLibraryWithFacets(filter, "en")
	if items[0].Size < items[1].Size || items[0].Id != "0100000000010000" {
		t.Fatalf("the game with an update and a DLC is the biggest: %+v", items)
	}

	filter = defaultFilter()
	filter.Extra = EXTRA_RECENT
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) != 1 || items[0].Id != "0100000000030000" {
		t.Fatalf("recently added: %+v", items)
	}
}
