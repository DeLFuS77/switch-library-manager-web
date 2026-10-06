package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestTitlesAreProcessedOnceAndDetailsStayOnDisk(t *testing.T) {
	web := newTestWeb(t)
	os.WriteFile(filepath.Join(web.dataFolder, settings.TITLE_JSON_FILENAME), []byte(`{
		"0100000000010000": {"id": "0100000000010000", "name": "Game", "description": "A long description", "screenshots": ["https://example.com/1.jpg"]},
		"0100000000010800": {"id": "0100000000010800"},
		"0100000000011001": {"id": "0100000000011001", "name": "Game DLC", "description": "DLC text"}
	}`), 0644)
	os.WriteFile(filepath.Join(web.dataFolder, settings.VERSIONS_JSON_FILENAME), []byte(`{"0100000000010000": {"65536": "2020-01-01"}}`), 0644)
	os.WriteFile(filepath.Join(web.dataFolder, "titles.es.json"), []byte(`{"0100000000010000": {"name": "Juego", "description": "Una descripción"}}`), 0644)

	switchDB, err := web.readTitles()
	if err != nil {
		t.Fatal(err)
	}
	title := switchDB.TitlesMap["0100000000010"]
	if title == nil || title.Attributes.Name != "Game" || title.Attributes.Description != "" || title.Attributes.Screenshots != nil {
		t.Fatalf("the details must not stay in memory: %+v", title)
	}
	if details := web.titleDetails(title, "en"); details.Description != "A long description" || len(details.Screenshots) != 1 {
		t.Fatalf("details from the store: %+v", details)
	}

	// the second start reads the processed copy
	stamp := db.FileStamp(filepath.Join(web.dataFolder, settings.TITLE_JSON_FILENAME), filepath.Join(web.dataFolder, settings.VERSIONS_JSON_FILENAME))
	cached, ok := web.titleStore().LoadTitles(stamp)
	if !ok || cached.TitlesMap["0100000000010"].Attributes.Name != "Game" || cached.TitlesMap["0100000000010"].Updates[65536] != "2020-01-01" {
		t.Fatalf("processed copy: %v %+v", ok, cached)
	}
	if _, ok := web.titleStore().LoadTitles("other files"); ok {
		t.Fatal("a copy made from other files is not used")
	}

	// names in other languages keep their descriptions on disk too
	localized, err := web.readLocalizedTitles("es", filepath.Join(web.dataFolder, "titles.es.json"))
	if err != nil || localized["0100000000010000"].Name != "Juego" || localized["0100000000010000"].Description != "" {
		t.Fatalf("localized: %+v %v", localized, err)
	}
	if details := web.titleDetails(title, "es"); details.Description != "Una descripción" {
		t.Fatalf("localized description: %+v", details)
	}
}
