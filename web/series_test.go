package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

func seriesTestWeb(t *testing.T) *Web {
	t.Helper()
	web := newTestWeb(t)
	title := func(id, name, publisher string) *db.SwitchTitle {
		return &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: name, Publisher: publisher, ReleaseDate: 20200101}}
	}
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		// IGDB: one series, two publishers
		"0100000000010": title("0100000000010000", "Star Odyssey", "Stellar Games"),
		"0100000000020": title("0100000000020000", "Star Odyssey II", "Other House"),
		// Wikidata: by title ID and by name; an edition without data joins its series
		"0100000000030": title("0100000000030000", "Hero Saga: First Tale", "Saga Works"),
		"0100000000040": title("0100000000040000", "Hero Saga: Second Tale", "Saga Works"),
		"0100000000050": title("0100000000050000", "Hero Saga: Third Tale Deluxe", "Saga Works"),
		// a demo is no game of the series
		"0100000000080": title("0100000000080000", "Hero Saga: Third Tale Demo", "Saga Works"),
		// no data: guessed by name and publisher, as before
		"0100000000060": title("0100000000060000", "Moon Runner", "Lunar"),
		"0100000000070": title("0100000000070000", "Moon Runner 2", "Lunar"),
	}}
	owned := func(id string) *db.SwitchGameFiles {
		return &db.SwitchGameFiles{BaseExist: true, File: db.SwitchFileInfo{Metadata: &switchfs.ContentMetaAttributes{TitleId: id}},
			Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	}
	localDB := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": owned("0100000000010000"), "0100000000030": owned("0100000000030000"), "0100000000060": owned("0100000000060000"),
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}
	web.state.set(switchDB, localDB)

	series, _ := json.Marshal(wikidataSeries{
		Series: map[string]map[string]string{"Q1": {"en": "Hero Saga", "es": "La saga del héroe"}},
		Ids:    map[string]string{"0100000000030000": "Q1"},
		Names:  map[string]string{"Hero Saga: Second Tale": "Q1"},
	})
	os.WriteFile(filepath.Join(web.dataFolder, SERIES_JSON_FILENAME), series, 0644)
	web.timesToBeat().set("0100000000010000", TimeToBeat{Found: true, Checked: time.Now(), SeriesChecked: true, Collections: []int{77}})
	web.igdbCollections().set(77, igdbCollection{Name: "Star Odyssey", Members: []string{"Star Odyssey", "Star Odyssey II"}, Checked: time.Now()})
	web.invalidateDerived()
	return web
}

func TestSeriesFromIgdbWikidataAndNames(t *testing.T) {
	web := seriesTestWeb(t)
	sagas := map[string]SagaProgress{}
	for _, saga := range web.sagaProgress("es") {
		sagas[saga.Name] = saga
	}
	if saga, ok := sagas["Star Odyssey"]; !ok || saga.Total != 2 || saga.Owned != 1 {
		t.Fatalf("IGDB groups the games of its collection, whatever their publisher: %+v", sagas)
	}
	if saga, ok := sagas["La saga del héroe"]; !ok || saga.Total != 3 {
		t.Fatalf("Wikidata names the series in the language, and the deluxe edition joins it: %+v", sagas)
	}
	if saga, ok := sagas["Moon Runner"]; !ok || saga.Total != 2 {
		t.Fatalf("games without data are still grouped by name: %+v", sagas)
	}
	// the page of a game shows its series
	if saga := web.sameSaga("0100000000020000", "Star Odyssey II", "es"); saga.Total != 2 || saga.Owned != 1 {
		t.Fatalf("on the game page: %+v", saga)
	}
}

func TestPageOfASeries(t *testing.T) {
	web := seriesTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleSagas()
	var hero SagaProgress
	for _, saga := range web.sagaProgress("en") {
		if saga.Name == "Hero Saga" {
			hero = saga
		}
	}
	if hero.Url() == "" || hero.Total != 3 {
		t.Fatalf("the series without its demo: %+v", hero)
	}
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", hero.Url(), nil))
	body := page.Body.String()
	if page.Code != 200 || !strings.Contains(body, "Hero Saga: Second Tale") || !strings.Contains(body, "Hero Saga: Third Tale Deluxe") || strings.Contains(body, "Third Tale Demo") {
		t.Fatalf("the page of the series: %d", page.Code)
	}
	missing := httptest.NewRecorder()
	web.router.ServeHTTP(missing, httptest.NewRequest("GET", "/saga/bm90LWEtc2VyaWVz.html", nil))
	if missing.Code != 404 {
		t.Fatalf("an unknown series: %d", missing.Code)
	}
}

// A collection IGDB no longer knows (no name, no games) is no series: the game keeps the
// series Wikidata or its name give it.
func TestEmptyIgdbCollectionIsNoSeries(t *testing.T) {
	web := seriesTestWeb(t)
	web.timesToBeat().set("0100000000060000", TimeToBeat{Found: true, Checked: time.Now(), SeriesChecked: true, Collections: []int{78}})
	web.igdbCollections().set(78, igdbCollection{Checked: time.Now()})
	web.invalidateDerived()
	if family := web.sagaFamilyOf("0100000000060000", "Moon Runner"); strings.HasPrefix(family, "igdb:") {
		t.Fatalf("an empty collection: %q", family)
	}
}
