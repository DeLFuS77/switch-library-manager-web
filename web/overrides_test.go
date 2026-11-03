package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const overrideTestId = "0100000000010000"

// testCover is an image of width by height pixels in PNG, with transparent parts or not.
func testCover(t *testing.T, width, height int, transparent bool) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			alpha := uint8(255)
			if transparent && x < width/2 {
				alpha = 0
			}
			picture.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 120, A: alpha})
		}
	}
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// overrideWeb is an app with a game of the library read from its file, the titles database of a
// synchronization before (none is downloaded: there is no connection) with its names in Spanish,
// and a server with its cover. It returns the path of the game and the address of its cover.
func overrideWeb(t *testing.T) (*Web, string, string) {
	t.Helper()
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	if err := testnsp.WriteKeys(web.dataFolder); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { settings.InitSwitchKeys(t.TempDir()) })
	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	web.localDbManager = manager

	folder := t.TempDir()
	path := filepath.Join(folder, "Game ["+overrideTestId+"][v0].nsp")
	if _, err := testnsp.WriteNsp(path); err != nil {
		t.Fatal(err)
	}
	icon := &bytes.Buffer{}
	jpeg.Encode(icon, image.NewRGBA(image.Rect(0, 0, 64, 64)), nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(icon.Bytes()) }))
	t.Cleanup(server.Close)
	writeOverrideTitles(t, web, "Database Name", "Nombre traducido", server.URL+"/icon.jpg")

	fallbacks, versionFallbacks := settings.FALLBACK_TITLES_JSON_URLS, settings.FALLBACK_VERSIONS_JSON_URLS
	t.Cleanup(func() {
		settings.FALLBACK_TITLES_JSON_URLS, settings.FALLBACK_VERSIONS_JSON_URLS = fallbacks, versionFallbacks
	})
	settings.FALLBACK_TITLES_JSON_URLS = []string{fmt.Sprintf("https://%s/titles.json", closedAddress(t))}
	settings.FALLBACK_VERSIONS_JSON_URLS = []string{fmt.Sprintf("https://%s/versions.json", closedAddress(t))}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Folder = folder
		s.TitlesJsonUrl = fmt.Sprintf("https://%s/titles.json", closedAddress(t))
		s.VersionsJsonUrl = fmt.Sprintf("https://%s/versions.json", closedAddress(t))
		s.LocalizedTitlesJsonUrl = fmt.Sprintf("https://%s/titles.%%s.json", closedAddress(t))
		s.Language = "es"
	})
	web.HandleApi()
	web.HandleCovers()
	return web, path, server.URL + "/icon.jpg"
}

// writeOverrideTitles writes the files of the titles database a synchronization replaces.
func writeOverrideTitles(t *testing.T, web *Web, name string, spanish string, icon string) {
	t.Helper()
	files := map[string]string{
		settings.TITLE_JSON_FILENAME:    fmt.Sprintf(`{%q: {"id": %q, "name": %q, "region": "US", "iconUrl": %q, "description": "From the database"}}`, overrideTestId, overrideTestId, name, icon),
		settings.VERSIONS_JSON_FILENAME: fmt.Sprintf(`{%q: {"65536": "2020-01-01"}}`, overrideTestId),
		"titles.es.json":                fmt.Sprintf(`{%q: {"name": %q, "description": "Descripción traducida"}}`, overrideTestId, spanish),
	}
	for file, content := range files {
		if err := os.WriteFile(filepath.Join(web.dataFolder, file), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// waitForScans waits until n synchronizations or scans ended, with the work after them and the
// covers they download.
func waitForScans(t *testing.T, web *Web, n uint64) {
	t.Helper()
	quiet := time.Time{}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d scans ended of %d", web.scansDone.Load(), n)
		}
		web.covers.mutex.Lock()
		covers := web.covers.running
		web.covers.mutex.Unlock()
		if web.scansDone.Load() < n || web.state.IsSynchronizing() || web.backgroundWork.Load() != 0 || covers {
			quiet = time.Time{}
			continue
		}
		if quiet.IsZero() {
			quiet = time.Now()
		} else if time.Since(quiet) > 300*time.Millisecond {
			break
		}
	}
	web.afterScanWork.Wait()
}

func sendOverride(t *testing.T, web *Web, method string, fields map[string]string, cover []byte) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	for name, value := range fields {
		form.WriteField(name, value)
	}
	if cover != nil {
		part, _ := form.CreateFormFile("cover", "cover.png")
		part.Write(cover)
	}
	form.Close()
	request := httptest.NewRequest(method, "/api/titles/"+overrideTestId+"/override", body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, request)
	return recorder
}

