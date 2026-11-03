package userdata

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"go.uber.org/zap"
)

const FAVORITES_FILENAME = "favorites.json"

// Favorites are the games marked with the star, saved in the data folder.
type Favorites struct {
	mutex sync.Mutex
	path  string
	// by upper case title ID: when the game was marked
	games map[string]time.Time
	// one save at a time: an older one never ends after a newer one
	saving sync.Mutex
}

// NewFavorites are the favorites saved at path, none until they are read (see Reload).
func NewFavorites(path string) *Favorites {
	return &Favorites{path: path, games: map[string]time.Time{}}
}

// Mark adds games to the favorites as marked now, without saving them.
func (f *Favorites) Mark(ids []string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	for _, id := range ids {
		f.games[id] = time.Now()
	}
}

// Reload reads the favorites again, also after a backup was restored.
func (f *Favorites) Reload() {
	games := map[string]time.Time{}
	if data, ok := durable.ReadStore(f.path); ok {
		durable.ParseStore(f.path, data, &games)
	}
	f.mutex.Lock()
	f.games = games
	f.mutex.Unlock()
}

// Has reports whether a game is a favorite.
func (f *Favorites) Has(id string) bool {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	_, ok := f.games[strings.ToUpper(id)]
	return ok
}

// Snapshot returns the favorite title IDs (upper case), for checking many titles at once.
func (f *Favorites) Snapshot() map[string]bool {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	set := make(map[string]bool, len(f.games))
	for id := range f.games {
		set[id] = true
	}
	return set
}

// Set marks a game as favorite or not and saves the favorites. It returns whether it changed.
func (f *Favorites) Set(id string, favorite bool) bool {
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
	if err := f.Save(); err != nil {
		zap.S().Errorf("The favorites could not be saved: %v", err)
	}
	return true
}

// SnapshotTimes returns when each favorite was marked, by title ID.
func (f *Favorites) SnapshotTimes() map[string]time.Time {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	times := make(map[string]time.Time, len(f.games))
	for id, marked := range f.games {
		times[id] = marked
	}
	return times
}

// Save writes the favorites as they are when it starts: saves made at the same time each
// write a newer state than the one before, so the last one written holds every change.
func (f *Favorites) Save() error {
	f.saving.Lock()
	defer f.saving.Unlock()
	f.mutex.Lock()
	data, err := json.MarshalIndent(f.games, "", " ")
	f.mutex.Unlock()
	if err != nil {
		return err
	}
	return durable.WriteFile(f.path, data)
}
