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
