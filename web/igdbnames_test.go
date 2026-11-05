package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
		"KINGDOM HEARTS -HD 1.5+2.5 ReMIX-":                  {"KINGDOM HEARTS HD 1.5+2.5 ReMIX"},
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
