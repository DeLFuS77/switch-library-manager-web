package web

import (
	"net/http"
	"net/url"
	"testing"
)

func TestCollections(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	web.HandleCollections()

	response := postForm(web, "/collections", url.Values{"id": {"0100000000010000", "0100000000030000"}, "name": {"  For   the kids "}})
	if response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body.String())
	}
	if names := web.collections().of("0100000000010000"); len(names) != 1 || names[0] != "For the kids" {
		t.Fatalf("names are cleaned: %v", names)
	}
	// the same collection, written in another case
	postForm(web, "/collections", url.Values{"id": {"0100000000010000"}, "name": {"for the KIDS"}})
	postForm(web, "/collections", url.Values{"id": {"0100000000010000"}, "name": {"Playing"}})
	if names := web.collections().of("0100000000010000"); len(names) != 2 || names[0] != "For the kids" || names[1] != "Playing" {
		t.Fatalf("one entry per collection, sorted: %v", names)
	}

	filter := defaultFilter()
	filter.Collection = "Playing"
	items, _, facets := web.getLibraryWithFacets(filter, "en")
	if len(items) != 1 || items[0].Id != "0100000000010000" || len(facets.Collections) != 2 || facets.Collections[0].Count != 2 {
		t.Fatalf("filter by collection: %+v %+v", items, facets.Collections)
	}

	postForm(web, "/collections", url.Values{"id": {"0100000000030000"}, "name": {"For the kids"}, "action": {"remove"}})
	if names := web.collections().of("0100000000030000"); len(names) != 0 {
		t.Fatalf("removed: %v", names)
	}

	for _, values := range []url.Values{
		{"id": {"0100000000010000"}, "name": {"   "}},
		{"id": {"nope"}, "name": {"Playing"}},
		{"name": {"Playing"}},
	} {
		if response := postForm(web, "/collections", values); response.Code != http.StatusBadRequest {
			t.Errorf("%v must be refused: %d", values, response.Code)
		}
	}

	web.collections().reload()
	if len(web.collections().names()) != 2 {
		t.Fatal("the collections are saved")
	}
}

func TestFilterCountsFollowTheCollection(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	web.collections().set([]string{"0100000000010000"}, "Playing", true)
	web.invalidateDerived()
	filter := defaultFilter()
	filter.Collection = "Playing"
	_, _, facets := web.getLibraryWithFacets(filter, "en")
	if facets.Games+facets.Demos != 1 || facets.Unknown != 0 || len(facets.Collections) != 1 {
		t.Fatalf("the counts of the panel are those of the collection: %+v", facets)
	}
	if filter.SecondaryCount() != 1 {
		t.Fatal("one filter besides the status")
	}
}

func TestFavorites(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	// a collection named like the favorites becomes the stars
	web.collections().set([]string{"0100000000030000"}, "Favoritos", true)
	if !web.favorites().has("0100000000030000") || len(web.collections().names()) != 0 {
		t.Fatal("the collection Favoritos becomes the favorites")
	}

	web.HandleFavorites()
	if response := postForm(web, "/favorites", url.Values{"id": {"0100000000010000"}, "favorite": {"true"}}); response.Code != http.StatusOK {
		t.Fatalf("favorite: %d", response.Code)
	}
	filter := defaultFilter()
	filter.Extra = EXTRA_FAVORITES
	items, _, facets := web.getLibraryWithFacets(filter, "en")
	if len(items) != 2 || facets.Favorites != 2 || !items[0].Favorite {
		t.Fatalf("favorites only: %+v %+v", items, facets)
	}
	if response := postForm(web, "/favorites", url.Values{"id": {"nope"}, "favorite": {"true"}}); response.Code != http.StatusBadRequest {
		t.Fatal("invalid IDs are refused")
	}

	// the missing games show with the others when asked
	filter = defaultFilter()
	filter.WithMissing = "1"
	items, _, facets = web.getLibraryWithFacets(filter, "en")
	if facets.Missing != 1 || len(items) != 3 {
		t.Fatalf("one missing game with the two of the library: %+v", facets)
	}
	filter.Status = STATUS_UPDATE
	if items, _, _ := web.getLibraryWithFacets(filter, "en"); len(items) != 1 || items[0].Missing {
		t.Fatalf("missing games have no status: %+v", items)
	}
}
