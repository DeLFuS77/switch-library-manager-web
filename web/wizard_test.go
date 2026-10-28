package web

import (
	"net/http"
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
	// saved by the wizard: the covers are downloaded now, whatever the background hours
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.BackgroundHours = backgroundHoursNotNow(time.Now()) })
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/wizard/state", strings.NewReader("state=done&covers=1"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	web.router.ServeHTTP(recorder, request)
	if recorder.Code != 204 || !web.backgroundAllowed() {
		t.Fatalf("covers now: %d, allowed %v", recorder.Code, web.backgroundAllowed())
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

// The administrator of the wizard is checked before the settings are saved: a password the
// app refuses made "Save and start" fail again and again without a reason.
func TestWizardChecksTheAdministratorFirst(t *testing.T) {
	cases := []struct {
		name, password string
		ok             bool
	}{
		{"", "", true},
		{"admin", "", false},
		{"", "alpha-secret-11", false},
		{"admin", "short", false},
		{"admin", "password123", false},
		{"bad name!", "alpha-secret-11", false},
		{"admin", "alpha-secret-11", true},
	}
	for _, c := range cases {
		if got := checkWizardAdmin(c.name, c.password, "es"); got.Ok != c.ok || (!got.Ok && got.Message == "") {
			t.Errorf("%q / %q: %+v", c.name, c.password, got)
		}
	}

	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleWizard()
	request := httptest.NewRequest("POST", "/wizard/admin", strings.NewReader(url.Values{"name": {"admin"}, "password": {"short"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, request)
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `"ok":false`) {
		t.Fatalf("the check of a short password: %d %s", recorder.Code, recorder.Body.String())
	}
	// only administrators see the folders of the server or check an administrator
	for _, path := range []string{"/wizard/folders", "/wizard/admin"} {
		if roleAllows(ROLE_EDITOR, httptest.NewRequest("GET", path, nil)) {
			t.Errorf("%s for an editor", path)
		}
	}
}

// The folder browser lists the subfolders of a folder of the server, the hidden ones left out,
// with the way up and the places to start from.
func TestWizardBrowsesTheFoldersOfTheServer(t *testing.T) {
	folder := t.TempDir()
	for _, name := range []string{"Switch", "backups", ".hidden", "$RECYCLE.BIN"} {
		os.Mkdir(filepath.Join(folder, name), 0o755)
	}
	os.WriteFile(filepath.Join(folder, "game.nsp"), []byte("x"), 0o644)
	got := browseFolders(folder, "en")
	if got.Error != "" || got.Path != filepath.Clean(folder) || got.Parent != filepath.Dir(filepath.Clean(folder)) {
		t.Fatalf("browse: %+v", got)
	}
	if !reflect.DeepEqual(got.Folders, []string{"backups", "Switch"}) {
		t.Fatalf("the subfolders, in order and without hidden ones or files: %v", got.Folders)
	}
	if len(got.Places) == 0 {
		t.Fatal("places to start from")
	}
	if missing := browseFolders(filepath.Join(folder, "missing"), "en"); missing.Error == "" {
		t.Fatal("a missing folder is told")
	}
	if relative := browseFolders("games", "en"); relative.Error == "" {
		t.Fatal("a relative path is refused")
	}
	if start := browseFolders("", "en"); start.Path == "" {
		t.Fatalf("an empty path starts at the first place: %+v", start)
	}
}

// The order chosen in the wizard sets the options of Organize it is about and leaves the others,
// and the subfolders named by the user keep their names.
func TestWizardOrganize(t *testing.T) {
	current := settings.OrganizeOptions{FolderNameTemplate: "{TITLE_NAME}", FoldersWithIds: true, UpdatesFolder: "Patches"}
	sub := wizardOrganize(current, WIZARD_LAYOUT_SUB, true, true, true, settings.KEEP_AUTO)
	if !sub.CreateFolderPerGame || sub.BaseFolder != "Base" || sub.UpdatesFolder != "Patches" || sub.DlcFolder != "DLC" {
		t.Fatalf("the subfolders: %+v", sub)
	}
	if !sub.KeepGameFolders || !sub.RenameFiles || !sub.DeleteEmptyFolders || sub.KeepDuplicate != settings.KEEP_AUTO || !sub.FoldersWithIds || sub.FolderNameTemplate != "{TITLE_NAME}" {
		t.Fatalf("the other options: %+v", sub)
	}
	if wizardLayout(sub) != WIZARD_LAYOUT_SUB {
		t.Fatalf("the layout of the subfolders: %q", wizardLayout(sub))
	}
	folder := wizardOrganize(sub, WIZARD_LAYOUT_FOLDER, false, false, false, "")
	if !folder.CreateFolderPerGame || folder.BaseFolder != "" || folder.UpdatesFolder != "" || folder.DlcFolder != "" || folder.RenameFiles || wizardLayout(folder) != WIZARD_LAYOUT_FOLDER {
		t.Fatalf("a folder per game: %+v", folder)
	}
	none := wizardOrganize(sub, WIZARD_LAYOUT_NONE, true, false, false, "")
	if none.CreateFolderPerGame || none.BaseFolder != "" || wizardLayout(none) != WIZARD_LAYOUT_NONE {
		t.Fatalf("the files as they are: %+v", none)
	}
	if !validWizardOrganize("", "anything") || !validWizardOrganize(WIZARD_LAYOUT_SUB, settings.KEEP_NSP) {
		t.Fatal("valid orders")
	}
	if validWizardOrganize("tree", "") || validWizardOrganize(WIZARD_LAYOUT_FOLDER, "biggest") {
		t.Fatal("an unknown order or copy is refused")
	}
}

// Saving from the wizard applies the order; saving the Settings page, which has no order, leaves
// the options of Organize as they are.
func TestWizardSavesTheOrder(t *testing.T) {
	web, _ := compressWeb(t)
	web.HandleSettings()
	post := func(form url.Values) {
		request := httptest.NewRequest(http.MethodPost, "/settings.html", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("saving: %d %s", recorder.Code, recorder.Body.String())
		}
	}
	folder := t.TempDir()
	post(url.Values{"scan_folders": {folder}, "organize_layout": {"sub"}, "keep_game_folders": {"true"}, "rename_files": {"true"}, "keep_duplicate": {"auto"}})
	options := settings.ReadSettings(web.dataFolder).OrganizeOptions
	if !options.CreateFolderPerGame || options.DlcFolder != "DLC" || !options.KeepGameFolders || !options.RenameFiles || options.KeepDuplicate != "auto" {
		t.Fatalf("the order of the wizard: %+v", options)
	}
	post(url.Values{"scan_folders": {folder}})
	if again := settings.ReadSettings(web.dataFolder).OrganizeOptions; !again.CreateFolderPerGame || !again.RenameFiles || again.KeepDuplicate != "auto" {
		t.Fatalf("the Settings page changed the order: %+v", again)
	}
}

// The test notification of the wizard uses the channels written in it, checked as when saving,
// before they are saved.
func TestWizardTestsTheChannelsWritten(t *testing.T) {
	web := newTestWeb(t)
	web.HandleNotifications()
	send := func(form url.Values) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/notifications/test", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, request)
		return recorder
	}
	if recorder := send(url.Values{"draft": {"1"}}); recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "Write a channel first.") {
		t.Fatalf("no channel: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := send(url.Values{"draft": {"1"}, "discord_webhook_url": {"https://example.com/hook"}}); recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "Discord") {
		t.Fatalf("a wrong Discord address: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := send(url.Values{"draft": {"1"}, "telegram_bot_token": {"123:abc"}}); recorder.Code != http.StatusBadRequest {
		t.Fatalf("a token without its chat: %d %s", recorder.Code, recorder.Body.String())
	}
}
