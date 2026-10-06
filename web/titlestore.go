package web

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// titleStore opens the processed copy of the titles database (see db.TitleStore), or
// returns nil when it cannot be used; everything then works from the JSON files.
func (web *Web) titleStore() *db.TitleStore {
	web.storeOnce.Do(func() {
		store, err := db.OpenTitleStore(web.dataFolder)
		if err != nil {
			web.sugarLogger.Warnf("The titles are read from the JSON files: %v", err)
			return
		}
		web.store = store
	})
	return web.store
}

// readTitles loads the titles database: from the processed copy when the JSON files did not
// change, else from the JSON files, keeping the descriptions and screenshots on disk.
func (web *Web) readTitles() (*db.SwitchTitlesDB, error) {
	titlesPath := filepath.Join(web.dataFolder, settings.TITLE_JSON_FILENAME)
	versionsPath := filepath.Join(web.dataFolder, settings.VERSIONS_JSON_FILENAME)
	stamp := db.FileStamp(titlesPath, versionsPath)
	store := web.titleStore()
	if store != nil {
		if switchDB, ok := store.LoadTitles(stamp); ok {
			return switchDB, nil
		}
	}

	titles, err := os.Open(titlesPath)
	if err != nil {
		return nil, err
	}
	defer titles.Close()
	versions, err := os.Open(versionsPath)
	if err != nil {
		return nil, err
	}
	defer versions.Close()
	switchDB, err := db.CreateSwitchTitleDB(titles, versions)
	if err != nil {
		return nil, err
	}
	if store != nil {
		details := db.SplitDetails(switchDB)
		if err := store.SaveTitles(stamp, switchDB, details); err != nil {
			web.sugarLogger.Warnf("The processed titles could not be saved: %v", err)
			restoreDetails(switchDB, details)
		}
	}
	return switchDB, nil
}

// restoreDetails puts the descriptions and screenshots back when they cannot be stored.
func restoreDetails(switchDB *db.SwitchTitlesDB, details map[string]db.TitleDetails) {
	for _, title := range switchDB.TitlesMap {
		if found, ok := details[strings.ToUpper(title.Attributes.Id)]; ok {
			title.Attributes.Description = found.Description
			title.Attributes.Screenshots = found.Screenshots
		}
	}
}

// readLocalizedTitles loads the names of a language, keeping the descriptions on disk.
func (web *Web) readLocalizedTitles(lang string, path string) (map[string]db.LocalizedTitle, error) {
	stamp := db.FileStamp(path)
	store := web.titleStore()
	if store != nil {
		if titles, ok := store.LoadLocalized(lang, stamp); ok {
			return titles, nil
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	titles, err := db.LoadLocalizedTitles(file)
	if err != nil {
		return nil, err
	}
	if store != nil {
		descriptions := db.SplitLocalizedDescriptions(titles)
		if err := store.SaveLocalized(lang, stamp, titles, descriptions); err != nil {
			web.sugarLogger.Warnf("The processed titles in %v could not be saved: %v", lang, err)
			for id, found := range descriptions {
				title := titles[id]
				title.Description = found.Description
				titles[id] = title
			}
		}
	}
	return titles, nil
}

// titleDetails returns the description and screenshots of a title, in the language when
// a translation exists.
func (web *Web) titleDetails(title *db.SwitchTitle, lang string) db.TitleDetails {
	details := db.TitleDetails{Description: title.Attributes.Description, Screenshots: title.Attributes.Screenshots}
	store := web.titleStore()
	if store != nil && details.Description == "" && len(details.Screenshots) == 0 {
		details = store.Details(title.Attributes.Id)
	}
	if store != nil && lang != DEFAULT_LANGUAGE {
		if description := store.LocalizedDescription(lang, title.Attributes.Id); description != "" {
			details.Description = description
		}
	}
	return details
}