// The name, cover and description changed by hand win over the titles database, its translation
// and the cover found, in every language, also after a synchronization that replaces the titles
// database, a scan and "Search covers again"; a game deleted keeps them for when it comes back,
// and "Back to the original" shows it as it was. The file of the game is never touched.
func TestTitleOverridesWinAndAreKept(t *testing.T) {
	web, path, icon := overrideWeb(t)
	gameBefore, _ := os.ReadFile(path)
	scans := uint64(0)
	synchronize := func() {
		t.Helper()
		if !web.Synchronize(tasklog.TRIGGER_MANUAL) {
			t.Fatal("the synchronization starts")
		}
		scans++
		waitForScans(t, web, scans)
	}
	rescan := func() {
		t.Helper()
		if !web.Rescan(tasklog.TRIGGER_MANUAL) {
			t.Fatal("the scan starts")
		}
		scans++
		waitForScans(t, web, scans)
	}
	detail := func(lang string) TitleDetail {
		t.Helper()
		detail, ok := web.getTitleDetail(overrideTestId, lang)
		if !ok {
			t.Fatal("the game is known")
		}
		return detail
	}
	apiIcon := func() string {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/titles", nil))
		var items map[string]ApiTitleItem
		json.Unmarshal(recorder.Body.Bytes(), &items)
		return items[overrideTestId].IconUrl
	}

	synchronize()
	if got := detail("es"); !got.Owned || got.Name != "Nombre traducido" || got.ImageUrl != "/i/icon.jpg" {
		t.Fatalf("the game as the titles database tells it: %q %q owned %v", got.Name, got.ImageUrl, got.Owned)
	}

	response := sendOverride(t, web, http.MethodPost, map[string]string{"name": "  Mi\tjuego ​", "description": "Mi descripción"}, testCover(t, 2000, 1000, false))
	var kept ApiTitleOverride
	if err := json.Unmarshal(response.Body.Bytes(), &kept); err != nil || response.Code != http.StatusOK || !kept.Edited || kept.Name != "Mi juego" || !overrideCoverName.MatchString(strings.TrimPrefix(kept.CoverUrl, "/i/")) {
		t.Fatalf("the change is kept: %d %s", response.Code, response.Body.String())
	}
	cover := strings.TrimPrefix(kept.CoverUrl, "/i/")
	if !strings.HasPrefix(cover, overrideTestId+"-") {
		t.Fatalf("the cover is named with the ID of the game: %s", cover)
	}
	file, err := os.Open(filepath.Join(web.dataFolder, OVERRIDE_COVERS_FOLDER, cover))
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(file)
	file.Close()
	if err != nil || format != "jpeg" || config.Width != overrideCoverSize || config.Height != overrideCoverSize/2 {
		t.Fatalf("the cover is made again, at most %d pixels a side: %v %s %dx%d", overrideCoverSize, err, format, config.Width, config.Height)
	}
	if _, err := os.Stat(filepath.Join(web.dataFolder, "img", "thumbs", cover)); err != nil {
		t.Fatalf("its thumbnail is made: %v", err)
	}
	check := func(when string) {
		t.Helper()
		for _, lang := range []string{"es", "en"} {
			if got := detail(lang); got.Name != "Mi juego" || got.Description != "Mi descripción" || got.ImageUrl != kept.CoverUrl {
				t.Fatalf("%s, in %s: %q %q %q", when, lang, got.Name, got.Description, got.ImageUrl)
			}
		}
		if icon := apiIcon(); icon != kept.CoverUrl {
			t.Fatalf("%s, the API gives the cover chosen: %q", when, icon)
		}
		if items, _ := web.getLibrary(defaultFilter(), "es"); len(items) != 1 || items[0].Name != "Mi juego" || items[0].ImageUrl != kept.CoverUrl || items[0].OriginalName == "Mi juego" {
			t.Fatalf("%s, the card of the library: %+v", when, items)
		}
	}
	check("at once")

	// a synchronization that replaces the titles database and its translations
	writeOverrideTitles(t, web, "Renamed In Database", "Nombre nuevo", icon)
	_, library := web.state.get()
	web.state.set(nil, library)
	synchronize()
	if switchDB, _ := web.state.get(); switchDB == nil || switchDB.TitlesMap["0100000000010"].Attributes.Name != "Renamed In Database" {
		t.Fatal("the titles database was replaced")
	}
	check("after a synchronization")
	rescan()
	check("after a scan")

	// "Search covers again" looks for the covers of the titles database, not the one chosen
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/covers/retry", nil))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("search covers again: %d", recorder.Code)
	}
	waitForScans(t, web, scans)
	if _, library := web.state.get(); library.TitlesMap["0100000000010"].Icon != "icon.jpg" {
		t.Fatalf("the cover found stays as it was: %q", library.TitlesMap["0100000000010"].Icon)
	}
	check("after searching the covers again")

	// a game deleted keeps its change, and finds it when it comes back
	if err := os.Rename(path, path+".away"); err != nil {
		t.Fatal(err)
	}
	rescan()
	if got := detail("es"); got.Owned {
		t.Fatal("the game left the library")
	}
	if _, ok := web.titleOverrides().get(overrideTestId); !ok {
		t.Fatal("the change of a game deleted is kept")
	}
	if err := os.Rename(path+".away", path); err != nil {
		t.Fatal(err)
	}
	rescan()
	if got := detail("es"); !got.Owned {
		t.Fatal("the game came back")
	}
	check("when the game comes back")
	if gameAfter, _ := os.ReadFile(path); !bytes.Equal(gameBefore, gameAfter) || filepath.Base(path) != "Game ["+overrideTestId+"][v0].nsp" {
		t.Fatal("the file of the game is never touched")
	}

	// back to the original
	response = sendOverride(t, web, http.MethodDelete, nil, nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"edited":true`) {
		t.Fatalf("back to the original: %d %s", response.Code, response.Body.String())
	}
	if got := detail("es"); got.Name != "Nombre nuevo" || got.Description != "Descripción traducida" || got.ImageUrl != "/i/icon.jpg" {
		t.Fatalf("the game as the titles database tells it again: %q %q %q", got.Name, got.Description, got.ImageUrl)
	}
	if _, err := os.Stat(filepath.Join(web.dataFolder, OVERRIDE_COVERS_FOLDER, cover)); !os.IsNotExist(err) {
		t.Fatal("the cover chosen is deleted")
	}
	if data, _ := os.ReadFile(filepath.Join(web.dataFolder, TITLE_OVERRIDES_FILENAME)); strings.Contains(string(data), overrideTestId) {
		t.Fatalf("nothing is kept of the game: %s", data)
	}
}

// Changing only the name keeps the cover chosen before; removeCover asks for the one of the game
// again; a cover can come from an address, downloaded once, and what is not an image is refused.
func TestTitleOverrideFields(t *testing.T) {
	var requests atomic.Int64
	cover := testCover(t, 300, 300, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/cover.png":
			w.Write(cover)
		case "/page.html":
			w.Write([]byte("<html><body>not an image</body></html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	web := newTestWeb(t)
	web.HandleApi()
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{"0100000000010": {Attributes: db.TitleAttributes{Id: overrideTestId, Name: "Game"}}}}, nil)

	response := sendOverride(t, web, http.MethodPost, map[string]string{"coverUrl": server.URL + "/cover.png"}, nil)
	var kept ApiTitleOverride
	json.Unmarshal(response.Body.Bytes(), &kept)
	if response.Code != http.StatusOK || !strings.HasSuffix(kept.CoverUrl, ".png") || kept.Name != "" || requests.Load() != 1 {
		t.Fatalf("a cover from an address, with transparent parts, in PNG: %d %s", response.Code, response.Body.String())
	}
	response = sendOverride(t, web, http.MethodPost, map[string]string{"name": "Only The Name"}, nil)
	var renamed ApiTitleOverride
	json.Unmarshal(response.Body.Bytes(), &renamed)
	if renamed.Name != "Only The Name" || renamed.CoverUrl != kept.CoverUrl || requests.Load() != 1 {
		t.Fatalf("the cover chosen stays, and is not downloaded again: %s", response.Body.String())
	}
	if switchDB, _ := web.state.get(); titleName(switchDB, "fr", overrideTestId, "Game") != "Only The Name" || coverUrl(switchDB, nil, switchDB.TitlesMap["0100000000010"].Attributes) != kept.CoverUrl {
		t.Fatal("shown at once")
	}

	for _, refused := range []struct {
		fields map[string]string
		cover  []byte
		status int
	}{
		{map[string]string{"coverUrl": server.URL + "/page.html"}, nil, http.StatusBadRequest},
		{map[string]string{"coverUrl": server.URL + "/missing.png"}, nil, http.StatusBadRequest},
		{map[string]string{"coverUrl": "file:///etc/passwd"}, nil, http.StatusBadRequest},
		{nil, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), http.StatusBadRequest},
		{nil, testCover(t, 7000, 10, false), http.StatusBadRequest},
	} {
		if response := sendOverride(t, web, http.MethodPost, refused.fields, refused.cover); response.Code != refused.status {
			t.Errorf("%v refused: %d %s", refused.fields, response.Code, response.Body.String())
		}
	}
	if now, _ := web.titleOverrides().get(overrideTestId); now.Cover != strings.TrimPrefix(kept.CoverUrl, "/i/") || now.Name != "Only The Name" {
		t.Fatalf("what is refused changes nothing: %+v", now)
	}

	response = sendOverride(t, web, http.MethodPost, map[string]string{"name": "Only The Name", "removeCover": "true"}, nil)
	var uncovered ApiTitleOverride
	json.Unmarshal(response.Body.Bytes(), &uncovered)
	if uncovered.CoverUrl != "" || uncovered.Name != "Only The Name" {
		t.Fatalf("the cover of the game again: %s", response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(web.dataFolder, OVERRIDE_COVERS_FOLDER, strings.TrimPrefix(kept.CoverUrl, "/i/"))); !os.IsNotExist(err) {
		t.Fatal("the cover left is deleted")
	}
	// nothing left: the change is forgotten
	sendOverride(t, web, http.MethodPost, map[string]string{"name": " "}, nil)
	if _, ok := web.titleOverrides().get(overrideTestId); ok {
		t.Fatal("an empty change is forgotten")
	}
}

// Only an administrator changes a game by hand; a game nobody knows, or what is not a title ID,
// is refused.
func TestTitleOverrideRefusals(t *testing.T) {
	web := newTestWeb(t)
	web.HandleApi()
	request := func(method string, id string) int {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, "/api/titles/"+id+"/override", strings.NewReader(url.Values{"name": {"x"}}.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		web.router.ServeHTTP(recorder, request)
		return recorder.Code
	}
	if code := request(http.MethodPost, overrideTestId); code != http.StatusNotFound {
		t.Fatalf("a game nobody knows: %d", code)
	}
	if code := request(http.MethodGet, "nope"); code != http.StatusBadRequest {
		t.Fatalf("not a title ID: %d", code)
	}
	for _, role := range []string{ROLE_EDITOR, ROLE_VIEWER, ROLE_GUEST} {
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			if roleAllows(role, httptest.NewRequest(method, "/api/titles/"+overrideTestId+"/override", nil)) {
				t.Errorf("%s cannot %s", role, method)
			}
		}
	}
	if !roleAllows(ROLE_VIEWER, httptest.NewRequest(http.MethodGet, "/api/titles/"+overrideTestId+"/override", nil)) {
		t.Error("anybody can look")
	}
}

// A cover is a real image made again: SVG, text and what is too large are refused; a GIF keeps
// its first frame; the parts that are transparent keep it in PNG.
func TestNormalizeCover(t *testing.T) {
	for name, data := range map[string][]byte{
		"svg":  []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`),
		"text": []byte("hello"),
		"html": []byte("<html><img src=x onerror=alert(1)></html>"),
	} {
		if _, _, err := normalizeCover(data); !errors.Is(err, errOverrideNotImage) {
			t.Errorf("%s refused: %v", name, err)
		}
	}
	if _, _, err := normalizeCover(append(testCover(t, 10, 10, false), make([]byte, maxOverrideCoverBytes)...)); !errors.Is(err, errOverrideCoverLarge) {
		t.Errorf("a file too large: %v", err)
	}
	if _, _, err := normalizeCover(testCover(t, 10, maxOverrideCoverSide+1, false)); !errors.Is(err, errOverrideCoverLarge) {
		t.Errorf("an image too large: %v", err)
	}
	animated := &bytes.Buffer{}
	frame := image.NewPaletted(image.Rect(0, 0, 40, 30), []color.Color{color.Black, color.White})
	gif.EncodeAll(animated, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{10, 10}})
	for name, test := range map[string]struct {
		data          []byte
		extension     string
		width, height int
	}{
		"small":       {testCover(t, 200, 100, false), "jpg", 200, 100},
		"large":       {testCover(t, 1500, 3000, false), "jpg", 512, 1024},
		"transparent": {testCover(t, 100, 100, true), "png", 100, 100},
		"gif":         {animated.Bytes(), "jpg", 40, 30},
	} {
		data, extension, err := normalizeCover(test.data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || extension != test.extension || config.Width != test.width || config.Height != test.height {
			t.Errorf("%s: %s %dx%d %v", name, extension, config.Width, config.Height, err)
		}
	}
	if got := cleanOverrideText("  Two\n lines\x00 ‮", 100, true); got != "Two lines" {
		t.Errorf("a name on one line, without what is not text: %q", got)
	}
	if got := cleanOverrideText("ゲーム名前です", 3, true); got != "ゲーム" {
		t.Errorf("cut by characters: %q", got)
	}
}

