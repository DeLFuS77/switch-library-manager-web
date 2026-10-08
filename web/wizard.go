package web

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The setup wizard walks an administrator through every setting, one step at a time. It opens
// by itself the first time the app is used (no library yet), can always be closed, put off
// ("Later", asked again the next day) or turned off ("Do not show again"), and can be run again
// from Settings, showing how each option is set now. It saves with the same form and the same
// checks as the Settings page.

const (
	// "Later" puts the wizard off for this long
	wizardSnooze = 24 * time.Hour
	// how many game files the folder check counts at most, and for how long
	wizardCountLimit = 5000
	wizardCountTime  = 2 * time.Second
)

// WizardPageData is what the wizard shows: the settings, as on the Settings page, and whether
// login is on (otherwise its last step offers to create an administrator).
type WizardPageData struct {
	SettingsPageData
	AuthEnabled bool
	// the wizard opened by itself: it offers "Later" and "Do not show again"
	Auto bool
	// a theme chosen by the user (applied in the browser only)
	Themes []string
}

// wizardAuto reports whether the wizard opens by itself: the first use, not put off nor turned off.
func (web *Web) wizardAuto() bool {
	if isDemoMode() {
		return false
	}
	appSettings := settings.ReadSettings(web.dataFolder)
	if appSettings.WizardDone || time.Now().Before(appSettings.WizardLater) {
		return false
	}
	_, localDB := web.state.get()
	return localDB == nil || len(localDB.TitlesMap) == 0
}

// WizardCheck is the answer of a live check of a folder or of the keys.
type WizardCheck struct {
	Ok      bool   `json:"ok"`
	Message string `json:"message"`
	// game files found in a folder (at most wizardCountLimit)
	Games int  `json:"games"`
	More  bool `json:"more"`
}

// countGameFiles counts the game files of a folder and its subfolders, up to a limit and a time.
func countGameFiles(folder string) (int, bool) {
	count := 0
	deadline := time.Now().Add(wizardCountTime)
	stop := false
	filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if count >= wizardCountLimit || time.Now().After(deadline) {
			stop = true
			return filepath.SkipAll
		}
		if !entry.IsDir() {
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".nsp", ".nsz", ".xci", ".xcz":
				count++
			}
		}
		return nil
	})
	return count, stop
}

// checkWizardFolder checks a folder of games: it exists, can be read, and has games.
func checkWizardFolder(folder string, lang string) WizardCheck {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return WizardCheck{Message: translate(lang, "Please provide at least one Folder to scan")}
	}
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return WizardCheck{Message: translatef(lang, "Folder not found: %v", folder)}
	}
	if _, err := os.ReadDir(folder); err != nil {
		return WizardCheck{Message: translate(lang, "The folder exists but cannot be read: check the permissions.")}
	}
	games, more := countGameFiles(folder)
	if games == 0 {
		return WizardCheck{Ok: true, Message: translate(lang, "Found, but there are no game files in it yet.")}
	}
	if more {
		return WizardCheck{Ok: true, Games: games, More: true, Message: translatef(lang, "Found, with more than %v game files.", games)}
	}
	return WizardCheck{Ok: true, Games: games, Message: translatef(lang, "Found, with %v game files.", games)}
}

// checkWizardKeys checks a path to the keys; empty is the default places.
func checkWizardKeys(path string, lang string) WizardCheck {
	path = strings.TrimSpace(path)
	if path == "" {
		if settings.IsKeysFileAvailable() {
			return WizardCheck{Ok: true, Message: translate(lang, "Keys found")}
		}
		return WizardCheck{Message: translate(lang, "Keys not found")}
	}
	keys, err := settings.GetSwitchKeys(path)
	if err != nil {
		return WizardCheck{Message: translatef(lang, "Error trying to read Product Keys (%v)", err)}
	}
	if keys["header_key"] == "" {
		return WizardCheck{Message: translate(lang, "Please provide a valid Product Keys Path")}
	}
	return WizardCheck{Ok: true, Message: translate(lang, "Keys found")}
}

func (web *Web) HandleWizard() {
	templates := web.mustParseTemplates(web.embedFS, "resources/partials/wizard.html")

	web.router.HandleFunc("/wizard", func(w http.ResponseWriter, r *http.Request) {
		current := settings.ReadSettings(web.dataFolder)
		data := WizardPageData{
			SettingsPageData: SettingsPageData{
				GlobalPageData: web.globalPageData("settings"),
				Settings:       current,
				SyncIntervals:  []int{0, 6, 12, 24, 168},
				Languages:      supportedLanguages,
				VerifySpeeds:   verifySpeedOptions(),
				KeysAvailable:  settings.IsKeysFileAvailable(),
				NotificationsConfigured: notificationsConfigured(current.Notifications),
			},
			AuthEnabled: web.auth != nil && web.auth.Enabled(),
			Auto:        r.URL.Query().Get("auto") == "1",
		}
		data.SettingsPageData.GlobalPageData.Auth = web.authInfo(r)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := templates.executeTemplate(w, web.requestLanguage(r), "wizard", data); err != nil {
			web.sugarLogger.Errorf("executing template failed: %v", err)
		}
	}).Methods("GET")

	web.router.HandleFunc("/wizard/check", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		value := r.URL.Query().Get("value")
		if len(value) > 4096 {
			value = value[:4096]
		}
		var result WizardCheck
		switch r.URL.Query().Get("kind") {
		case "folder":
			result = checkWizardFolder(value, lang)
		case "keys":
			result = checkWizardKeys(value, lang)
		default:
			http.Error(w, "unknown check", http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, result)
	}).Methods("GET")

	// "later" puts the wizard off for a day, "done" stops it from opening by itself
	web.router.HandleFunc("/wizard/state", func(w http.ResponseWriter, r *http.Request) {
		switch r.FormValue("state") {
		case "later":
			settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.WizardLater = time.Now().Add(wizardSnooze) })
		case "done":
			settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.WizardDone = true })
		default:
			http.Error(w, "unknown state", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")
}
