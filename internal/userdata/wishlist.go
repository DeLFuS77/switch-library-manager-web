package userdata

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
)

const (
	WISHLIST_FILENAME = "wishlist.json"
	STATUS_WANTED     = "wanted"
)

// Wishlist is the games the users want and do not have, saved in the data folder.
type Wishlist struct {
	mutex sync.Mutex
	path  string
	// by upper case title ID: when the game was added
	games map[string]time.Time
}

// LoadWishlist reads the wishlist saved at path.
func LoadWishlist(path string) *Wishlist {
	list := &Wishlist{path: path, games: map[string]time.Time{}}
	if data, ok := durable.ReadStore(list.path); ok {
		durable.ParseStore(list.path, data, &list.games)
	}
	return list
}

// Has reports whether a game is on the wishlist.
func (w *Wishlist) Has(id string) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	_, ok := w.games[strings.ToUpper(id)]
	return ok
}

// Snapshot returns the wished title IDs (upper case), for checking many titles at once.
func (w *Wishlist) Snapshot() map[string]bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	set := make(map[string]bool, len(w.games))
	for id := range w.games {
		set[id] = true
	}
	return set
}

// Ids are the wished title IDs (upper case).
func (w *Wishlist) Ids() []string {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	ids := make([]string, 0, len(w.games))
	for id := range w.games {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Count is the number of games on the wishlist.
func (w *Wishlist) Count() int {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return len(w.games)
}

// Set adds or removes a game and saves the list. It returns whether it changed.
func (w *Wishlist) Set(id string, wanted bool) bool {
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

// RemoveOwned drops the games that are now in the library.
func (w *Wishlist) RemoveOwned(owned func(id string) bool) bool {
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
	return changed
}

func (w *Wishlist) saveLocked() {
	if data, err := json.MarshalIndent(w.games, "", " "); err == nil {
		durable.WriteFile(w.path, data)
	}
}

// Reload reads the list again, after a backup was restored.
func (w *Wishlist) Reload() {
	games := map[string]time.Time{}
	if data, ok := durable.ReadStore(w.path); ok {
		durable.ParseStore(w.path, data, &games)
	}
	w.mutex.Lock()
	w.games = games
	w.mutex.Unlock()
}
