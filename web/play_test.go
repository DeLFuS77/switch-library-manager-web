package web

import (
	"context"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// The state of a game is kept for each user: the card shows it, the library filters by it with
// its counts, and the game page sets it.
func TestPlayStateOfEachUser(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)
	web.HandlePlay()
	web.HandleTitle()
	id := "0100000000010000"

	if response := postForm(web, "/play", url.Values{"id": {id}, "status": {userdata.PLAY_PLAYING}, "rating": {"4"}, "note": {" Chapter 3 "}}); response.Code != http.StatusOK {
		t.Fatalf("set: %d %s", response.Code, response.Body.String())
	}
	if entry := web.play().Of("", id); entry.Status != userdata.PLAY_PLAYING || entry.Rating != 4 || entry.Note != "Chapter 3" {
		t.Fatalf("kept: %+v", entry)
	}
	for _, bad := range []url.Values{{"id": {id}, "status": {"won"}}, {"id": {id}, "rating": {"6"}}, {"id": {"x"}, "status": {userdata.PLAY_PLAYING}}} {
		if response := postForm(web, "/play", bad); response.Code != http.StatusBadRequest {
			t.Errorf("%v is refused: %d", bad, response.Code)
		}
	}
	// another user has their own
	web.play().Set("alice", id, userdata.PlayEntry{Status: userdata.PLAY_FINISHED})
	if web.play().Of("alice", id).Status != userdata.PLAY_FINISHED || web.play().Of("", id).Status != userdata.PLAY_PLAYING {
		t.Fatal("each user has their own")
	}

	filter := defaultFilter()
	filter.Play = userdata.PLAY_PLAYING
	items, _, facets := web.getLibraryWithFacets(filter, "en")
	if len(items) != 1 || items[0].Id != id || items[0].Play.Status != userdata.PLAY_PLAYING || facets.Play[userdata.PLAY_PLAYING] != 1 || facets.Play["none"] == 0 {
		t.Fatalf("the filter and its counts: %+v %v", items, facets.Play)
	}
	filter.player = "alice"
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) != 0 {
		t.Fatal("alice is not playing it")
	}
	filter.Play = "none"
	filter.player = ""
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) == 0 || items[0].Id == id {
		t.Fatalf("the games without a state: %+v", items)
	}

	page := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/title/"+id+".html", nil)
	web.router.ServeHTTP(page, request.WithContext(context.Background()))
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, `data-play="`+id+`"`) || !strings.Contains(body, `id="play-playing" value="playing" checked`) || !strings.Contains(body, "Chapter 3") {
		t.Fatalf("the game page shows the state: %d", page.Code)
	}

	// nothing left: forgotten
	postForm(web, "/play", url.Values{"id": {id}})
	if _, ok := web.play().All("")[id]; ok {
		t.Fatal("an empty state is forgotten")
	}
}
