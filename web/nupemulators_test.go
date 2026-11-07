package web

import (
	"net/http"
	"net/http/httptest"
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

// The page of a game in a NUP tells whether Eglantine and Lichii open it and can start its game:
// their names in green, or in orange with what keeps them from it, translated.
func TestNupGamePageTellsTheEmulators(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	withSwitch2Game(t, localDB)
	game := localDB.TitlesMap["0400486024dec"]
	switchDB.TitlesMap["0400486024dec"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0400486024DEC000", Name: "Made-up Quest"}}
	web.state.set(switchDB, localDB)
	web.embedFS = os.DirFS("..")
	web.HandleTitle()
	page := func(lang string) string {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/title/0400486024DEC000.html", nil)
		request.Header.Set("Accept-Language", lang)
		web.router.ServeHTTP(recorder, request)
		return recorder.Body.String()
	}

	// a NUP read by a version that did not tell it says nothing
	if detail, _ := web.getTitleDetail("0400486024DEC000", "en"); detail.Base.Emulators != nil {
		t.Fatalf("not told: %+v", detail.Base.Emulators)
	}
	if strings.Contains(page("en"), "nup-emulators") {
		t.Fatal("no emulators on the page of a NUP not read for them")
	}

	// they read it well
	game.File.Metadata.Emulators = &switchfs.NupEmulators{}
	if detail, _ := web.getTitleDetail("0400486024DEC000", "en"); detail.Base.Emulators == nil || !detail.Base.Emulators.Ok ||
		len(detail.Base.Emulators.Warnings) != 0 {
		t.Fatalf("they read it: %+v", detail.Base.Emulators)
	}
	body := page("es")
	if !strings.Contains(body, "Emuladores") || !strings.Contains(body, `<span class="chip chip-success"><span class="bi bi-check-circle" aria-hidden="true"></span>Eglantine</span>`) ||
		strings.Contains(body, "nup-emulator-warning") {
		t.Fatalf("green names:\n%s", body)
	}

	// they do not open it
	game.File.Metadata.Emulators = &switchfs.NupEmulators{Refused: []switchfs.NupEmulatorIssue{
		{Text: "package %v is of kind %v, which the format keeps for later", Args: []string{"2", "3"}},
		{Text: "its folders go more than 128 deep"}}}
	detail, _ := web.getTitleDetail("0400486024DEC000", "en")
	if detail.Base.Emulators.Ok || len(detail.Base.Emulators.Warnings) != 1 || detail.Base.Emulators.Warnings[0] !=
		"Eglantine and Lichii do not open it: package 2 is of kind 3, which the format keeps for later; its folders go more than 128 deep." {
		t.Fatalf("refused: %+v", detail.Base.Emulators)
	}
	body = page("es")
	if !strings.Contains(body, `<span class="chip chip-warning"><span class="bi bi-exclamation-triangle" aria-hidden="true"></span>Lichii</span>`) ||
		!strings.Contains(body, "Eglantine y Lichii no lo abren: el paquete 2 es de tipo 3, que el formato reserva para más adelante; sus carpetas pasan de 128 niveles.") {
		t.Fatalf("orange names and the reason, in Spanish:\n%s", body)
	}

	// they cannot start it
	game.File.Metadata.Emulators = &switchfs.NupEmulators{NoStart: &switchfs.NupEmulatorIssue{Text: "the ExeFS of update v%v lacks: %v",
		Args: []string{"131072", "main.npdm"}}}
	if detail, _ := web.getTitleDetail("0400486024DEC000", "de"); detail.Base.Emulators.Ok || len(detail.Base.Emulators.Warnings) != 1 ||
		detail.Base.Emulators.Warnings[0] != "Eglantine und Lichii können es nicht starten: dem ExeFS von Update v131072 fehlt: main.npdm." {
		t.Fatalf("no start: %+v", detail.Base.Emulators)
	}

	// they open it, but show its name, version and icon wrong
	game.File.Metadata.Emulators = &switchfs.NupEmulators{ZeroBased: true}
	if detail, _ := web.getTitleDetail("0400486024DEC000", "en"); detail.Base.Emulators.Ok || len(detail.Base.Emulators.Warnings) != 1 ||
		detail.Base.Emulators.Warnings[0] != emulatorsZeroBased {
		t.Fatalf("counted from 0: %+v", detail.Base.Emulators)
	}
	body = page("es")
	if !strings.Contains(body, `<span class="chip chip-warning"><span class="bi bi-exclamation-triangle" aria-hidden="true"></span>Eglantine</span>`) ||
		!strings.Contains(body, "Eglantine y Lichii muestran mal su nombre, su versión y su icono: los tipos de sus metadatos se cuentan desde 0") {
		t.Fatalf("counted from 0, in Spanish:\n%s", body)
	}
}

// A check of the files tells a NUP counted from 0 on an orange line of its own, its cover sound.
func TestVerifyTellsANupCountedFromZero(t *testing.T) {
	web, _ := compressWeb(t)
	web.HandleVerify()
	web.HandleTasks()
	nup := testnup.Build([]testnup.Package{{TitleId: 0x0400486024DEC000, Kind: testnup.Base, Version: 65536,
		Meta:  []testnup.Record{{Type: 0, Payload: []byte("META npdm")}, {Type: 4, Payload: []byte("Made-up Quest")}},
		Files: []testnup.File{{Path: "exefs/main", Data: []byte("code")}, {Path: "exefs/main.npdm", Data: []byte("META npdm")}}}})
	folder := t.TempDir()
	path := filepath.Join(folder, "Made-up Quest [0400486024DEC000][v65536].nup")
	if err := os.WriteFile(path, nup, 0o644); err != nil {
		t.Fatal(err)
	}
	_, localDB := web.state.get()
	localDB.TitlesMap["0400486024dec"] = &db.SwitchGameFiles{BaseExist: true,
		File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(path), BaseFolder: folder, Size: int64(len(nup))},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: "0400486024dec000", Version: 65536, Type: "BASE"}},
		Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	web.startVerificationAt(tasklog.VERIFY_LEVEL_HASH, []string{path}, verifyRecheckAll, tasklog.TRIGGER_MANUAL)
	task := waitForTask(t, web, tasklog.TASK_VERIFY)
	if task.Damaged != 0 || task.Status != tasklog.TASK_WARNING || len(task.Warnings) != 1 ||
		task.Warnings[0].Text != NOTE_VERIFY_EMULATORS_ZERO_BASED || !strings.HasPrefix(task.Warnings[0].Detail, filepath.Base(path)+": ") ||
		len(task.Tiles) != 1 || task.Tiles[0].Failed || task.Tiles[0].Modified {
		t.Fatalf("counted from 0: %+v", task)
	}
}

