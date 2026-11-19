package web

import (
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// An older update can be kept on purpose: an emulator may run a game better at an older
// version, or a mod may need it (#193). Keep this update, on the page of the game, in Issues and
// in Space, keeps that version of the game: Delete old updates, the automations and Space leave
// it where it is, Issues no longer lists it, and Compress offers it. A version is kept by the ID
// of the update and its number, not by its path, so it stays kept when Organize moves or renames
// the file, or when it is compressed. A second copy of the same version is still a duplicate.

const NOTE_KEPT_BY_CHOICE = "You keep this version: it is not deleted."

// keptUpdateKey is how a version kept is written in the settings: "0100ABCD00010800:65536".
func keptUpdateKey(id string, version int) string {
	return strings.ToUpper(id) + ":" + strconv.Itoa(version)
}

// updateKeyOf is the key of a file of an update, "" for any other file (a game, a DLC, a file
// whose ID and version cannot be read).
func updateKeyOf(file db.ExtendedFileInfo) string {
	id, version, ok := updateOf(file)
	if !ok {
		return ""
	}
	if kind, err := db.ContentKind(id); err != nil || kind != db.KIND_UPDATE {
		return ""
	}
	return keptUpdateKey(id, version)
}

// keptByChoice tells whether a file of an update is a version kept on purpose.
func (web *Web) keptByChoice(file db.ExtendedFileInfo) bool {
	kept := settings.ReadSettings(web.dataFolder).OrganizeOptions.KeepUpdates
	if len(kept) == 0 {
		return false
	}
	key := updateKeyOf(file)
	return key != "" && slices.Contains(kept, key)
}

// oldUpdateFile is the old update of the library at path, as the scan listed it: not one that
// also holds what the library keeps (the game, its latest update, a DLC), which no cleanup takes.
func (web *Web) oldUpdateFile(path string) (db.ExtendedFileInfo, bool) {
	_, localDB := web.state.get()
	if localDB == nil {
		return db.ExtendedFileInfo{}, false
	}
	inUse := process.FilesInUse(localDB)
	for file, skipped := range localDB.Skipped {
		found := filepath.Join(file.BaseFolder, file.FileName)
		if skipped.ReasonCode == db.REASON_OLD_UPDATE && found == path && !inUse[found] && updateKeyOf(file) != "" {
			return file, true
		}
	}
	return db.ExtendedFileInfo{}, false
}

// handleKeepUpdates adds the action that keeps a version of an update, or stops keeping it. Only
// an administrator changes it, as any setting.
func (web *Web) handleKeepUpdates() {
	web.router.HandleFunc("/updates/keep", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		keep := r.FormValue("keep") != "false"
		// an old update of the library, never a path of the request as it is; one kept already
		// can be let go also when it is no longer old (a newer update was deleted)
		key := ""
		if file, ok := web.oldUpdateFile(r.FormValue("path")); ok {
			key = updateKeyOf(file)
		} else if !keep {
			key = web.libraryUpdateKey(r.FormValue("path"))
		}
		if key == "" {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		if _, err := settings.TryUpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			kept := slices.DeleteFunc(slices.Clone(s.OrganizeOptions.KeepUpdates), func(other string) bool { return other == key })
			if keep {
				kept = append(kept, key)
			}
			slices.Sort(kept)
			s.OrganizeOptions.KeepUpdates = kept
		}); err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, i18n.Translatef(lang, "The settings could not be saved: %v", err))
			return
		}
		// Issues, Space and Compress tell it at once
		web.invalidateDerived()
		writeJSON(w, http.StatusOK, map[string]any{"kept": keep})
	}).Methods("POST")
}

// libraryUpdateKey is the key of an update file of the library at path, "" when there is none.
func (web *Web) libraryUpdateKey(path string) string {
	_, localDB := web.state.get()
	if localDB == nil {
		return ""
	}
	for _, title := range localDB.TitlesMap {
		for _, update := range title.Updates {
			if filepath.Join(update.ExtendedInfo.BaseFolder, update.ExtendedInfo.FileName) == path {
				return updateKeyOf(update.ExtendedInfo)
			}
		}
	}
	return ""
}
