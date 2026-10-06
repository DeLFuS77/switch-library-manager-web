package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

func TestCoversOfOtherStores(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/removed.jpg" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("image " + r.URL.Path))
	}))
	defer server.Close()

	web := newTestWeb(t)
	os.WriteFile(filepath.Join(web.dataFolder, COVERS_JSON_FILENAME), []byte(`{
		"0100000000010000": {"iconUrl": "`+server.URL+`/eu1.jpg"},
		"0100000000020000": {"iconUrl": "`+server.URL+`/eu2.jpg"},
		"0100000000030000": {"iconUrl": "`+server.URL+`/jp3.jpg"}
	}`), 0644)
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		// no cover in the US store
		"0100000000010": {Attributes: db.TitleAttributes{Id: "0100000000010000"}},
		// a US cover that is gone
		"0100000000020": {Attributes: db.TitleAttributes{Id: "0100000000020000", IconUrl: server.URL + "/removed.jpg"}},
	}}
	web.applyCoverFallbacks(switchDB)
	if switchDB.TitlesMap["0100000000010"].Attributes.IconUrl != server.URL+"/eu1.jpg" {
		t.Fatal("a title without a cover takes the one of another store")
	}

	web.state.set(switchDB, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": {BaseExist: true},
		"0100000000020": {BaseExist: true},
		// not sold in the US store at all
		"0100000000030": {BaseExist: true},
	}})
	// the first run finds the removed US cover
	web.downloadMissingCovers()
	web.downloadMissingCovers()
	_, localDB := web.state.get()
	if localDB.TitlesMap["0100000000010"].Icon != "eu1.jpg" || localDB.TitlesMap["0100000000020"].Icon != "eu2.jpg" || localDB.TitlesMap["0100000000030"].Icon != "jp3.jpg" {
		t.Fatalf("covers: %q %q %q", localDB.TitlesMap["0100000000010"].Icon, localDB.TitlesMap["0100000000020"].Icon, localDB.TitlesMap["0100000000030"].Icon)
	}
}
