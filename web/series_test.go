package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
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
		// no data: guessed by name and publisher, as before
		"0100000000060": title("0100000000060000", "Moon Runner", "Lunar"),
		"0100000000070": title("0100000000070000", "Moon Runner 2", "Lunar"),
	}}
	localDB := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": {BaseExist: true}, "0100000000030": {BaseExist: true}, "0100000000060": {BaseExist: true},
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
