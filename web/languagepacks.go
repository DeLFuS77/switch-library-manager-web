package web

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/dlclang"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The language packs and the commentary of a game in the languages not chosen in Settings that
// are already in the library can be deleted from Space (#200). They are a group of their own,
// none selected at first: only the ones the user selects go, after the confirmation, never in a
// cleanup of Organize or of the automations. A pack whose language is not known (several
// languages, or none in its name) is never offered, nor a file that also holds something else,
// nor a DLC kept on purpose with Keep this DLC, which is kept by its title ID.

const (
	SPACE_LANGUAGES = "languages"
	// why a file went to the quarantine
	QUARANTINE_LANGUAGE = "language"

	NOTE_DLC_KEPT         = "You keep this DLC: it is not deleted."
	NOTE_LANGUAGE_UNKNOWN = "The language of this pack is not known: it stays."
)

// keptDLC are the DLC kept on purpose, by their title ID in upper case.
func keptDLC(appSettings *settings.AppSettings) map[string]bool {
	kept := map[string]bool{}
	for _, id := range appSettings.OrganizeOptions.KeepDlc {
		kept[strings.ToUpper(strings.TrimSpace(id))] = true
	}
	return kept
}

// languagePackFiles are the files of the language packs in the languages not chosen, none while
// no language is chosen: the ones that can be deleted, and the ones that stay with the reason.
func (web *Web) languagePackFiles() []SpaceFile {
	files := []SpaceFile{}
	switchDB, localDB := web.state.get()
	if switchDB == nil || localDB == nil {
		return files
	}
	appSettings := settings.ReadSettings(web.dataFolder)
	chosen := dlclang.Chosen(appSettings.DLCLanguages)
	if len(chosen) == 0 {
		return files
	}
	kept := keptDLC(appSettings)

	// how many contents the library takes from each file: a file with the game, an update or
	// another DLC in it too is never deleted for one language pack
	uses := map[string]int{}
	count := func(file db.ExtendedFileInfo) {
		uses[filepath.Join(file.BaseFolder, file.FileName)]++
	}
	for _, title := range localDB.TitlesMap {
		if title.BaseExist {
			count(title.File.ExtendedInfo)
		}
		if update, ok := title.Updates[title.LatestUpdate]; ok {
			count(update.ExtendedInfo)
		}
		for _, dlc := range title.Dlc {
			count(dlc.ExtendedInfo)
		}
	}

	seen := map[string]bool{}
	for prefix, local := range localDB.TitlesMap {
		known := switchDB.TitlesMap[prefix]
		if known == nil {
			continue
		}
		for id, dlc := range local.Dlc {
			attributes, ok := known.Dlc[id]
			if !ok {
				continue
			}
			code, pack := dlclang.Language(attributes.Name)
			if !pack || (code != "" && chosen[code]) {
				continue
			}
			path := filepath.Join(dlc.ExtendedInfo.BaseFolder, dlc.ExtendedInfo.FileName)
			if seen[path] {
				continue
			}
			seen[path] = true
			entry := SpaceFile{Path: path, Name: attributes.Name, Size: dlc.ExtendedInfo.Size, Deletable: true,
				DlcId: strings.ToUpper(id), Language: dlclang.Names[code], file: dlc.ExtendedInfo}
			switch {
			case code == "":
				entry.Deletable, entry.Note = false, NOTE_LANGUAGE_UNKNOWN
			case uses[path] > 1:
				entry.Deletable, entry.Note = false, process.REASON_IN_USE
			default:
				entry.KeepableDLC = true
				if kept[entry.DlcId] {
					entry.Deletable, entry.Note, entry.KeptDLC = false, NOTE_DLC_KEPT, true
				}
			}
			files = append(files, entry)
		}
	}
	return files
}

// languagePacksSummary is the number and the size of the language packs that can be deleted, for
// the line of Settings that leads to Space.
func (web *Web) languagePacksSummary() (int, int64) {
	count, size := 0, int64(0)
	for _, file := range web.languagePackFiles() {
		if file.Deletable {
			count++
			size += file.Size
		}
	}
	return count, size
}

// libraryHasDLC tells whether a DLC of that title ID is in the library.
func (web *Web) libraryHasDLC(id string) bool {
	_, localDB := web.state.get()
	if localDB == nil {
		return false
	}
	for _, title := range localDB.TitlesMap {
		for other := range title.Dlc {
			if strings.EqualFold(other, id) {
				return true
			}
		}
	}
	return false
}

// handleKeepDLC adds the action that keeps a DLC, or stops keeping it. Only an administrator
// changes it, as any setting.
func (web *Web) handleKeepDLC() {
	web.router.HandleFunc("/dlc/keep", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		keep := r.FormValue("keep") != "false"
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		// a DLC of the library; one kept already can be let go also when it is no longer there
		if !titleIdPattern.MatchString(id) || (keep && !web.libraryHasDLC(id)) {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		if _, err := settings.TryUpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			kept := slices.DeleteFunc(slices.Clone(s.OrganizeOptions.KeepDlc), func(other string) bool { return strings.EqualFold(other, id) })
			if keep {
				kept = append(kept, id)
			}
			slices.Sort(kept)
			s.OrganizeOptions.KeepDlc = kept
		}); err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, i18n.Translatef(lang, "The settings could not be saved: %v", err))
			return
		}
		// Space tells it at once
		web.invalidateDerived()
		writeJSON(w, http.StatusOK, map[string]any{"kept": keep})
	}).Methods("POST")
}
