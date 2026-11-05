package web

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// "I have it" marks a game the user has without a file of it, on a cartridge for instance, of
// the Switch or of the Switch 2: the library shows it as a game without a file, with "My game",
// and it is missing nowhere (Missing games, the suggestions, the series, the counts). Without a
// file the version is not known, so its updates and DLC are not asked for. The marks are of the
// whole library, as its files are, and go in the backups.

func (web *Web) owned() *userdata.Owned {
	web.ownedOnce.Do(func() {
		web.ownedStore = userdata.LoadOwned(filepath.Join(web.dataFolder, userdata.OWNED_FILENAME))
	})
	return web.ownedStore
}

// markedPrefixes are the games marked as owned by the group of their title IDs (see
// db.TitleIDPrefix), with when they were marked.
func (web *Web) markedPrefixes() map[string]time.Time {
	marked := map[string]time.Time{}
	for id, when := range web.owned().Snapshot() {
		if prefix, err := db.TitleIDPrefix(strings.ToLower(id)); err == nil {
			marked[prefix] = when
		}
	}
	return marked
}

// hasGame reports whether the user has the game of a prefix: its file is in the library, or it
// is marked as owned.
func hasGame(localDB *db.LocalSwitchFilesDB, marked map[string]time.Time, prefix string) bool {
	if localDB != nil {
		if local, ok := localDB.TitlesMap[prefix]; ok && local.BaseExist {
			return true
		}
	}
	_, ok := marked[prefix]
	return ok
}

// showsSwitch2 reports whether the games of the Switch 2 the library does not have are shown:
// only for who has the console.
func (web *Web) showsSwitch2() bool {
	return settings.ReadSettings(web.dataFolder).HasSwitch2
}

func (web *Web) HandleOwned() {
	web.router.HandleFunc("/owned", func(w http.ResponseWriter, r *http.Request) {
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		if kind, err := db.ContentKind(id); !titleIdPattern.MatchString(id) || err != nil || kind != db.KIND_BASE {
			writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), "Invalid request")
			return
		}
		owned := r.FormValue("owned") == "true"
		changed, err := web.owned().Set(id, owned)
		if err != nil {
			web.sugarLogger.Errorf("The games marked as owned could not be saved: %v", err)
			writeGlobalError(w, http.StatusInternalServerError, web.requestLanguage(r), "The change could not be saved in the data folder.")
			return
		}
		// a game one has is not wished any more
		if owned && web.wishes().Set(id, false) {
			changed = true
		}
		if changed {
			// the library, the missing games and the counts show it
			web.invalidateDerived()
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "owned": owned, "count": web.owned().Count()})
	}).Methods("POST")
}
