package web

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// halves is an image of 200×100: its left half red, its right half blue.
func halves(t *testing.T) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	for y := range 100 {
		for x := range 200 {
			picture.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
			if x >= 100 {
				picture.SetNRGBA(x, y, color.NRGBA{B: 255, A: 255})
			}
		}
	}
	var data bytes.Buffer
	png.Encode(&data, picture)
	return data.Bytes()
}

func near(c color.Color, r, g, b uint32) bool {
	cr, cg, cb, _ := c.RGBA()
	diff := func(a, b uint32) bool { return a>>8 > b+40 || a>>8+40 < b }
	return !diff(cr, r) && !diff(cg, g) && !diff(cb, b)
}

// A crop takes the square chosen, told in parts of the image, kept inside it; Fit takes the whole
// image in the middle of a white square.
func TestCoverCropCuts(t *testing.T) {
	source, _, _ := image.Decode(bytes.NewReader(halves(t)))
	for _, test := range []struct {
		crop       CoverCrop
		side       int
		r, g, b    uint32
		cornerNote string
	}{
		{CoverCrop{X: 0.5, Y: 0, Size: 0.5}, 100, 0, 0, 255, "the right half"},
		{CoverCrop{X: 0, Y: 0, Size: 0.25}, 50, 255, 0, 0, "a quarter of the left"},
		// past the right edge and larger than the picture: kept inside it
		{CoverCrop{X: 0.9, Y: 0.5, Size: 1}, 100, 0, 0, 255, "kept inside"},
		{CoverCrop{Fit: true}, 200, 255, 255, 255, "white above the whole image"},
	} {
		cut := test.crop.cut(source)
		if cut.Bounds().Dx() != test.side || cut.Bounds().Dy() != test.side || !near(cut.At(cut.Bounds().Min.X+1, cut.Bounds().Min.Y+1), test.r, test.g, test.b) {
			t.Errorf("%s: %v %v", test.cornerNote, cut.Bounds(), cut.At(1, 1))
		}
	}
	fit := CoverCrop{Fit: true}.cut(source)
	if !near(fit.At(10, 100), 255, 0, 0) || !near(fit.At(190, 100), 0, 0, 255) {
		t.Error("the whole image in the middle")
	}
}

// Edit sends the crop with the image chosen, or alone to cut the cover kept; a crop that cannot be
// one is refused, and its texts are in every language.
func TestCoverCropInEdit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(halves(t)) }))
	defer server.Close()
	web := newTestWeb(t)
	web.HandleApi()
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{"0100000000010": {Attributes: db.TitleAttributes{Id: overrideTestId, Name: "Game"}}}}, nil)
	keptCover := func(response *httptest.ResponseRecorder) image.Image {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf("%d %s", response.Code, response.Body)
		}
		override, _ := web.titleOverrides().get(overrideTestId)
		data, err := os.ReadFile(web.overrideCoverPath(override.Cover))
		if err != nil {
			t.Fatal(err)
		}
		picture, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return picture
	}

	// the right half of the image of an address
	picture := keptCover(sendOverride(t, web, http.MethodPost, map[string]string{"coverUrl": server.URL + "/cover.png", "cropX": "0.5", "cropY": "0", "cropSize": "0.5"}, nil))
	if picture.Bounds().Dx() != 100 || picture.Bounds().Dy() != 100 || !near(picture.At(50, 50), 0, 0, 255) {
		t.Fatalf("the right half: %v %v", picture.Bounds(), picture.At(50, 50))
	}
	// the cover kept, cut again: its left quarter, as it is now blue, stays blue
	picture = keptCover(sendOverride(t, web, http.MethodPost, map[string]string{"cropX": "0", "cropY": "0", "cropSize": "0.5"}, nil))
	if picture.Bounds().Dx() != 50 || !near(picture.At(25, 25), 0, 0, 255) {
		t.Fatalf("the cover kept, cut: %v", picture.Bounds())
	}
	// an image uploaded, whole on white
	picture = keptCover(sendOverride(t, web, http.MethodPost, map[string]string{"cropFit": "true"}, halves(t)))
	if picture.Bounds().Dx() != 200 || picture.Bounds().Dy() != 200 || !near(picture.At(100, 10), 255, 255, 255) || !near(picture.At(10, 100), 255, 0, 0) {
		t.Fatalf("whole on white: %v", picture.Bounds())
	}

	for _, fields := range []map[string]string{
		{"cropX": "2", "cropY": "0", "cropSize": "0.5"},
		{"cropX": "0", "cropY": "0", "cropSize": "0"},
		{"cropX": "x", "cropY": "0", "cropSize": "0.5"},
	} {
		if response := sendOverride(t, web, http.MethodPost, fields, nil); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), string(errCoverCrop)) {
			t.Errorf("%v: %d %s", fields, response.Code, response.Body)
		}
	}
	for _, lang := range i18n.SupportedLanguages {
		if lang == i18n.DEFAULT_LANGUAGE {
			continue
		}
		for _, text := range []string{string(errCoverCrop), string(errCoverCropNoCover)} {
			if i18n.Translate(lang, text) == text {
				t.Errorf("%s: %q", lang, text)
			}
		}
	}
}

