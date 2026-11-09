package web

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
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
	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// noIconNupGame writes a game in a NUP without an icon, as one made of a dump folder without
// control data, and puts it in the library as a scan reads it, with a cover of 400×300 when
// cover is true; it gives its path.
func noIconNupGame(t *testing.T, web *Web, cover bool) string {
	t.Helper()
	nup := testnup.Build([]testnup.Package{{TitleId: 0x0400486024DEC000, Kind: testnup.Base, Version: 65536,
		Meta:  []testnup.Record{{Type: testnup.TitleName, Payload: []byte("Made-up Quest")}},
		Files: []testnup.File{{Path: "exefs/main", Data: []byte("code")}, {Path: "exefs/main.npdm", Data: []byte("META npdm")}}}})
	folder := t.TempDir()
	path := filepath.Join(folder, "Made-up Quest [0400486024DEC000][v65536].nup")
	if err := os.WriteFile(path, nup, 0o644); err != nil {
		t.Fatal(err)
	}
	metadata, err := switchfs.ReadNupMetadata(path)
	if err != nil || metadata["0400486024dec000"] == nil || !metadata["0400486024dec000"].Emulators.NoIcon {
		t.Fatalf("%v %+v", err, metadata)
	}
	game := &db.SwitchGameFiles{BaseExist: true,
		File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(path), BaseFolder: folder, Size: int64(len(nup))},
			Metadata: metadata["0400486024dec000"]},
		Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	if cover {
		picture := image.NewRGBA(image.Rect(0, 0, 400, 300))
		for y := range 300 {
			for x := range 400 {
				picture.Set(x, y, color.RGBA{0, 0, 255, 255})
			}
		}
		var data bytes.Buffer
		jpeg.Encode(&data, picture, nil)
		os.MkdirAll(filepath.Join(web.dataFolder, "img"), 0o755)
		if err := os.WriteFile(filepath.Join(web.dataFolder, "img", "cover.jpg"), data.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		game.Icon = "cover.jpg"
	}
	_, localDB := web.state.get()
	localDB.TitlesMap["0400486024dec"] = game
	return path
}

func postNupIcon(t *testing.T, web *Web, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/title/nup-icon", strings.NewReader(url.Values{"path": {path}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	web.router.ServeHTTP(recorder, request)
	return recorder
}

// The button of the page of a game gives its NUP the cover of the game as its icon, as a task:
// 256×256, the files as they were, its check kept, and no copy left beside it.
func TestNupIconFromTheGamePage(t *testing.T) {
	web, _ := compressWeb(t)
	web.handleNupIcon()
	path := noIconNupGame(t, web, true)
	web.verifications().remember(path, switchfs.FileCheck{})

	recorder := postNupIcon(t, web, path)
	answer := map[string]int{}
	if recorder.Code != http.StatusAccepted || json.Unmarshal(recorder.Body.Bytes(), &answer) != nil || answer["started"] != 1 {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
	task := waitForTask(t, web, tasklog.TASK_NUP_ICON)
	if task.Status != tasklog.TASK_SUCCESS || len(task.Tiles) != 1 || task.Tiles[0].Failed || task.Error != nil {
		t.Fatalf("%+v", task)
	}
	container, err := switchfs.ReadNup(path)
	if err != nil || !container.HasIcon() || container.Emulators().NoIcon {
		t.Fatalf("no icon: %v", err)
	}
	icon, _ := container.Icon()
	if config, err := jpeg.DecodeConfig(bytes.NewReader(icon)); err != nil || config.Width != 256 || config.Height != 256 {
		t.Errorf("%v %+v", err, config)
	}
	if _, err := switchfs.VerifyNup(t.Context(), path, nil); err != nil {
		t.Errorf("verify: %v", err)
	}
	if _, err := os.Stat(switchfs.NupIconBackup(path)); !os.IsNotExist(err) {
		t.Errorf("copy left beside it: %v", err)
	}
	info, _ := os.Stat(path)
	if record, ok := web.verifications().current(path, info); !ok || !record.OK {
		t.Errorf("the check is lost: %+v", record)
	}
	if taskKindNames[tasklog.TASK_NUP_ICON] != NUP_ICON_ACTION || !queueKinds[tasklog.TASK_NUP_ICON] {
		t.Error("the kind is not known everywhere")
	}
}

// Only a NUP of the library without an icon is written.
func TestNupIconTakesOnlyANupWithoutAnIcon(t *testing.T) {
	web, nsp := compressWeb(t)
	web.handleNupIcon()
	path := noIconNupGame(t, web, true)
	for name, requested := range map[string]string{"none": "", "an NSP": nsp, "a folder": filepath.Dir(path)} {
		if recorder := postNupIcon(t, web, requested); recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, recorder.Code, recorder.Body.String())
		}
	}
	_, localDB := web.state.get()
	localDB.TitlesMap["0400486024dec"].File.Metadata.Emulators = &switchfs.NupEmulators{}
	if recorder := postNupIcon(t, web, path); recorder.Code != http.StatusBadRequest {
		t.Errorf("with an icon: %d", recorder.Code)
	}
	for _, task := range web.taskLog().Snapshot() {
		if task.Kind == tasklog.TASK_NUP_ICON {
			t.Fatalf("a task started: %+v", task)
		}
	}
}

