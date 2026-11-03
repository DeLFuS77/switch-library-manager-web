package web

import (
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
	"net/http"
	"path/filepath"
	"strings"
)

// Collections are the user's own groups of games ("Favorites", "Playing", "For the
// kids"...). A game can be in several of them; the library can be filtered by collection.

func (web *Web) collections() *userdata.Collections {
	web.collectionsOnce.Do(func() {
		web.coll = userdata.LoadCollections(filepath.Join(web.dataFolder, userdata.COLLECTIONS_FILENAME))
	})
	return web.coll
}

func (web *Web) HandleCollections() {
	web.router.HandleFunc("/collections", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		lang := web.requestLanguage(r)
		name := userdata.CleanCollectionName(r.FormValue("name"))
		if name == "" {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid collection name")
			return
		}
		ids := []string{}
		for _, id := range r.Form["id"] {
			id = strings.ToUpper(strings.TrimSpace(id))
			if !titleIdPattern.MatchString(id) {
				writeGlobalError(w, http.StatusBadRequest, lang, "Invalid Title ID (16 hexadecimal characters)")
				return
			}
			ids = append(ids, id)
		}
		if len(ids) == 0 || len(ids) > userdata.MaxCollectionIds {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid Title ID (16 hexadecimal characters)")
			return
		}
		add := r.FormValue("action") != "remove"
		if err := web.collections().Set(ids, name, add); err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, err.Error())
			return
		}
		// the library shows and filters the collections
		web.invalidateDerived()
		writeJSON(w, http.StatusOK, map[string]any{"ids": ids, "name": name, "added": add})
	}).Methods("POST")
}
