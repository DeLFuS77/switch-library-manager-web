package web

import "testing"

func TestOrphansAreGroupedByGame(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))

	// testDatabases has a game with only its update in the library ("0100000000040")
	items := web.buildOrphans("en")
	if len(items) != 1 || items[0].Id != "0100000000040000" || items[0].OrphanUpdates != 1 || items[0].OrphanDlc != 0 || items[0].Name == "" {
		t.Fatalf("one game with an update and without its base: %+v", items)
	}
	if web.orphanCount() != 1 {
		t.Fatalf("count: %d", web.orphanCount())
	}

	web.wishes().set("0100000000040000", true)
	if items := web.buildOrphans("en"); !items[0].Wished {
		t.Fatal("the wishlist is respected")
	}

	// the same files are the issues "base file is missing"
	missing := 0
	for _, issue := range web.getIssues() {
		if issue.Reason == "base file is missing" {
			missing++
		}
	}
	if missing != items[0].OrphanUpdates+items[0].OrphanDlc {
		t.Fatalf("the issues and the groups count the same files: %d", missing)
	}
}
