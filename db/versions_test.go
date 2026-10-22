package db

import (
	"strings"
	"testing"
)

// The versions Nintendo published reach a game titles.json lists without its update, and the
// versions of the games it does not list are kept apart, also in the store.
func TestVersionsOfGamesTitlesDoesNotList(t *testing.T) {
	titles := `{"0100000000010000":{"id":"0100000000010000","name":"Known"}}`
	versions := `{"0100000000010000":{"65536":"2024-01-01"},"0100000000020000":{"65536":"2024-05-01","131072":"2024-06-01"}}`
	result, err := CreateSwitchTitleDB(strings.NewReader(titles), strings.NewReader(versions))
	if err != nil {
		t.Fatal(err)
	}
	if result.TitlesMap["0100000000010"].Updates[65536] != "2024-01-01" {
		t.Fatalf("the versions of a listed game without its update entry: %+v", result.TitlesMap["0100000000010"])
	}
	if _, listed := result.TitlesMap["0100000000020"]; listed || len(result.Versions["0100000000020"]) != 2 {
		t.Fatalf("an unlisted game is kept apart, not listed: %v", result.Versions)
	}

	store, err := OpenTitleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveTitles("stamp", result, nil); err != nil {
		t.Fatal(err)
	}
	loaded, ok := store.LoadTitles("stamp")
	if !ok || len(loaded.Versions["0100000000020"]) != 2 || loaded.TitlesMap["0100000000010"] == nil {
		t.Fatalf("the store keeps the versions apart: %+v", loaded)
	}
}