// The changes by hand, their covers and the copies chosen of the games of several regions are kept
// in a backup and restored; a cover in a backup that is not an image is refused.
func TestBackupKeepsTheChangesByHand(t *testing.T) {
	source := newTestWeb(t)
	settings.UpdateSettings(source.dataFolder, func(s *settings.AppSettings) { s.Folder = "/games" })
	cover, err := source.saveOverrideCover(overrideTestId, testCover(t, 50, 50, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.titleOverrides().set(overrideTestId, TitleOverride{Name: "Kept", Cover: cover}); err != nil {
		t.Fatal(err)
	}
	source.regionChoices().choose("0100000000020000", []string{"0100000000020000", "0100000000030000"})
	buffer := &bytes.Buffer{}
	if err := source.writeBackup(buffer); err != nil {
		t.Fatal(err)
	}

	target := newTestWeb(t)
	os.MkdirAll(filepath.Join(target.dataFolder, OVERRIDE_COVERS_FOLDER), 0755)
	os.WriteFile(filepath.Join(target.dataFolder, OVERRIDE_COVERS_FOLDER, "0100000000090000-00000000.jpg"), []byte("left of before"), 0644)
	if err := target.restoreBackup(buffer.Bytes(), nil); err != nil {
		t.Fatal(err)
	}
	if kept, ok := target.titleOverrides().get(overrideTestId); !ok || kept.Name != "Kept" || kept.Cover != cover {
		t.Fatalf("the change restored: %+v", kept)
	}
	if data, err := os.ReadFile(filepath.Join(target.dataFolder, OVERRIDE_COVERS_FOLDER, cover)); err != nil || backupCoverImage(data) == false {
		t.Fatalf("its cover restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target.dataFolder, OVERRIDE_COVERS_FOLDER, "0100000000090000-00000000.jpg")); !os.IsNotExist(err) {
		t.Fatal("a cover no change uses is deleted")
	}
	if !target.regionChoices().snapshot()["0100000000020000"] {
		t.Fatal("the copy chosen restored")
	}

	bad := zipFiles(t, map[string]string{settings.SETTINGS_FILENAME: `{}`, OVERRIDE_COVERS_FOLDER + "/" + overrideTestId + "-00000000.jpg": "<svg/>"})
	if _, err := readBackup(bad); !errors.Is(err, ErrBackupFile) {
		t.Fatalf("a cover that is not an image: %v", err)
	}
	other := zipFiles(t, map[string]string{settings.SETTINGS_FILENAME: `{}`, OVERRIDE_COVERS_FOLDER + "/../evil.jpg": "x", "img/x.jpg": "x"})
	if files, err := readBackup(other); err != nil || len(files) != 1 {
		t.Fatalf("other files are left out: %v %v", err, files)
	}
}

// The messages of a change by hand that is refused are translated in every language.
func TestOverrideMessagesAreTranslated(t *testing.T) {
	for _, err := range []error{errOverrideId, errOverrideUnknown, errOverrideNotImage, errOverrideCoverLarge, errOverrideCoverUrl, errOverrideSave} {
		for _, lang := range i18n.SupportedLanguages {
			if _, ok := i18n.Translations[lang][err.Error()]; !ok && lang != i18n.DEFAULT_LANGUAGE {
				t.Errorf("no %s translation for %q", lang, err.Error())
			}
		}
	}
}

// TestOverrideCoverRejectsTraversal checks that a cover name restored from a backup cannot
// reach out of the covers folder: reload drops a traversal name and overrideCoverPath refuses
// it, so a crafted overrides.json cannot make the app read prod.keys or any file outside.
func TestOverrideCoverRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TITLE_OVERRIDES_FILENAME)
	content := `{"` + overrideTestId + `":{"cover":"../prod.keys"}}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	store := &titleOverrides{path: path}
	store.reload()
	if cover := store.titles[overrideTestId].Cover; cover != "" {
		t.Fatalf("a traversal cover was kept: %q", cover)
	}

	web := &Web{dataFolder: dir}
	if got := web.overrideCoverPath("../prod.keys"); got != "" {
		t.Fatalf("overrideCoverPath did not refuse a traversal name: %q", got)
	}
	valid := overrideTestId + "-00000000.jpg"
	if got := web.overrideCoverPath(valid); got != filepath.Join(dir, OVERRIDE_COVERS_FOLDER, valid) {
		t.Fatalf("overrideCoverPath refused a valid name: %q", got)
	}
}
