package web

import (
	"errors"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Favorites are the games of the library the user likes most, marked with a star. They are
// not the wishlist, which keeps games that are not in the library yet.

// collections named like this before the star existed become the favorites
var favoriteCollectionNames = map[string]bool{"favorites": true, "favourites": true, "favoritos": true, "favoris": true, "favoriten": true, "preferiti": true}

func (web *Web) favorites() *userdata.Favorites {
	web.favoritesOnce.Do(func() {
		path := filepath.Join(web.dataFolder, userdata.FAVORITES_FILENAME)
		store := userdata.NewFavorites(path)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			web.migrateFavoriteCollection(store)
		} else {
			store.Reload()
		}
		web.fav = store
	})
	return web.fav
}

// migrateFavoriteCollection turns a collection named "Favorites" into the favorites.
func (web *Web) migrateFavoriteCollection(store *userdata.Favorites) {
	collections := web.collections()
	for _, name := range collections.Names() {
		if !favoriteCollectionNames[strings.ToLower(name)] {
			continue
		}
		ids := []string{}
		for id, names := range collections.Snapshot() {
			for _, existing := range names {
				if existing == name {
					ids = append(ids, id)
				}
			}
		}
		store.Mark(ids)
		if len(ids) > 0 {
			collections.Set(ids, name, false)
		}
	}
	if len(store.Snapshot()) > 0 {
		store.Save()
	}
}

func (web *Web) HandleFavorites() {
	web.router.HandleFunc("/favorites", func(w http.ResponseWriter, r *http.Request) {
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		if !titleIdPattern.MatchString(id) {
			writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), "Invalid request")
			return
		}
		favorite := r.FormValue("favorite") == "true"
		if web.favorites().Set(id, favorite) {
			// the library shows the stars and filters them
			web.invalidateDerived()
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "favorite": favorite})
	}).Methods("POST")
}
