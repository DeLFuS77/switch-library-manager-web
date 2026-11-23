package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The files that are not games (images, notes, archives...) can be deleted from Issues, one by
// one or a few chosen at once, always after a confirmation (#197). They are the user's files, so
// the ones that may belong to something else are never offered: see nonGameRefusal.

// nonGameQuiet is how long a file must be left alone before it can be deleted: a copy or a
// download that is still going on changes it.
const nonGameQuiet = 10 * time.Minute

// the reasons a file that is not a game is not offered for deletion, shown on its row
const (
	NON_GAME_PART_OF_GAME = "Part of a game, a mod or a dump of a console: not deleted from here"
	NON_GAME_RECENT       = "Changed a moment ago, maybe still being copied: not deleted for now"
	NON_GAME_LINK         = "A link, or in the folders of the app: not deleted from here"
)

// nonGameProtectedTypes are the types that are pieces of a game, a mod, homebrew, keys, saves or
// a dump of a console: the app does not read them as games, but they are not junk either.
var nonGameProtectedTypes = map[string]bool{
	".nca": true, ".ncz": true, ".tik": true, ".cert": true, ".cnmt": true, ".nacp": true, ".npdm": true,
	".nso": true, ".nro": true, ".nrr": true, ".ovl": true, ".kip": true, ".ips": true, ".pchtxt": true,
	".keys": true, ".bin": true, ".sav": true,
}

// nonGameProtectedFolders are the folders of the contents of a game or of a mod (an extracted
// game, the LayeredFS of Atmosphère, the homebrew of a copy of an SD card).
var nonGameProtectedFolders = map[string]bool{"romfs": true, "exefs": true, "contents": true, "atmosphere": true, "switch": true}

// nonGameRefusal says why a file that is not a game cannot be deleted from Issues, or "" when it
// can. Each check fails closed: what cannot be looked at is not offered.
func (web *Web) nonGameRefusal(path string, now time.Time) string {
	lower := strings.ToLower(filepath.Base(path))
	if nonGameProtectedTypes[filepath.Ext(lower)] || strings.HasSuffix(lower, ".cnmt.xml") {
		return NON_GAME_PART_OF_GAME
	}
	// only the folders inside the library count: a library in D:\Switch is not a copy of an SD card
	folders := filepath.Dir(path)
	for _, root := range scanFolders(settings.ReadSettings(web.dataFolder)) {
		if insideFolder(path, root) {
			if rel, err := filepath.Rel(root, folders); err == nil && len(rel) < len(folders) {
				folders = rel
			}
		}
	}
	for _, folder := range strings.FieldsFunc(strings.ToLower(folders), func(r rune) bool { return r == '/' || r == '\\' }) {
		if nonGameProtectedFolders[folder] {
			return NON_GAME_PART_OF_GAME
		}
		if folder == strings.ToLower(db.QUARANTINE_FOLDER) {
			return NON_GAME_LINK
		}
	}
	// the other files of an extracted game (its NCA beside it)
	if entries, err := os.ReadDir(filepath.Dir(path)); err != nil {
		return NON_GAME_PART_OF_GAME
	} else {
		for _, entry := range entries {
			if extension := strings.ToLower(filepath.Ext(entry.Name())); extension == ".nca" || extension == ".ncz" {
				return NON_GAME_PART_OF_GAME
			}
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || web.linksIntoData(path) {
		return NON_GAME_LINK
	}
	if now.Sub(info.ModTime()) < nonGameQuiet {
		return NON_GAME_RECENT
	}
	return ""
}

// NonGameFile is a file that is not a game and can be deleted, for the window to choose them.
type NonGameFile struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// NonGameType is the deletable files of one type.
type NonGameType struct {
	Type  string        `json:"type"`
	Size  int64         `json:"size"`
	Files []NonGameFile `json:"files"`
}

// nonGameFiles lists the files that are not games and can be deleted now, by type (the most
// files first) and by path, and how many others are left out.
func (web *Web) nonGameFiles(issues []Issue, now time.Time) ([]NonGameType, int) {
	byType := map[string]*NonGameType{}
	leftOut := 0
	for _, file := range issueFiles(issues, ISSUE_UNSUPPORTED) {
		path := filepath.Join(file.BaseFolder, file.FileName)
		if web.nonGameRefusal(path, now) != "" {
			leftOut++
			continue
		}
		extension := strings.ToLower(filepath.Ext(file.FileName))
		if extension == "" {
			extension = "."
		}
		group := byType[extension]
		if group == nil {
			group = &NonGameType{Type: extension}
			byType[extension] = group
		}
		group.Files = append(group.Files, NonGameFile{Path: path, Name: file.FileName, Size: file.Size})
		group.Size += file.Size
	}
	types := []NonGameType{}
	for _, group := range byType {
		sort.Slice(group.Files, func(i, j int) bool { return group.Files[i].Path < group.Files[j].Path })
		types = append(types, *group)
	}
	sort.Slice(types, func(i, j int) bool {
		if len(types[i].Files) != len(types[j].Files) {
			return len(types[i].Files) > len(types[j].Files)
		}
		return types[i].Type < types[j].Type
	})
	return types, leftOut
}

// deletableNonGameFiles are the requested paths that are files not games the library lists now
// and can be deleted, as the library lists them (never a path as it was sent), once each.
func (web *Web) deletableNonGameFiles(requested []string, now time.Time) []db.ExtendedFileInfo {
	wanted := map[string]bool{}
	for _, path := range requested {
		wanted[path] = true
	}
	files := []db.ExtendedFileInfo{}
	for _, file := range issueFiles(web.getIssues(), ISSUE_UNSUPPORTED) {
		path := filepath.Join(file.BaseFolder, file.FileName)
		if !wanted[path] || web.nonGameRefusal(path, now) != "" || !readsNow(path) {
			continue
		}
		files = append(files, file)
	}
	return files
}

func (web *Web) handleNonGameFiles() {
	// the files that are not games and can be deleted, for the window to choose them
	web.router.HandleFunc("/issues/non-game-files", func(w http.ResponseWriter, r *http.Request) {
		types, leftOut := web.nonGameFiles(web.getIssues(), time.Now())
		writeJSON(w, http.StatusOK, map[string]any{"types": types, "leftOut": leftOut})
	}).Methods("GET")

	// delete the files that are not games chosen in the window, then scan the library again
	web.router.HandleFunc("/issues/delete-non-game", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil || len(r.PostForm["path"]) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		requested := r.PostForm["path"]
		files := web.deletableNonGameFiles(requested, time.Now())
		if len(files) == 0 {
			writeGlobalError(w, http.StatusConflict, lang, "None of these files can be deleted now: they changed, are in use or are gone.")
			return
		}
		reasons := make([]string, len(files))
		for i := range reasons {
			reasons[i] = "deleted from Issues: " + ISSUE_UNSUPPORTED
		}
		deleted, freed, err := web.removeFilesAsTask(files, reasons)
		if err != nil {
			writeGlobalError(w, http.StatusConflict, lang, err.Error())
			return
		}
		web.invalidateDerived()
		web.Rescan(tasklog.TRIGGER_MANUAL)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "freed": freed, "failed": len(requested) - deleted})
	}).Methods("POST")
}