// What is told of Eglantine and Lichii is in every language of the app.
func TestNupEmulatorTextsAreTranslated(t *testing.T) {
	texts := append([]string{emulatorsDoNotOpen, emulatorsCannotStart, emulatorsZeroBased, NOTE_VERIFY_EMULATORS_REFUSE,
		NOTE_VERIFY_EMULATORS_NO_START, NOTE_VERIFY_EMULATORS_ZERO_BASED,
		"failed to read %v [reason: %v]; Eglantine and Lichii do not open it either", "Emulators", "They open it and can start its game."},
		switchfs.NupEmulatorTexts...)
	for _, lang := range i18n.SupportedLanguages {
		if lang == i18n.DEFAULT_LANGUAGE {
			continue
		}
		for _, text := range texts {
			translated := i18n.Translations[lang][text]
			if translated == "" || strings.Count(translated, "%v") != strings.Count(text, "%v") {
				t.Errorf("%s: %q is %q", lang, text, translated)
			}
		}
	}
	// a NUP the library does not read for a reason they share, in Issues
	got := i18n.TranslateIssue("es", "failed to read NUP [reason: NUP flags 0x1 are not supported]; "+switchfs.NupEmulatorsRefuseToo)
	if got != "no se pudo leer el NUP [motivo: NUP flags 0x1 are not supported]; Eglantine y Lichii tampoco lo abren" {
		t.Errorf("issue: %q", got)
	}
	if got := i18n.TranslateIssue("es", "failed to read NUP [reason: NUP header is too small]"); got != "no se pudo leer el NUP [motivo: NUP header is too small]" {
		t.Errorf("issue of any other NUP: %q", got)
	}
}
