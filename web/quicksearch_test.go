package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func quickLabels(results []QuickResult) []string {
	labels := []string{}
	for _, result := range results {
		labels = append(labels, result.Label)
	}
	return labels
}

func TestQuickSearchFindsGamesSeriesAndPages(t *testing.T) {
	web := demoWeb(t)

	results := web.quickSearch("kart", "en", true)
	if len(results.Games) == 0 || results.Games[0].Label != "Pixel Kart Rally" || !strings.HasPrefix(results.Games[0].Href, "/title/") {
		t.Fatalf("games: %+v", results.Games)
	}
	if len(results.Series) != 1 || !strings.HasPrefix(results.Series[0].Href, "/sagas.html?q=") {
		t.Fatalf("series: %+v", results.Series)
	}

	// typing mistakes and accents are forgiven, like in the lists
	if got := quickLabels(web.quickSearch("bubble bistr", "en", true).Games); len(got) != 1 || got[0] != "Bubble Bistro" {
		t.Fatalf("a typo: %v", got)
	}

	// pages by their translated name, their English name or other words
	if got := quickLabels(web.quickSearch("estadisticas", "es", true).Pages); len(got) != 1 || got[0] != "Estadísticas" {
		t.Fatalf("translated page: %v", got)
	}
	if got := web.quickSearch("telegram", "en", true).Pages; len(got) != 1 || got[0].Href != "/settings.html#notifications" {
		t.Fatalf("a section of Settings by another word: %+v", got)
	}
}

func TestQuickSearchHidesAdministratorPages(t *testing.T) {
	web := demoWeb(t)
	if got := web.quickSearch("settings", "en", false).Pages; len(got) != 0 {
		t.Fatalf("a viewer does not get the settings: %+v", got)
	}
	if got := web.quickSearch("statistics", "en", false).Pages; len(got) != 1 {
		t.Fatalf("a viewer gets the other pages: %+v", got)
	}
	// nothing typed: suggested pages only
	for _, page := range web.quickSearch("", "en", false).Pages {
		if page.Href == "/settings.html#library" {
			t.Fatal("no settings for a viewer among the suggestions")
		}
	}
	if results := web.quickSearch("", "en", true); len(results.Pages) == 0 || len(results.Games) != 0 {
		t.Fatalf("nothing typed: %+v", results)
	}
}

func TestQuickSearchTitleIdIsExact(t *testing.T) {
	web := demoWeb(t)
	id := demoTitleId(0)
	if got := web.quickSearch(id, "en", true).Games; len(got) != 1 || !strings.Contains(got[0].Href, id) {
		t.Fatalf("the game of the ID: %+v", got)
	}
	if got := web.quickSearch("0100FFFFFFFF0000", "en", true).Games; len(got) != 0 {
		t.Fatalf("an unknown ID finds nothing: %+v", got)
	}
}

func TestQuickSearchEndpoint(t *testing.T) {
	web := demoWeb(t)
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/search?q="+strings.Repeat("a", 500), nil))
	var results QuickResults
	if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &results) != nil || results.Games == nil || results.Pages == nil {
		t.Fatalf("got %d %s", recorder.Code, recorder.Body.String())
	}
}