// A game without a cover yet: the task fails, says what to do, and the NUP is left as it was.
func TestNupIconWithoutACover(t *testing.T) {
	web, _ := compressWeb(t)
	web.handleNupIcon()
	path := noIconNupGame(t, web, false)
	original, _ := os.ReadFile(path)
	if recorder := postNupIcon(t, web, path); recorder.Code != http.StatusAccepted {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
	task := waitForTask(t, web, tasklog.TASK_NUP_ICON)
	if task.Status != tasklog.TASK_FAILED || task.Error == nil || task.Error.Text != NOTE_NUP_ICON_FAILED ||
		len(task.Warnings) != 1 || task.Warnings[0].Text != NOTE_NUP_ICON_NO_COVER {
		t.Fatalf("%+v", task)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, original) {
		t.Error("the NUP was written")
	}
}

// The page of a game whose NUP has no icon says so and has the button, for administrators.
func TestNupIconButtonOnTheGamePage(t *testing.T) {
	web, _ := compressWeb(t)
	noIconNupGame(t, web, true)
	web.HandleTitle()
	page := func(lang string) string {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/title/0400486024DEC000.html", nil)
		request.Header.Set("Accept-Language", lang)
		web.router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%d", recorder.Code)
		}
		return recorder.Body.String()
	}
	body := page("es")
	if !strings.Contains(body, "data-nup-icon=") || !strings.Contains(body, "Poner icono</button>") ||
		!strings.Contains(body, "No lleva icono: Eglantine y Lichii lo muestran sin imagen.") || !strings.Contains(body, "Convierte la carátula de su juego") {
		t.Fatalf("no button, in Spanish:\n%s", body)
	}
	template, _ := os.ReadFile("../resources/pages/title.html")
	if !strings.Contains(string(template), `{{t "`+NUP_ICON_HELP+`"}}`) || !strings.Contains(string(template), `{{t "`+NUP_ICON_ACTION+`"}}`) {
		t.Error("the page and NUP_ICON_HELP or NUP_ICON_ACTION differ")
	}
	// the emulators open it: their chips stay green
	if !strings.Contains(body, "chip chip-success") {
		t.Error("the emulators are told not to read it")
	}
	_, localDB := web.state.get()
	localDB.TitlesMap["0400486024dec"].File.Metadata.Emulators = &switchfs.NupEmulators{}
	web.invalidateDerived()
	if body := page("en"); strings.Contains(body, "data-nup-icon") {
		t.Errorf("a button on a NUP with an icon:\n%s", body)
	}
}

// What it tells is in every language of the app.
func TestNupIconTextsAreTranslated(t *testing.T) {
	for _, lang := range i18n.SupportedLanguages {
		if lang == i18n.DEFAULT_LANGUAGE {
			continue
		}
		for _, text := range []string{NUP_ICON_ACTION, NUP_ICON_HELP, NUP_NO_ICON, NOTE_NUP_ICON_FAILED, NOTE_NUP_ICON_NO_COVER, NOTE_NUP_ICON_OTHER_COPY, string(errNupIconNotFound)} {
			if i18n.Translate(lang, text) == text {
				t.Errorf("%s: %q", lang, text)
			}
		}
	}
}
