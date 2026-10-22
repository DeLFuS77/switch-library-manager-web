package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// IGDB tells the genres (in the names of the app), release date, publisher, developer,
// description and cover of a game.
func TestIgdbLookupOfATitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/token"):
			io.WriteString(w, `{"access_token":"abc","expires_in":3600}`)
		case r.URL.Path == "/v4/games":
			io.WriteString(w, `[{"name":"Other Game"},{"name":"Lost Quest","first_release_date":1609459200,"summary":" A quest. ",
				"genres":[{"name":"Role-playing (RPG)"},{"name":"Indie"},{"name":"Platform"}],"themes":[{"name":"Action"}],
				"cover":{"image_id":"co1abc"},"involved_companies":[{"company":{"name":"Maker"},"developer":true},{"company":{"name":"Seller"},"publisher":true}]}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	savedToken, savedApi := igdbTokenUrl, igdbApiUrl
	igdbTokenUrl, igdbApiUrl = server.URL+"/token", server.URL+"/v4/"
	t.Cleanup(func() { igdbTokenUrl, igdbApiUrl = savedToken, savedApi })

	found, err := newIgdbClient("id", "secret").lookupTitle(context.Background(), "Lost Quest", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !found.Found || found.ReleaseDate != 20210101 || found.Summary != "A quest." || found.Publisher != "Seller" || found.Developer != "Maker" ||
		strings.Join(found.Genres, ",") != "RPG,Platformer,Action" || !strings.HasSuffix(found.Cover, "/t_cover_big/co1abc.jpg") {
		t.Fatalf("what IGDB told: %+v", found)
	}
}

// A game of the library the titles database does not list gets a title with the versions
// published, the name and publisher of its files and what IGDB told; the database is left as
// it is, a known game keeps its own title, and a game gone takes its title away.
func TestLibraryTitlesForUnknownGames(t *testing.T) {
	web := newTestWeb(t)
	known := &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000010000", Name: "Known Game"}}
	switchDB := &db.SwitchTitlesDB{
		TitlesMap: map[string]*db.SwitchTitle{"0100000000010": known},
		Versions:  map[string]map[int]string{"0100000000020": {65536: "2024-05-01"}},
	}
	game := func(id string, name string, publisher string) *db.SwitchGameFiles {
		return &db.SwitchGameFiles{BaseExist: true, File: db.SwitchFileInfo{Metadata: &switchfs.ContentMetaAttributes{TitleId: id,
			Ncap: &switchfs.Nacp{TitleName: map[string]switchfs.NacpTitle{"AmericanEnglish": {Title: name, Publisher: publisher}}}}}}
	}
	localDB := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": game("0100000000010000", "Known Game", "Known Publisher"),
		"0100000000020": game("0100000000020000", "Lost Quest", "Small Studio"),
	}}
	web.igdbTitles().set("0100000000020", IgdbTitle{Found: true, Genres: []string{"RPG"}, ReleaseDate: 20210101, Publisher: "Seller", Summary: "A quest.", Cover: "https://images.igdb.com/x.jpg"})

	enriched := web.withLibraryTitles(switchDB, localDB)
	lost := enriched.TitlesMap["0100000000020"]
	if lost == nil || !lost.FromLibrary || lost.Attributes.Name != "Lost Quest" || lost.Attributes.Publisher != "Small Studio" ||
		lost.Updates[65536] == "" || lost.Attributes.ReleaseDate != 20210101 || len(lost.Attributes.Genres) != 1 ||
		lost.Attributes.Description != "A quest." || lost.Attributes.IconUrl != "https://images.igdb.com/x.jpg" || lost.Attributes.Id != "0100000000020000" {
		t.Fatalf("the title of the unknown game: %+v", lost)
	}
	if enriched.TitlesMap["0100000000010"] != known || len(switchDB.TitlesMap) != 1 {
		t.Fatal("the known game and the database are left as they are")
	}

	// made again from the enriched database: the same, and gone with its game
	again := web.withLibraryTitles(enriched, localDB)
	if again.TitlesMap["0100000000020"] == nil || again.TitlesMap["0100000000020"].Attributes.Name != "Lost Quest" {
		t.Fatal("made again from a database already enriched")
	}
	delete(localDB.TitlesMap, "0100000000020")
	if gone := web.withLibraryTitles(enriched, localDB); gone.TitlesMap["0100000000020"] != nil {
		t.Fatal("a game gone takes its title away")
	}

	// the names to ask IGDB: the games with a title of their own not asked yet
	web.state.set(enriched, localDB)
	if pending := web.libraryTitlesToLookUp(time.Now()); len(pending) != 0 {
		t.Fatalf("a game already found is not asked again: %v", pending)
	}
	web.igdbTitles().set("0100000000020", IgdbTitle{Checked: time.Now().Add(-2 * igdbTitleRetry)})
	if pending := web.libraryTitlesToLookUp(time.Now()); pending["0100000000020"] != "Lost Quest" {
		t.Fatalf("a game not found long ago is asked again: %v", pending)
	}
}
