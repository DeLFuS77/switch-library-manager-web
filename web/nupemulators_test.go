package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
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
}

// What is told of Eglantine and Lichii is in every language of the app.
func TestNupEmulatorTextsAreTranslated(t *testing.T) {
	texts := append([]string{emulatorsDoNotOpen, emulatorsCannotStart, NOTE_VERIFY_EMULATORS_REFUSE, NOTE_VERIFY_EMULATORS_NO_START,
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