// A crop alone for a game without a cover is told.
func TestCoverCropWithoutACover(t *testing.T) {
	web := newTestWeb(t)
	web.HandleApi()
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{"0100000000010": {Attributes: db.TitleAttributes{Id: overrideTestId, Name: "Game"}}}}, nil)
	response := sendOverride(t, web, http.MethodPost, map[string]string{"cropFit": "true"}, nil)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), string(errCoverCropNoCover)) {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
}

// Set the icon takes the part of the cover chosen in the window of the crop: kept while its task
// waits, used, and forgotten once the NUP has its icon.
func TestNupIconCropped(t *testing.T) {
	web, _ := compressWeb(t)
	web.handleNupIcon()
	path := noIconNupGame(t, web, true)
	// the cover of the game: its left half red, its right half blue
	os.WriteFile(filepath.Join(web.dataFolder, "img", "cover.jpg"), halves(t), 0o644)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/title/nup-icon", strings.NewReader(url.Values{"path": {path}, "cropX": {"0.5"}, "cropY": {"0"}, "cropSize": {"0.5"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	web.router.ServeHTTP(recorder, request)
	answer := map[string]int{}
	if recorder.Code != http.StatusAccepted || json.Unmarshal(recorder.Body.Bytes(), &answer) != nil {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
	if task := waitForTask(t, web, tasklog.TASK_NUP_ICON); task.Status != tasklog.TASK_SUCCESS {
		t.Fatalf("%+v", task)
	}
	container, err := switchfs.ReadNup(path)
	if err != nil || !container.HasIcon() {
		t.Fatalf("no icon: %v", err)
	}
	data, _ := container.Icon()
	icon, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil || icon.Bounds().Dx() != 256 || !near(icon.At(20, 128), 0, 0, 255) || !near(icon.At(236, 128), 0, 0, 255) {
		t.Fatalf("the right half, 256×256: %v", err)
	}
	if _, ok := web.nupIconCrops().get(path); ok {
		t.Error("the crop is kept after the icon is given")
	}
	if _, err := os.Stat(filepath.Join(web.dataFolder, NUP_ICON_CROPS_FILE)); !os.IsNotExist(err) {
		t.Errorf("the file of the crops is left: %v", err)
	}
}

// The crops of the icons kept are read back, and one forgotten leaves the others.
func TestNupIconCropsAreKept(t *testing.T) {
	web := newTestWeb(t)
	crops := web.nupIconCrops()
	crops.set("a.nup", &CoverCrop{X: 0.1, Y: 0.2, Size: 0.3})
	crops.set("b.nup", &CoverCrop{Fit: true})
	crops.set("a.nup", nil)
	if _, ok := crops.get("a.nup"); ok {
		t.Error("a forgotten crop")
	}
	if crop, ok := web.nupIconCrops().get("b.nup"); !ok || !crop.Fit {
		t.Errorf("%+v %v", crop, ok)
	}
}
