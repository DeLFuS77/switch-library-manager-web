package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The wishlist keeps games that are not in the library yet. It is shown on the Missing Games
// page and the notifications report when a wished game is released or gets new DLC.

const (
	WISHLIST_FILENAME = "wishlist.json"
	STATUS_WANTED     = "wanted"
)

var titleIdPattern = regexp.MustCompile(`^[0-9A-F]{16}$`)

type wishlist struct {
	mutex sync.Mutex
	path  string
	// by upper case title ID: when the game was added
	games map[string]time.Time
}

func (web *Web) wishes() *wishlist {
	web.wishOnce.Do(func() {
		list := &wishlist{path: filepath.Join(web.dataFolder, WISHLIST_FILENAME), games: map[string]time.Time{}}
		if data, err := os.ReadFile(list.path); err == nil {
			json.Unmarshal(data, &list.games)
		}
		web.wish = list
	})
	return web.wish
}

func (w *wishlist) has(id string) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	_, ok := w.games[strings.ToUpper(id)]
	return ok
}

func (w *wishlist) ids() []string {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	ids := make([]string, 0, len(w.games))
	for id := range w.games {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (w *wishlist) count() int {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return len(w.games)
}

// set adds or removes a game and saves the list. It returns whether it changed.
func (w *wishlist) set(id string, wanted bool) bool {
	id = strings.ToUpper(id)
	w.mutex.Lock()
	defer w.mutex.Unlock()
	_, present := w.games[id]
	if present == wanted {
		return false
	}
	if wanted {
		w.games[id] = time.Now()
	} else {
		delete(w.games, id)
	}
	w.saveLocked()
	return true
}

// removeOwned drops the games that are now in the library.
func (w *wishlist) removeOwned(owned func(id string) bool) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	changed := false
	for id := range w.games {
		if owned(id) {
			delete(w.games, id)
			changed = true
		}
	}
	if changed {
		w.saveLocked()
	}
}

func (w *wishlist) saveLocked() {
	if data, err := json.MarshalIndent(w.games, "", " "); err == nil {
		writeFileAtomic(w.path, data)
	}
}

// reload reads the list again, after a backup was restored.
func (w *wishlist) reload() {
	games := map[string]time.Time{}
	if data, err := os.ReadFile(w.path); err == nil {
		json.Unmarshal(data, &games)
	}
	w.mutex.Lock()
	w.games = games
	w.mutex.Unlock()
}

// forgetOwnedWishes removes the wished games that are in the library now.
func (web *Web) forgetOwnedWishes() {
	_, localDB := web.state.get()
	if localDB == nil {
		return
	}
	web.wishes().removeOwned(func(id string) bool {
		local, ok := localDB.TitlesMap[strings.ToLower(id[:13])]
		return ok && local.BaseExist
	})
}

func (web *Web) HandleWishlist() {
	web.router.HandleFunc("/wishlist", func(w http.ResponseWriter, r *http.Request) {
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		if !titleIdPattern.MatchString(id) {
			writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), "Invalid request")
			return
		}
		wanted := r.FormValue("wanted") == "true"
		if web.wishes().set(id, wanted) {
			// the Missing Games lists show the hearts
			web.invalidateDerived()
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "wanted": wanted, "count": web.wishes().count()})
	}).Methods("POST")
}
