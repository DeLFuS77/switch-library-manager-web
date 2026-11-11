package web

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// The catalog groups the files of a game, says what each is, and gives its cover to a console key.
func TestNetCatalog(t *testing.T) {
	web, admin, key := netWeb(t)
	// a cover for the game
	picture := image.NewRGBA(image.Rect(0, 0, 400, 400))
	for i := range picture.Pix {
		picture.Pix[i] = 200
	}
	picture.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var encoded bytes.Buffer
	jpeg.Encode(&encoded, picture, nil)
	os.MkdirAll(filepath.Join(web.dataFolder, "img"), 0755)
	os.WriteFile(filepath.Join(web.dataFolder, "img", "cover-1.jpg"), encoded.Bytes(), 0644)
	os.WriteFile(filepath.Join(web.dataFolder, "img", "private.jpg"), encoded.Bytes(), 0644)
	_, localDB := web.state.get()
	for _, title := range localDB.TitlesMap {
		title.Icon = "cover-1.jpg"
	}
	web.netFilesForget()

	console := &client{t: t, handler: admin.handler}
	auth := basic("switch", key)
	got := console.do("GET", "/install/catalog", nil, "Authorization", auth)
	var catalog netCatalog
	if err := json.Unmarshal(got.Body.Bytes(), &catalog); err != nil || got.Code != http.StatusOK {
		t.Fatalf("the catalog: %d %s", got.Code, got.Body.String())
	}
	if len(catalog.Games) != 1 || catalog.App.App != netAppName {
		t.Fatalf("one game: %+v", catalog)
	}
	game := catalog.Games[0]
	if game.Name != "Game" || game.TitleId != "0100000000010000" || game.Cover != "/install/cover/cover-1.jpg" || len(game.Files) != 3 {
		t.Fatalf("the game: %+v", game)
	}
	kinds := []string{game.Files[0].Kind, game.Files[1].Kind, game.Files[2].Kind}
	if kinds[0] != "game" || kinds[1] != "update" || kinds[2] != "dlc" {
		t.Fatalf("game, update and DLC in order: %v", kinds)
	}
	if game.Added <= 0 {
		t.Fatalf("when the game came to the library: %d", game.Added)
	}
	update := game.Files[1]
	if update.Version != 131072 || update.Format != "nsz" || update.TitleId != "0100000000010800" || update.Size <= 0 {
		t.Fatalf("the update: %+v", update)
	}
	// every file of the catalog downloads with its url
	for _, file := range game.Files {
		if got := console.do("GET", file.Url, nil, "Authorization", auth); got.Code != http.StatusOK || int64(got.Body.Len()) != file.Size {
			t.Fatalf("%s: %d %d", file.Url, got.Code, got.Body.Len())
		}
	}

	cover := console.do("GET", game.Cover, nil, "Authorization", auth)
	if cover.Code != http.StatusOK || !bytes.HasPrefix(cover.Body.Bytes(), []byte{0xFF, 0xD8}) {
		t.Fatalf("the cover: %d %q", cover.Code, cover.Header())
	}
	// only covers of the catalog: not any image of the data folder, nothing outside it
	for _, path := range []string{"/install/cover/private.jpg", "/install/cover/..%2Fsettings.json", "/install/cover/"} {
		if got := console.do("GET", path, nil, "Authorization", auth); got.Code == http.StatusOK {
			t.Fatalf("%s: %d", path, got.Code)
		}
	}
	if anonymous := (&client{t: t, handler: admin.handler}).do("GET", "/install/catalog", nil); anonymous.Code == http.StatusOK {
		t.Fatalf("the catalog without a key: %d", anonymous.Code)
	}
}

// Game card files (XCI, XCZ) are in the catalog as they are, and download in pieces: NXDock reads
// the container from the bytes.
func TestNetCatalogGivesGameCardFiles(t *testing.T) {
	web, admin, key := netWeb(t)
	dir := t.TempDir()
	file := func(name string, id string, version int) db.SwitchFileInfo {
		os.WriteFile(filepath.Join(dir, name), []byte("card data of "+name), 0644)
		return db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: name, BaseFolder: dir, Size: int64(len("card data of " + name))},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: id, Version: version}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000020": {BaseExist: true, File: file("Card [0100000000020000][v0].xci", "0100000000020000", 0)},
		"0100000000030": {BaseExist: true, File: file("Packed [0100000000030000][v0].xcz", "0100000000030000", 0)},
	}})
	web.netFilesForget()

	console := &client{t: t, handler: admin.handler}
	auth := basic("switch", key)
	got := console.do("GET", "/install/catalog", nil, "Authorization", auth)
	var catalog netCatalog
	if err := json.Unmarshal(got.Body.Bytes(), &catalog); err != nil || got.Code != http.StatusOK {
		t.Fatalf("the catalog: %d %s", got.Code, got.Body.String())
	}
	formats := map[string]bool{}
	for _, game := range catalog.Games {
		for _, f := range game.Files {
			formats[f.Format] = true
			piece := console.do("GET", f.Url, nil, "Authorization", auth, "Range", "bytes=0-3")
			if piece.Code != http.StatusPartialContent || piece.Body.String() != "card" {
				t.Fatalf("%s in pieces: %d %q", f.Url, piece.Code, piece.Body.String())
			}
		}
	}
	if len(catalog.Games) != 2 || !formats["xci"] || !formats["xcz"] {
		t.Fatalf("an XCI and an XCZ: %+v", catalog.Games)
	}
}

