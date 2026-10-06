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
	postForm(web, "/collections", url.Values{"id": {"0100000000010000"}, "name": {"Favorites"}})
	if names := web.collections().of("0100000000010000"); len(names) != 2 || names[0] != "Favorites" || names[1] != "For the kids" {
		t.Fatalf("one entry per collection, sorted: %v", names)
	}

	filter := defaultFilter()
	filter.Collection = "Favorites"
	items, _, facets := web.getLibraryWithFacets(filter, "en")
	if len(items) != 1 || items[0].Id != "0100000000010000" || len(facets.Collections) != 2 || facets.Collections[1].Count != 2 {
		t.Fatalf("filter by collection: %+v %+v", items, facets.Collections)
	}

	postForm(web, "/collections", url.Values{"id": {"0100000000030000"}, "name": {"For the kids"}, "action": {"remove"}})
	if names := web.collections().of("0100000000030000"); len(names) != 0 {
		t.Fatalf("removed: %v", names)
	}

	for _, values := range []url.Values{
		{"id": {"0100000000010000"}, "name": {"   "}},
		{"id": {"nope"}, "name": {"Favorites"}},
		{"name": {"Favorites"}},
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
	web.collections().set([]string{"0100000000010000"}, "Favorites", true)
	web.invalidateDerived()
	filter := defaultFilter()
	filter.Collection = "Favorites"
	_, _, facets := web.getLibraryWithFacets(filter, "en")
	if facets.Games+facets.Demos != 1 || facets.Unknown != 0 || len(facets.Collections) != 1 {
		t.Fatalf("the counts of the panel are those of the collection: %+v", facets)
	}
	if filter.SecondaryCount() != 1 {
		t.Fatal("one filter besides the status")
	}
}
