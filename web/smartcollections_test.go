package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A smart collection keeps filters, not games: it is found again each time, with its count.
func TestSmartCollections(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)
	web.HandleSmartCollections()
	id := "0100000000010000"

	if response := postForm(web, "/smart-collections", url.Values{"name": {"Playing now"}, "query": {"?page=3&per_page=48"}}); response.Code != http.StatusBadRequest {
		t.Fatalf("no filter, nothing to keep: %d", response.Code)
	}
	if response := postForm(web, "/smart-collections", url.Values{"name": {"  Playing   now "}, "query": {"?play=playing&page=3&per_page=48&evil=1"}}); response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	saved := web.smartCollections().list()
	if len(saved) != 1 || saved[0].Name != "Playing now" || strings.Contains(saved[0].Query, "page") || strings.Contains(saved[0].Query, "evil") || !strings.Contains(saved[0].Query, "play=playing") {
		t.Fatalf("kept clean: %+v", saved)
	}

	filter := defaultFilter()
	if views := web.smartCollectionViews(filter, "en"); len(views) != 1 || views[0].Count != 0 || views[0].Active {
		t.Fatalf("nothing played yet: %+v", views)
	}
	// it fills by itself
	web.play().set("", id, PlayEntry{Status: PLAY_PLAYING})
	filter.Play = PLAY_PLAYING
	// the same filters as the ones kept (All, not My games)
	filter.WithMissing = ""
	views := web.smartCollectionViews(filter, "en")
	if len(views) != 1 || views[0].Count != 1 || !views[0].Active || !strings.HasPrefix(views[0].Href, "/index.html?") {
		t.Fatalf("its game now, and shown: %+v", views)
	}
	// each user sees their own games in it
	filter.player = "alice"
	if views := web.smartCollectionViews(filter, "en"); views[0].Count != 0 {
		t.Fatalf("alice plays nothing: %+v", views)
	}

	// the same name replaces it; deleting forgets it
	postForm(web, "/smart-collections", url.Values{"name": {"playing NOW"}, "query": {"?status=update"}})
	if saved := web.smartCollections().list(); len(saved) != 1 || !strings.Contains(saved[0].Query, "status=update") {
		t.Fatalf("replaced: %+v", saved)
	}
	postForm(web, "/smart-collections", url.Values{"name": {"Playing now"}, "action": {"delete"}})
	if saved := web.smartCollections().list(); len(saved) != 0 {
		t.Fatalf("deleted: %+v", saved)
	}
}
