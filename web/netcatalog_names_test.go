package web

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// netOnlyExtras is a library of two titles without their game: one with an update the titles
// database knows, one with a DLC it does not know.
func netOnlyExtras(t *testing.T, web *Web, cover string) {
	t.Helper()
	dir := t.TempDir()
	file := func(name string, id string, version int) db.SwitchFileInfo {
		os.WriteFile(filepath.Join(dir, name), []byte("data of "+name), 0644)
		return db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: name, BaseFolder: dir, Size: int64(len("data of " + name))},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: id, Version: version}}
	}
	known := &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000020000", Name: "Known Game"}}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{"0100000000020": known}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000020": {Icon: cover,
			Updates:      map[int]db.SwitchFileInfo{65536: file("upd [0100000000020800][v65536].nsp", "0100000000020800", 65536)},
			LatestUpdate: 65536},
		"0100000000030": {Icon: cover,
			Dlc: map[string]db.SwitchFileInfo{"0100000000031001": file("Lonely Game Extra [0100000000031001][v0].nsp", "0100000000031001", 0)}},
	}})
	web.netFilesForget()
}

// A title with only an update or a DLC has the name of the titles database, else of its file:
// never a game without a name.
func TestNetCatalogNamesTitlesWithoutTheirGame(t *testing.T) {
	web, admin, key := netWeb(t)
	netOnlyExtras(t, web, "")
	console := &client{t: t, handler: admin.handler}
	got := console.do("GET", "/install/catalog", nil, "Authorization", basic("switch", key))
	var catalog netCatalog
	if err := json.Unmarshal(got.Body.Bytes(), &catalog); err != nil || got.Code != http.StatusOK {
		t.Fatalf("the catalog: %d %s", got.Code, got.Body.String())
	}
	names := map[string]bool{}
	for _, game := range catalog.Games {
		if game.Name == "" {
			t.Fatalf("a game without a name: %+v", game)
		}
		names[game.Name] = true
	}
	if len(catalog.Games) != 2 || !names["Known Game"] || !names["Lonely Game Extra"] {
		t.Fatalf("the names: %v", names)
	}
	for _, file := range web.netFiles(settings.NetInstallOptions{}) {
		if !strings.HasPrefix(file.name, "Known Game [") && !strings.HasPrefix(file.name, "Lonely Game Extra [") {
			t.Fatalf("a file without the name of its game: %q", file.name)
		}
	}
}

// A cover the cover server does not give now is "ask again later" (503), not "no cover" (404),
// which stays for a cover the server says is not there.
func TestNetCoverTellsToAskAgainWhenTheServerDoesNotAnswer(t *testing.T) {
	cover := &bytes.Buffer{}
	jpeg.Encode(cover, image.NewRGBA(image.Rect(0, 0, 100, 100)), nil)
	answering := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !answering {
			http.NotFound(w, r)
			return
		}
		w.Write(cover.Bytes())
	}))
	saved := remoteCoverBase
	remoteCoverBase = server.URL + "/i/"
	defer func() { remoteCoverBase = saved }()

	web, admin, key := netWeb(t)
	name := strings.Repeat("cd", 32) + ".jpg"
	netOnlyExtras(t, web, name)
	console := &client{t: t, handler: admin.handler}
	auth := basic("switch", key)
	address := netInstallCover + name

	// the cover server answers nothing: closed
	server.Close()
	offline := console.do("GET", address, nil, "Authorization", auth)
	if offline.Code != http.StatusServiceUnavailable || offline.Header().Get("Retry-After") == "" {
		t.Fatalf("without the cover server: %d %q", offline.Code, offline.Header())
	}

	// the cover server says there is no such cover
	gone := httptest.NewServer(server.Config.Handler)
	defer gone.Close()
	remoteCoverBase = gone.URL + "/i/"
	answering = false
	if got := console.do("GET", address, nil, "Authorization", auth); got.Code != http.StatusNotFound {
		t.Fatalf("a cover that is not there: %d", got.Code)
	}

	// and the cover once it answers
	answering = true
	got := console.do("GET", address, nil, "Authorization", auth)
	if got.Code != http.StatusOK || !bytes.HasPrefix(got.Body.Bytes(), []byte{0xFF, 0xD8}) {
		t.Fatalf("the cover: %d %q", got.Code, got.Header())
	}
}
