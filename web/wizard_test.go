package web

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The wizard saves with the form of the Settings page: every field of that form must be in
// the wizard, shown or kept hidden, or saving would reset the settings it does not send.
func TestWizardSendsEveryField(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleWizard()
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest("GET", "/wizard", nil))
	if recorder.Code != 200 {
		t.Fatalf("the wizard: %d", recorder.Code)
	}
	page := recorder.Body.String()
	form := reflect.TypeOf(SettingsForm{})
	for i := 0; i < form.NumField(); i++ {
		tag := form.Field(i).Tag.Get("in")
		name := strings.TrimPrefix(strings.Split(tag, ";")[0], "form=")
		if name == "" {
			continue
		}
		if !strings.Contains(page, `name="`+name+`"`) {
			t.Errorf("the wizard does not send %q", name)
		}
	}
	if !strings.Contains(page, `id="setupWizard"`) {
		t.Fatal("the wizard markup")
	}
}

func TestWizardOpensByItselfOnlyTheFirstTime(t *testing.T) {
	web := newTestWeb(t)
	if !web.wizardAuto() {
		t.Fatal("a new app without a library opens the wizard")
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.WizardLater = time.Now().Add(time.Hour) })
	if web.wizardAuto() {
		t.Fatal("put off")
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.WizardLater = time.Time{}; s.WizardDone = true })
	if web.wizardAuto() {
		t.Fatal("finished or turned off")
	}

	// an app already in use (it has a library) does not open it after an update
	used := newTestWeb(t)
	used.state.set(testDatabases(t))
	if used.wizardAuto() {
		t.Fatal("a library already there")
	}
}

func TestWizardState(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleWizard()
	post := func(state string) int {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/wizard/state", strings.NewReader(url.Values{"state": {state}}.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		web.router.ServeHTTP(recorder, request)
		return recorder.Code
	}
	if code := post("later"); code != 204 || settings.ReadSettings(web.dataFolder).WizardLater.Before(time.Now().Add(23*time.Hour)) {
		t.Fatalf("later: %d", code)
	}
	if code := post("done"); code != 204 || !settings.ReadSettings(web.dataFolder).WizardDone {
		t.Fatalf("done: %d", code)
	}
	if code := post("nonsense"); code != 400 {
		t.Fatalf("unknown: %d", code)
	}
}

func TestWizardChecksAFolder(t *testing.T) {
	folder := t.TempDir()
	if result := checkWizardFolder(folder, "en"); !result.Ok || result.Games != 0 {
		t.Fatalf("an empty folder: %+v", result)
	}
	os.MkdirAll(filepath.Join(folder, "sub"), 0755)
	for _, name := range []string{"a.nsp", "sub/b.NSZ", "c.txt"} {
		os.WriteFile(filepath.Join(folder, name), []byte("x"), 0644)
	}
	if result := checkWizardFolder(folder, "en"); !result.Ok || result.Games != 2 || !strings.Contains(result.Message, "2") {
		t.Fatalf("two games: %+v", result)
	}
	if result := checkWizardFolder(filepath.Join(folder, "missing"), "en"); result.Ok {
		t.Fatalf("a missing folder: %+v", result)
	}
	if result := checkWizardFolder("  ", "en"); result.Ok {
		t.Fatal("no folder")
	}
}
