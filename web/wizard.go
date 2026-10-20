package web

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
	// the app runs in a container: its folders are the paths inside it
	InContainer bool
	// a folder of games written the way of the system of the server, as an example
	FolderExample string
}

// runningInContainer tells an app started by Docker or Podman.
func runningInContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

// folderExample is a folder of games as the system of the server writes it.
func folderExample(inContainer bool) string {
	switch {
	case inContainer:
		return "/mnt/roms"
	case runtime.GOOS == "windows":
		return `D:\Switch`
	case runtime.GOOS == "darwin":
		return "/Volumes/Games/Switch"
	}
	return "/mnt/roms"
}

// WizardFolders is a folder of the server with its subfolders, for the folder browser.
type WizardFolders struct {
	Path    string   `json:"path"`
	Parent  string   `json:"parent,omitempty"`
	Folders []string `json:"folders"`
	// where to start: the drives on Windows, the root and the usual places elsewhere
	Places []string `json:"places"`
	// more subfolders than are listed
	More  bool   `json:"more,omitempty"`
	Error string `json:"error,omitempty"`
}

// wizardFolderLimit is how many subfolders the browser lists at most.
const wizardFolderLimit = 500

// browsePlaces are the places the folder browser starts from: the drives of Windows, or the
// root, the home folder and the usual places of disks and shares.
func browsePlaces() []string {
	places := []string{}
	if runtime.GOOS == "windows" {
		for letter := 'A'; letter <= 'Z'; letter++ {
			drive := string(letter) + `:\`
			if _, err := os.Stat(drive); err == nil {
				places = append(places, drive)
			}
		}
		return places
	}
	places = append(places, "/")
	candidates := []string{"/mnt", "/media", "/Volumes", "/volume1", "/share", "/data"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append([]string{home}, candidates...)
	}
	for _, place := range candidates {
		if info, err := os.Stat(place); err == nil && info.IsDir() {
			places = append(places, place)
		}
	}
	return places
}

// browseFolders lists the subfolders of a folder of the server, hidden ones left out; an empty
// path starts at the first place.
func browseFolders(path string, lang string) WizardFolders {
	result := WizardFolders{Places: browsePlaces(), Folders: []string{}}
	path = strings.TrimSpace(path)
	if path == "" && len(result.Places) > 0 {
		path = result.Places[0]
	}
	path = filepath.Clean(path)
	// only inside the places it starts from: a drive on Windows, the root elsewhere (a network
	// path of Windows is written by hand instead)
	inside := false
	for _, place := range result.Places {
		if strings.HasPrefix(path, filepath.Clean(place)) {
			inside = true
		}
	}
	if !filepath.IsAbs(path) || !inside {
		result.Error = translatef(lang, "Folder not found: %v", path)
		return result
	}
	result.Path = path
	if parent := filepath.Dir(path); parent != path {
		result.Parent = parent
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			result.Error = translatef(lang, "Folder not found: %v", path)
		} else {
			result.Error = translate(lang, "The folder exists but cannot be read: check the permissions.")
		}
		return result
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") {
			continue
		}
		// a link to a folder counts as a folder
		if !entry.IsDir() {
			if info, err := os.Stat(filepath.Join(path, name)); err != nil || !info.IsDir() {
				continue
			}
		}
		if len(result.Folders) == wizardFolderLimit {
			result.More = true
			break
		}
		result.Folders = append(result.Folders, name)
	}
	sort.Slice(result.Folders, func(i, j int) bool {
		return strings.ToLower(result.Folders[i]) < strings.ToLower(result.Folders[j])
	})
	return result
}

// checkWizardAdmin checks the first administrator the wizard creates, before anything is saved:
// a wrong password made the last step fail again and again with no reason given.
func checkWizardAdmin(name string, password string, lang string) WizardCheck {
	name = strings.TrimSpace(name)
	if name == "" && password == "" {
		return WizardCheck{Ok: true}
	}
	if name == "" || password == "" {
		return WizardCheck{Message: translate(lang, "Fill in the user name and the password, or leave both empty.")}
	}
	if !validUserName.MatchString(name) {
		return WizardCheck{Message: translate(lang, ErrUserName.Error())}
	}
	if err := checkPassword(name, password); err != nil {
		return WizardCheck{Message: translate(lang, err.Error())}
	}
	return WizardCheck{Ok: true}
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
		inContainer := runningInContainer()
		data := WizardPageData{
			SettingsPageData: web.settingsPageData(),
			AuthEnabled:      web.auth != nil && web.auth.Enabled(),
			Auto:             r.URL.Query().Get("auto") == "1",
			InContainer:      inContainer,
			FolderExample:    folderExample(inContainer),
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
		case "folders":
			// a folder twice, or one inside another: what Settings refuse when saving
			message := repeatedFolders(SplitAndTrimSpaceArray(value, "\n"), lang)
			result = WizardCheck{Ok: message == "", Message: message}
		default:
			http.Error(w, "unknown check", http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, result)
	}).Methods("GET")

	// the folders of the server, to choose the folder of the games instead of writing it
	web.router.HandleFunc("/wizard/folders", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if len(path) > 4096 {
			path = path[:4096]
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, browseFolders(path, web.requestLanguage(r)))
	}).Methods("GET")

	// the administrator is checked before the settings are saved; the password goes in the
	// body of a POST, never in an address
	web.router.HandleFunc("/wizard/admin", func(w http.ResponseWriter, r *http.Request) {
		result := checkWizardAdmin(r.FormValue("name"), r.FormValue("password"), web.requestLanguage(r))
		if result.Ok && strings.TrimSpace(r.FormValue("name")) != "" && web.auth != nil && web.auth.Enabled() {
			result = WizardCheck{Message: translate(web.requestLanguage(r), "Login is on")}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, result)
	}).Methods("POST")

	// "later" puts the wizard off for a day, "done" stops it from opening by itself
	web.router.HandleFunc("/wizard/state", func(w http.ResponseWriter, r *http.Request) {
		switch r.FormValue("state") {
		case "later":
			settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.WizardLater = time.Now().Add(wizardSnooze) })
		case "done":
			settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.WizardDone = true })
			// saved by the wizard: the covers of the games it finds are downloaded at once, also
			// outside the background hours, like "Search covers again"
			if r.FormValue("covers") == "1" {
				web.background.mutex.Lock()
				web.background.forced = true
				web.background.mutex.Unlock()
				// the scan the saved settings started downloads them when it ends
				if !web.state.IsSynchronizing() {
					web.startCoverDownloads()
				}
			}
		default:
			http.Error(w, "unknown state", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")
}
