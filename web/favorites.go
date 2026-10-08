package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Favorites are the games of the library the user likes most, marked with a star. They are
// not the wishlist, which keeps games that are not in the library yet.

const FAVORITES_FILENAME = "favorites.json"

// collections named like this before the star existed become the favorites
var favoriteCollectionNames = map[string]bool{"favorites": true, "favourites": true, "favoritos": true, "favoris": true, "favoriten": true, "preferiti": true}

type favoriteStore struct {
	mutex sync.Mutex
	path  string
	// by upper case title ID: when the game was marked
	games map[string]time.Time
}

func (web *Web) favorites() *favoriteStore {
	web.favoritesOnce.Do(func() {
		store := &favoriteStore{path: filepath.Join(web.dataFolder, FAVORITES_FILENAME)}
		if _, err := os.Stat(store.path); err != nil {
			store.games = map[string]time.Time{}
			web.migrateFavoriteCollection(store)
		} else {
			store.reload()
		}
		web.fav = store
	})
	return web.fav
}

// migrateFavoriteCollection turns a collection named "Favorites" into the favorites.
func (web *Web) migrateFavoriteCollection(store *favoriteStore) {
	collections := web.collections()
	for _, name := range collections.names() {
		if !favoriteCollectionNames[strings.ToLower(name)] {
			continue
		}
		ids := []string{}
		for id, names := range collections.snapshot() {
			for _, existing := range names {
				if existing == name {
					ids = append(ids, id)
				}
			}
		}
		for _, id := range ids {
			store.games[id] = time.Now()
		}
		if len(ids) > 0 {
			collections.set(ids, name, false)
		}
	}
	if len(store.games) > 0 {
		store.save()
	}
}

// reload reads the favorites again, also after a backup was restored.
func (f *favoriteStore) reload() {
	games := map[string]time.Time{}
	if data, err := os.ReadFile(f.path); err == nil {
		json.Unmarshal(data, &games)
	}
	f.mutex.Lock()
	f.games = games
	f.mutex.Unlock()
}

func (f *favoriteStore) has(id string) bool {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	_, ok := f.games[strings.ToUpper(id)]
	return ok
}

// snapshot returns the favorite title IDs (upper case), for checking many titles at once.
func (f *favoriteStore) snapshot() map[string]bool {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	set := make(map[string]bool, len(f.games))
	for id := range f.games {
		set[id] = true
	}
	return set
}

// set marks a game as favorite or not and saves the favorites. It returns whether it changed.
func (f *favoriteStore) set(id string, favorite bool) bool {
	id = strings.ToUpper(id)
	f.mutex.Lock()
	_, present := f.games[id]
	if present == favorite {
		f.mutex.Unlock()
		return false
	}
	if favorite {
		f.games[id] = time.Now()
	} else {
		delete(f.games, id)
	}
	f.mutex.Unlock()
	f.save()
	return true
}

// snapshotTimes returns when each favorite was marked, by title ID.
func (f *favoriteStore) snapshotTimes() map[string]time.Time {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	times := make(map[string]time.Time, len(f.games))
	for id, marked := range f.games {
		times[id] = marked
	}
	return times
}

func (f *favoriteStore) save() {
	f.mutex.Lock()
	data, err := json.MarshalIndent(f.games, "", " ")
	f.mutex.Unlock()
	if err == nil {
		writeFileAtomic(f.path, data)
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
		if web.favorites().set(id, favorite) {
			// the library shows the stars and filters them
			web.invalidateDerived()
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "favorite": favorite})
	}).Methods("POST")
}
