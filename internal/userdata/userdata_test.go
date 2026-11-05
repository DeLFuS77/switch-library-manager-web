package userdata

import (
	"path/filepath"
	"testing"
)

// Each store saves what it is told and reads it again from its file.
func TestStoresReadWhatTheySaved(t *testing.T) {
	folder := t.TempDir()

	collections := LoadCollections(filepath.Join(folder, COLLECTIONS_FILENAME))
	if err := collections.Set([]string{"0100000000010000", "01007ef00011e000"}, "Zelda", true); err != nil {
		t.Fatal(err)
	}
	// a name that only differs in case is the same collection
	if err := collections.Set([]string{"0100000000010000"}, "zelda", false); err != nil {
		t.Fatal(err)
	}
	again := LoadCollections(filepath.Join(folder, COLLECTIONS_FILENAME))
	if names := again.Names(); len(names) != 1 || names[0] != "Zelda" || len(again.Of("0100000000010000")) != 0 || len(again.Of("01007EF00011E000")) != 1 {
		t.Fatalf("collections: %v %v", names, again.Snapshot())
	}

	favorites := NewFavorites(filepath.Join(folder, FAVORITES_FILENAME))
	if !favorites.Set("01007ef00011e000", true) || favorites.Set("01007EF00011E000", true) {
		t.Fatal("a favorite changes once")
	}
	reloaded := NewFavorites(filepath.Join(folder, FAVORITES_FILENAME))
	reloaded.Reload()
	if !reloaded.Has("01007EF00011E000") || len(reloaded.SnapshotTimes()) != 1 {
		t.Fatalf("favorites: %v", reloaded.Snapshot())
	}

	wishlist := LoadWishlist(filepath.Join(folder, WISHLIST_FILENAME))
	wishlist.Set("0100000000010000", true)
	wishlist.Set("01007EF00011E000", true)
	if !wishlist.RemoveOwned(func(id string) bool { return id == "0100000000010000" }) {
		t.Fatal("an owned game leaves the wishlist")
	}
	if list := LoadWishlist(filepath.Join(folder, WISHLIST_FILENAME)); list.Count() != 1 || !list.Has("01007ef00011e000") {
		t.Fatalf("wishlist: %v", list.Ids())
	}

	owned := LoadOwned(filepath.Join(folder, OWNED_FILENAME))
	if changed, err := owned.Set("0400c3f00006e000", true); !changed || err != nil {
		t.Fatalf("a game is marked: %v %v", changed, err)
	}
	if changed, _ := owned.Set("0400C3F00006E000", true); changed {
		t.Fatal("a game is marked once")
	}
	owned.Set("01007EF00011E000", true)
	owned.Set("01007ef00011e000", false)
	if again := LoadOwned(filepath.Join(folder, OWNED_FILENAME)); again.Count() != 1 || !again.Has("0400c3f00006e000") || again.Has("01007EF00011E000") || again.Ids()[0] != "0400C3F00006E000" {
		t.Fatalf("owned: %v", again.Ids())
	}

	play := LoadPlay(filepath.Join(folder, PLAY_FILENAME))
	entry, ok := CleanPlayEntry(PLAY_PLAYING, "4", "  on the second temple ")
	if !ok {
		t.Fatal("a valid entry")
	}
	if err := play.Set("ana", "01007EF00011E000", entry); err != nil {
		t.Fatal(err)
	}
	if got := LoadPlay(filepath.Join(folder, PLAY_FILENAME)).Of("ana", "01007ef00011e000"); got.Status != PLAY_PLAYING || got.Rating != 4 || got.Look().Icon == "" {
		t.Fatalf("play: %+v", got)
	}
	if _, ok := CleanPlayEntry("sleeping", "", ""); ok {
		t.Fatal("an unknown state is refused")
	}
}
