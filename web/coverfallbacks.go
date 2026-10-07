package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Some games have no cover in the US store the titles database comes from, or the cover was
// removed there, while another store has one. titles.covers.json, made with the titles
// data, lists those covers.

const COVERS_JSON_FILENAME = "titles.covers.json"

// coverFallback is the cover of a title in another store.
type coverFallback struct {
	IconUrl   string `json:"iconUrl"`
	BannerUrl string `json:"bannerUrl,omitempty"`
}

// downloadCoverFallbacks fetches a newer titles.covers.json; failures keep the saved copy.
func (web *Web) downloadCoverFallbacks() {
	settingsObj := settings.ReadSettings(web.dataFolder)
	path := filepath.Join(web.dataFolder, COVERS_JSON_FILENAME)
	etag := settingsObj.LocalizedTitlesEtags["covers"]
	if _, err := os.Stat(path); err != nil {
		etag = ""
	}
	result, err := db.LoadAndUpdateAllowingEmpty([]string{settings.DEFAULT_COVERS_JSON_URL}, path, etag)
	if err != nil {
		web.sugarLogger.Debugf("Cover fallbacks are not available: %v", err)
		return
	}
	result.File.Close()
	newEtag := result.Etag
	if newEtag != etag {
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			if s.LocalizedTitlesEtags == nil {
				s.LocalizedTitlesEtags = map[string]string{}
			}
			s.LocalizedTitlesEtags["covers"] = newEtag
		})
	}
}

// loadCoverFallbacks reads the saved titles.covers.json, by upper case title ID.
func (web *Web) loadCoverFallbacks() map[string]coverFallback {
	fallbacks := map[string]coverFallback{}
	if data, err := os.ReadFile(filepath.Join(web.dataFolder, COVERS_JSON_FILENAME)); err == nil {
		json.Unmarshal(data, &fallbacks)
	}
	return fallbacks
}

// applyCoverFallbacks gives the titles without a cover the cover of another store, and
// remembers the others for covers that fail to download.
func (web *Web) applyCoverFallbacks(switchDB *db.SwitchTitlesDB) {
	fallbacks := web.loadCoverFallbacks()
	web.fallbackMutex.Lock()
	web.coverFallbacks = fallbacks
	web.fallbackMutex.Unlock()
	if len(fallbacks) == 0 || switchDB == nil {
		return
	}
	for _, title := range switchDB.TitlesMap {
		found, ok := fallbacks[strings.ToUpper(title.Attributes.Id)]
		if !ok {
			continue
		}
		if title.Attributes.IconUrl == "" {
			title.Attributes.IconUrl = found.IconUrl
		}
		if title.Attributes.BannerUrl == "" {
			title.Attributes.BannerUrl = found.BannerUrl
		}
	}
}

// coverFallbackFor returns the cover of another store for a title, if any.
func (web *Web) coverFallbackFor(titleId string) (coverFallback, bool) {
	web.fallbackMutex.Lock()
	defer web.fallbackMutex.Unlock()
	found, ok := web.coverFallbacks[strings.ToUpper(titleId)]
	return found, ok
}
