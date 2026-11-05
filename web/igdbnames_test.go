package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// A game is searched on IGDB by its name without the marks and the words of the eShop, then by
// shorter forms of it; a name with nothing to take away is searched as it is.
func TestIgdbSearchNames(t *testing.T) {
	for name, want := range map[string][]string{
		"Super Mario Odyssey":                                {"Super Mario Odyssey"},
		"Demon's Souls":                                      {"Demon's Souls"},
		"LEGO® Voyagers":                                     {"LEGO Voyagers"},
		"WWE 2K25 Standard Edition":                          {"WWE 2K25"},
		"EARTH DEFENSE FORCE5 for Nintendo Switch™ 2":        {"EARTH DEFENSE FORCE5", "EARTH DEFENSE FORCE 5"},
		"Arcade Archives2 AIR COMBAT 22":                     {"Arcade Archives2 AIR COMBAT 22", "Arcade Archives 2 AIR COMBAT 22"},
		"Yooka-Replaylee DEMO":                               {"Yooka-Replaylee"},
		"Little Nightmares III - Free Trial & Friend's Pass": {"Little Nightmares III"},
		"Orbitals: Global Friend's Pass":                     {"Orbitals"},
		"Yakuza Kiwami 3 & Dark Ties (Demo Version)":         {"Yakuza Kiwami 3 & Dark Ties"},
		"KINGDOM HEARTS -HD 1.5+2.5 ReMIX-":                  {"KINGDOM HEARTS HD 1.5+2.5 ReMIX", "KINGDOM HEARTS HD 1.5 + 2.5 ReMIX"},
		"Arcade Archives 2 Mr.Do!'s Castle":                  {"Arcade Archives 2 Mr.Do!'s Castle", "Arcade Archives 2 Mr. Do!'s Castle"},
		"EA SPORTS™ Madden NFL 27":                           {"EA SPORTS Madden NFL 27", "Madden NFL 27"},
		"KINGDOM HEARTS III + Re Mind (DLC)":                 {"KINGDOM HEARTS III + Re Mind", "KINGDOM HEARTS III"},
		"Marvel's Guardians of the Galaxy: Encore Edition":   {"Marvel's Guardians of the Galaxy: Encore Edition", "Marvel's Guardians of the Galaxy"},
		"FATAL FURY: City of the Wolves - STANDARD Edition":  {"FATAL FURY: City of the Wolves", "FATAL FURY"},
		"Diablo® IV: Age of Hatred Collection":               {"Diablo IV: Age of Hatred Collection", "Diablo IV"},
		"Tombi! Special Edition":                             {"Tombi! Special Edition", "Tombi!"},
		"RippleIsland Kyle and Cal’s Restaurant":             {"RippleIsland Kyle and Cal's Restaurant"},
		"SUSHI BAR MOEBIUS for Nintendo Switch™ 2":           {"SUSHI BAR MOEBIUS"},
		"™": {"™"},
	} {
		if got := igdbSearchNames(name); strings.Join(got, " | ") != strings.Join(want, " | ") {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}

// The name of the game itself takes the best match of IGDB; a shorter form takes only a game
// whose name is it or starts with it.
func TestIgdbPick(t *testing.T) {
	names := []string{"Sports Party", "Guardians Arena", "Marvel's Guardians of the Galaxy"}
	if got := igdbPick(names, "MARVEL'S GUARDIANS OF THE GALAXY", false); got != 2 {
		t.Errorf("the same name: %d", got)
	}
	if got := igdbPick(names, "Something Else", true); got != 0 {
		t.Errorf("the name of the game: the best match, %d", got)
	}
	if got := igdbPick(names, "Sports Camp", false); got != -1 {
		t.Errorf("a shorter form takes no other game: %d", got)
	}
	if got := igdbPick([]string{"Drag x Drive: Global Jam Edition"}, "Drag x Drive", false); got != 0 {
		t.Errorf("a name that starts with the shorter form: %d", got)
	}
	if got := igdbPick(nil, "Anything", true); got != -1 {
		t.Errorf("no answer: %d", got)
	}
}

// A game IGDB does not find by its name is found by a shorter form of it, and a shorter form
// that finds another game finds nothing.
func TestIgdbLookupByShorterNames(t *testing.T) {
	var searched []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/token"):
			io.WriteString(w, `{"access_token":"abc","expires_in":3600}`)
		case r.URL.Path == "/v4/games":
			body, _ := io.ReadAll(r.Body)
			query := string(body)
			searched = append(searched, query[strings.Index(query, `"`)+1:strings.Index(query, `";`)])
			switch {
			case strings.Contains(query, `search "Diablo IV";`):
				io.WriteString(w, `[{"name":"Diablo IV","release_dates":[{"date":1749081600,"platform":508}],"cover":{"image_id":"d4"}}]`)
			case strings.Contains(query, `search "Sports Camp";`):
				io.WriteString(w, `[{"name":"Sports Party"}]`)
			default:
				io.WriteString(w, `[]`)
			}
		case r.URL.Path == "/v4/game_time_to_beats":
			io.WriteString(w, `[]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	savedToken, savedApi := igdbTokenUrl, igdbApiUrl
	igdbTokenUrl, igdbApiUrl = server.URL+"/token", server.URL+"/v4/"
	t.Cleanup(func() { igdbTokenUrl, igdbApiUrl = savedToken, savedApi })
	client := newIgdbClient("id", "secret")

	found, err := client.lookupTitleOn(context.Background(), "Diablo® IV: Age of Hatred Collection", igdbSwitch2Platform, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !found.Found || found.ReleaseDate != 20250605 || !strings.HasSuffix(found.Cover, "/d4.jpg") ||
		strings.Join(searched, " | ") != "Diablo IV: Age of Hatred Collection | Diablo IV" {
		t.Fatalf("found by a shorter name: %+v, searched %q", found, searched)
	}

	searched = nil
	found, err = client.lookupTitleOn(context.Background(), "Sports Camp: A 35-Game Adventure", igdbSwitch2Platform, time.Now())
	if err != nil || found.Found || found.Version != igdbTitleVersion {
		t.Fatalf("another game is not taken: %+v %v, searched %q", found, err, searched)
	}

	// the times to beat search the same names
	searched = nil
	if _, err := client.lookupOn(context.Background(), "Diablo® IV: Age of Hatred Collection", igdbSwitch2Platform, time.Now()); err != nil ||
		strings.Join(searched, " | ") != "Diablo IV: Age of Hatred Collection | Diablo IV" {
		t.Fatalf("the times to beat: %v, searched %q", err, searched)
	}
}

// A game Wikidata links to its game on IGDB is asked by that game first; a game of the Switch 2
// IGDB lists on the Switch only is found there by the same name, without a date of the Switch 2,
// and a game of the Switch is not searched on another console.
func TestIgdbFindBySlugAndOnTheSwitch(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/token"):
			io.WriteString(w, `{"access_token":"abc","expires_in":3600}`)
		case r.URL.Path == "/v4/games":
			body, _ := io.ReadAll(r.Body)
			query := string(body)
			asked = append(asked, query[:strings.Index(query, ";")]+strings.TrimSuffix(query[strings.LastIndex(query, "where"):], "limit 5;"))
			switch {
			case strings.Contains(query, `where slug = "shine-post-be-your-idol"`):
				io.WriteString(w, `[{"name":"Shine Post: Be Your Idol!","cover":{"image_id":"sp"}}]`)
			case strings.Contains(query, `search "Gecko Gods"`) && strings.Contains(query, "platforms = (130)"):
				io.WriteString(w, `[{"name":"Gecko Gods","release_dates":[{"date":1749081600,"platform":130}],"cover":{"image_id":"gg"}},{"name":"Gecko Gods 2"}]`)
			case strings.Contains(query, `search "Gecko"`):
				io.WriteString(w, `[{"name":"Gecko Racers"}]`)
			default:
				io.WriteString(w, `[]`)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	savedToken, savedApi := igdbTokenUrl, igdbApiUrl
	igdbTokenUrl, igdbApiUrl = server.URL+"/token", server.URL+"/v4/"
	t.Cleanup(func() { igdbTokenUrl, igdbApiUrl = savedToken, savedApi })
	client := newIgdbClient("id", "secret")

	found, err := client.lookupTitleFor(context.Background(), "シャインポスト Be Your アイドル！", "shine-post-be-your-idol", igdbSwitch2Platform, time.Now())
	if err != nil || !found.Found || !strings.HasSuffix(found.Cover, "/sp.jpg") || len(asked) != 1 {
		t.Fatalf("by the game of Wikidata: %+v %v, asked %q", found, err, asked)
	}

	asked = nil
	found, err = client.lookupTitleFor(context.Background(), "Gecko Gods", "", igdbSwitch2Platform, time.Now())
	if err != nil || !found.Found || !strings.HasSuffix(found.Cover, "/gg.jpg") || found.ReleaseDate != 0 {
		t.Fatalf("on the Switch, without a date of the Switch 2: %+v %v, asked %q", found, err, asked)
	}

	// a game of the Switch is searched on the Switch only
	asked = nil
	if found, err = client.lookupTitleOn(context.Background(), "Sniper Dan", igdbSwitchPlatform, time.Now()); err != nil || found.Found || len(asked) != 1 {
		t.Fatalf("a game of the Switch: %+v %v, asked %q", found, err, asked)
	}
}

// The list of the Switch 2 gives the date of Wikidata, until IGDB tells its own, and the games of
// IGDB it links the games to.
func TestSwitch2ListDateAndSlug(t *testing.T) {
	if switch2Date("2025-06-05") != 20250605 || switch2Date("") != 0 || switch2Date("2025") != 0 {
		t.Fatal("the dates of the list")
	}
	web := newTestWeb(t)
	list := `{"titles":{"0400C3F00006E000":{"name":"Mario Kart World","regions":["US"],"igdb":"mario-kart-world","date":"2025-06-05"},` +
		`"0400000000010000":{"name":"No Link"}}}`
	if err := os.WriteFile(filepath.Join(web.dataFolder, SWITCH2_JSON_FILENAME), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	slugs := web.switch2Slugs()
	if len(slugs) != 1 || slugs["0400c3f00006e"] != "mario-kart-world" {
		t.Fatalf("the slugs: %v", slugs)
	}
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}, Versions: map[string]map[int]string{}}
	web.applySwitch2Titles(switchDB)
	for _, title := range switchDB.TitlesMap {
		if title.Attributes.Name == "Mario Kart World" && title.Attributes.ReleaseDate != 20250605 {
			t.Fatalf("the date of Wikidata: %+v", title.Attributes)
		}
		if title.Attributes.Name == "No Link" && title.Attributes.ReleaseDate != 0 {
			t.Fatalf("no date: %+v", title.Attributes)
		}
	}
}