// A console that installs NXPress's extra compression itself asks with ?ncz=native: files without
// it are given as always.
func TestNetCatalogNativeLeavesPlainFiles(t *testing.T) {
	_, admin, key := netWeb(t)
	console := &client{t: t, handler: admin.handler}
	auth := basic("switch", key)
	var plain, native netCatalog
	json.Unmarshal(console.do("GET", "/install/catalog", nil, "Authorization", auth).Body.Bytes(), &plain)
	json.Unmarshal(console.do("GET", "/install/catalog?ncz=native", nil, "Authorization", auth).Body.Bytes(), &native)
	if len(plain.Games) != 1 || len(native.Games) != 1 || len(plain.Games[0].Files) != len(native.Games[0].Files) {
		t.Fatalf("the same games: %+v %+v", plain, native)
	}
	for i, f := range native.Games[0].Files {
		if f != plain.Games[0].Files[i] {
			t.Fatalf("a plain file changed: %+v %+v", f, plain.Games[0].Files[i])
		}
		if got := console.do("GET", f.Url+"?raw=1", nil, "Authorization", auth); got.Code != http.StatusOK || int64(got.Body.Len()) != f.Size {
			t.Fatalf("raw: %d %d", got.Code, got.Body.Len())
		}
	}
}

// While the list of files is made, which in a big library on a NAS takes minutes the first time,
// the catalog does not keep the console waiting: it tells how far it is and to ask again. An older
// list is given at once, and made again behind it.
func TestNetCatalogWhileTheListIsMade(t *testing.T) {
	web, admin, key := netWeb(t)
	waitWas := netCatalogWait
	netCatalogWait = 50 * time.Millisecond
	t.Cleanup(func() { netCatalogWait = waitWas })
	console := &client{t: t, handler: admin.handler}
	auth := basic("switch", key)

	// a list being made that does not end yet
	_, localDB := web.state.get()
	options := settings.ReadSettings(web.dataFolder).NetInstall
	web.netFilesForget()
	making := make(chan struct{})
	web.netList.mutex.Lock()
	web.netList.making, web.netList.makingLocalDB, web.netList.makingAllUpdates = making, localDB, options.AllUpdates
	web.netList.makingGeneration, web.netList.makingDone, web.netList.makingTotal = web.netList.generation, 5, 10
	web.netList.mutex.Unlock()

	started := time.Now()
	got := console.do("GET", "/install/catalog", nil, "Authorization", auth)
	var preparing struct {
		Preparing bool `json:"preparing"`
		Done      int  `json:"done"`
		Total     int  `json:"total"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &preparing); err != nil || got.Code != http.StatusServiceUnavailable ||
		!preparing.Preparing || preparing.Done != 5 || preparing.Total != 10 || got.Header().Get("Retry-After") == "" {
		t.Fatalf("preparing: %d %q %s", got.Code, got.Header(), got.Body.String())
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("the console waited %s", time.Since(started))
	}

	// the list ends (here: is let go), and the next question gets the catalog
	web.netList.mutex.Lock()
	web.netList.making = nil
	web.netList.mutex.Unlock()
	close(making)
	netCatalogWait = 30 * time.Second
	if got := console.do("GET", "/install/catalog", nil, "Authorization", auth); got.Code != http.StatusOK {
		t.Fatalf("the catalog once made: %d %s", got.Code, got.Body.String())
	}

	// an older list is given at once, and made again behind it
	web.netList.mutex.Lock()
	web.netList.until = time.Now().Add(-time.Second)
	web.netList.mutex.Unlock()
	if got := console.do("GET", "/install/catalog", nil, "Authorization", auth); got.Code != http.StatusOK {
		t.Fatalf("the older list: %d %s", got.Code, got.Body.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		web.netList.mutex.Lock()
		fresh := time.Now().Before(web.netList.until)
		web.netList.mutex.Unlock()
		if fresh {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the older list was not made again")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
