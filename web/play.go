package web

import (
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
	"net/http"
	"path/filepath"
	"strings"
)

// Each user keeps how their games go: pending, playing, finished, completed or abandoned, with
// a rating of 1 to 5 stars and a note of their own. Without login there is one of them for
// everyone. The Library shows it on the cards and filters by it, and the game page sets it.

func (web *Web) play() *userdata.PlayStore {
	web.playOnce.Do(func() {
		web.playStore = userdata.LoadPlay(filepath.Join(web.dataFolder, userdata.PLAY_FILENAME))
	})
	return web.playStore
}

// playUser is the user the states of a request belong to: "" without login.
func playUser(r *http.Request) string {
	if principal := principalFrom(r); principal != nil {
		return principal.Name
	}
	return ""
}

func (web *Web) HandlePlay() {
	web.router.HandleFunc("/play", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		if !titleIdPattern.MatchString(id) {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid Title ID (16 hexadecimal characters)")
			return
		}
		entry, ok := userdata.CleanPlayEntry(r.FormValue("status"), r.FormValue("rating"), r.FormValue("note"))
		if !ok {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		if err := web.play().Set(playUser(r), id, entry); err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": entry.Status, "rating": entry.Rating, "note": entry.Note})
	}).Methods("POST")
}
