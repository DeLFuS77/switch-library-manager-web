package web

import (
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

func TestRecommendationsFollowTheFavorites(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	game := func(id, name string, release int, genres ...string) *db.SwitchTitle {
		return &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: name, ReleaseDate: release, Genres: genres, IconUrl: "https://example.com/" + id + ".jpg"}}
	}
	known := *switchDB.TitlesMap["0100000000010"]
	known.Attributes.Genres = []string{"RPG", "Adventure"}
	switchDB.TitlesMap["0100000000010"] = &known
	switchDB.TitlesMap["0100000000020"] = game("0100000000020000", "Known Game 2", 20200101, "Racing")
	switchDB.TitlesMap["0100000000050"] = game("0100000000050000", "Another RPG", 20220101, "RPG")
	switchDB.TitlesMap["0100000000060"] = game("0100000000060000", "A Racing Game", 20220101, "Racing")
	switchDB.TitlesMap["0100000000070"] = game("0100000000070000", "Future RPG", 20990101, "RPG")
	// the game in another store, and an app of the game
	switchDB.TitlesMap["0100000000080"] = game("0100000000080000", "Known Game（ノウン）", 20220101, "RPG")
	switchDB.TitlesMap["0100000000090"] = game("0100000000090000", "Known Game - Media Review", 20220101, "RPG")
	web.state.set(switchDB, localDB)

	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	ids := func() []string {
		result := []string{}
		for _, r := range web.recommendationsAt("en", now) {
			result = append(result, r.Item.Id)
		}
		return result
	}
	// without favorites, the library: the RPG, then the sequel; not the future game
	got := ids()
	if len(got) != 2 || got[0] != "0100000000050000" || got[1] != "0100000000020000" {
		t.Fatalf("from the library: %v", got)
	}
	web.favorites().set("0100000000010000", true)
	recommendations := web.recommendationsAt("en", now)
	if len(recommendations) != 2 || recommendations[0].Saga != "Known Game" || recommendations[1].Genre != "RPG" {
		t.Fatalf("from the favorites: %+v", recommendations)
	}
	web.wishes().set("0100000000050000", true)
	if got := ids(); len(got) != 1 {
		t.Fatalf("the wishlist is not recommended: %v", got)
	}
}
