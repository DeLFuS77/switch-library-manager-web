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
	"github.com/dtrunk90/switch-library-manager-web/settings"
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

// The release date is the one of the console searched, not the first one on any platform: a game
// of the Switch 2 that came out on the PC years before gets the day it came out on the Switch 2,
// and none when IGDB gives a date of another platform only. A game found before this asks again.
func TestIgdbReleaseDateOfTheConsole(t *testing.T) {
	answer := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/token"):
			io.WriteString(w, `{"access_token":"abc","expires_in":3600}`)
		case r.URL.Path == "/v4/games":
			io.WriteString(w, answer)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	savedToken, savedApi := igdbTokenUrl, igdbApiUrl
	igdbTokenUrl, igdbApiUrl = server.URL+"/token", server.URL+"/v4/"
	t.Cleanup(func() { igdbTokenUrl, igdbApiUrl = savedToken, savedApi })
	client := newIgdbClient("id", "secret")

	// 25/03/2013 on the PC, 05/06/2025 and 12/06/2025 on the Switch 2
	answer = `[{"name":"Old Shooter","first_release_date":1364169600,"release_dates":[{"date":1364169600,"platform":6},
		{"date":1749686400,"platform":508},{"date":1749081600,"platform":508},{"platform":508}]}]`
	found, err := client.lookupTitleOn(context.Background(), "Old Shooter", igdbSwitch2Platform, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !found.Found || found.ReleaseDate != 20250605 || found.Version != igdbTitleVersion {
		t.Fatalf("the date of the Switch 2: %+v", found)
	}

	// a date of the PC only: none
	answer = `[{"name":"Old Shooter","first_release_date":1364169600,"release_dates":[{"date":1364169600,"platform":6},{"platform":508}]}]`
	if found, err = client.lookupTitleOn(context.Background(), "Old Shooter", igdbSwitch2Platform, time.Now()); err != nil || found.ReleaseDate != 0 {
		t.Fatalf("a date of another platform only: %+v %v", found, err)
	}

	// a game of the Switch without a date of the Switch keeps the first one, as before
	answer = `[{"name":"Old Shooter","first_release_date":1364169600,"release_dates":[{"date":1364169600,"platform":6}]}]`
	if found, err = client.lookupTitleOn(context.Background(), "Old Shooter", igdbSwitchPlatform, time.Now()); err != nil || found.ReleaseDate != 20130325 {
		t.Fatalf("a game of the Switch without a date of the Switch: %+v %v", found, err)
	}

	// no dates by platform: the first one
	answer = `[{"name":"Old Shooter","first_release_date":1364169600}]`
	if found, err = client.lookupTitleOn(context.Background(), "Old Shooter", igdbSwitch2Platform, time.Now()); err != nil || found.ReleaseDate != 20130325 {
		t.Fatalf("no dates by platform: %+v %v", found, err)
	}

	store := &igdbTitleStore{games: map[string]IgdbTitle{}}
	store.set("0400000000010", IgdbTitle{Found: true, ReleaseDate: 20130325, Checked: time.Now()})
	store.set("0400000000020", IgdbTitle{Found: true, ReleaseDate: 20250605, Version: igdbTitleVersion, Checked: time.Now()})
	if !store.needsLookup("0400000000010", "A", time.Now()) || store.needsLookup("0400000000020", "B", time.Now()) {
		t.Fatal("a game found by an older version is asked again, one found now is not")
	}

	// a game not found is asked again when the name it is searched by changes, not before
	store.set("0400000000030", IgdbTitle{Found: false, Version: igdbTitleVersion, Checked: time.Now(), Name: "NintendoSwitchSportsResortv1.1.0"})
	if store.needsLookup("0400000000030", "NintendoSwitchSportsResortv1.1.0", time.Now()) {
		t.Error("a game not found was asked again by the same name before a month")
	}
	if !store.needsLookup("0400000000030", "Nintendo Switch Sports Resort", time.Now()) {
		t.Error("a game not found was not asked again by its new name")
	}
	// a game found keeps what was found, whatever its name now
	if store.needsLookup("0400000000020", "Another Name", time.Now()) {
		t.Error("a game found was asked again for a new name")
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
	web.igdbTitles().set("0100000000020", IgdbTitle{Found: true, Version: igdbTitleVersion, Genres: []string{"RPG"}, ReleaseDate: 20210101, Publisher: "Seller", Summary: "A quest.", Cover: "https://images.igdb.com/x.jpg"})

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

// The titles made again from what IGDB told are made of the databases as they are then, under
// the lock: a library replaced meanwhile is never written over with the one of before, and
// nothing changes while a synchronization runs.
func TestTitlesAreMadeOfTheCurrentLibrary(t *testing.T) {
	web := newTestWeb(t)
	switchDB, before := testDatabases(t)
	web.state.set(switchDB, before)
	// covers or a scan gave a new library after the lookups started
	after := *before
	web.state.set(switchDB, &after)
	if !web.state.update(func(s *db.SwitchTitlesDB, l *db.LocalSwitchFilesDB) *db.SwitchTitlesDB {
		if l != &after {
			t.Error("made of the library of before")
		}
		return s
	}) {
		t.Fatal("not updated")
	}
	if _, library := web.state.get(); library != &after {
		t.Fatal("the new library stays")
	}
	web.state.startSync()
	defer web.state.endSync()
	if web.state.update(func(s *db.SwitchTitlesDB, l *db.LocalSwitchFilesDB) *db.SwitchTitlesDB { return nil }) {
		t.Fatal("nothing changes during a synchronization")
	}
}

// A language of titles asked for while a synchronization runs is added once it ends, as it may
// have loaded the languages before.
func TestTitleLanguageAskedDuringASyncIsAddedAfter(t *testing.T) {
	web := newTestWeb(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"0100000000010000":{"name":"Un jeu"}}`)
	}))
	defer server.Close()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.LocalizedTitlesJsonUrl = server.URL + "/titles.%s.json" })
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, nil)
	web.state.startSync()
	web.addTitleLanguage("fr")
	web.state.endSync()
	if switchDB, _ := web.state.get(); switchDB.Localized["fr"] != nil {
		t.Fatal("not added during the synchronization")
	}
	web.addPendingTitleLanguages()
	if switchDB, _ := web.state.get(); switchDB.Localized["fr"] == nil {
		t.Fatal("added once it ended")
	}
}
