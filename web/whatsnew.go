package web

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// After an update the library says once what is new: the version the app ran before is kept
// in version.json, and the highlights of the new one come from resources/whatsnew.json (in the
// language of the interface).
const (
	VERSION_FILENAME = "version.json"
	// the notice is shown for this long after an update, until it is closed
	whatsNewFor = 30 * 24 * time.Hour
	releasesUrl = "https://github.com/DeLFuS77/switch-library-manager-web/releases/tag/"
)

type versionRecord struct {
	Version  string    `json:"version"`
	Previous string    `json:"previous,omitempty"`
	Updated  time.Time `json:"updated"`
}

// WhatsNew is the notice of an update on the library.
type WhatsNew struct {
	Version string
	Items   []string
	Url     string
}

// recordVersion keeps the version that runs, and the one before when it changed. A new
// installation has none before: it tells nothing. One that ran a version older than the
// record (no version.json yet, but a library database) counts as updated.
func (web *Web) recordVersion() {
	path := filepath.Join(web.dataFolder, VERSION_FILENAME)
	record := versionRecord{}
	if data, err := os.ReadFile(path); err == nil {
		parseStore(path, data, &record)
	} else if _, err := os.Stat(filepath.Join(web.dataFolder, "slm.db")); err == nil {
		record.Version = "?"
	}
	if record.Version == settings.SLM_WEB_VERSION {
		return
	}
	next := versionRecord{Version: settings.SLM_WEB_VERSION, Previous: record.Version, Updated: time.Now()}
	if data, err := json.Marshal(next); err == nil {
		writeFileAtomic(path, data)
	}
}

// whatsNew is the notice of the last update, or nil when there is none to show.
func (web *Web) whatsNew(lang string) *WhatsNew {
	path := filepath.Join(web.dataFolder, VERSION_FILENAME)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	record := versionRecord{}
	if json.Unmarshal(data, &record) != nil || record.Previous == "" || record.Version != settings.SLM_WEB_VERSION || time.Since(record.Updated) > whatsNewFor {
		return nil
	}
	return &WhatsNew{Version: record.Version, Items: web.whatsNewItems(record.Version, lang), Url: releasesUrl + record.Version}
}

// whatsNewItems are the highlights of a version in a language, in English when it has none
// in that language; a version without highlights tells only that the app was updated.
func (web *Web) whatsNewItems(version string, lang string) []string {
	if web.embedFS == nil {
		return nil
	}
	data, err := fs.ReadFile(web.embedFS, "resources/whatsnew.json")
	if err != nil {
		return nil
	}
	highlights := map[string]map[string][]string{}
	if json.Unmarshal(data, &highlights) != nil {
		return nil
	}
	byLang := highlights[version]
	if items := byLang[lang]; len(items) > 0 {
		return items
	}
	return byLang["en"]
}
