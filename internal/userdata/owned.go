package userdata

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
)

const OWNED_FILENAME = "owned.json"

// Owned are the games the users have without a file of them, a cartridge for instance, marked by
// hand for the whole library, saved in the data folder: the library shows them and they are not
// missing.
type Owned struct {
	mutex sync.Mutex
	path  string
	// by upper case title ID of the game: when it was marked
	games map[string]time.Time
	// one save at a time: an older one never ends after a newer one
	saving sync.Mutex
}

// LoadOwned reads the games marked at path.
func LoadOwned(path string) *Owned {
	owned := &Owned{path: path, games: map[string]time.Time{}}
	owned.Reload()
	return owned
}

// Reload reads the marks again, also after a backup was restored.
func (o *Owned) Reload() {
	games := map[string]time.Time{}
	if data, ok := durable.ReadStore(o.path); ok {
		durable.ParseStore(o.path, data, &games)
	}
	o.mutex.Lock()
	o.games = games
	o.mutex.Unlock()
}

// Has reports whether a game is marked.
func (o *Owned) Has(id string) bool {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	_, ok := o.games[strings.ToUpper(id)]
	return ok
}

// Snapshot returns when each marked game was marked, by upper case title ID.
func (o *Owned) Snapshot() map[string]time.Time {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	games := make(map[string]time.Time, len(o.games))
	for id, marked := range o.games {
		games[id] = marked
	}
	return games
}

// Ids are the marked title IDs (upper case), in order.
func (o *Owned) Ids() []string {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	ids := make([]string, 0, len(o.games))
	for id := range o.games {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Count is the number of marked games.
func (o *Owned) Count() int {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return len(o.games)
}

// Set marks a game or takes its mark away, and saves the marks. It returns whether it changed.
func (o *Owned) Set(id string, owned bool) (bool, error) {
	id = strings.ToUpper(id)
	o.mutex.Lock()
	_, present := o.games[id]
	if present == owned {
		o.mutex.Unlock()
		return false, nil
	}
	if owned {
		o.games[id] = time.Now()
	} else {
		delete(o.games, id)
	}
	o.mutex.Unlock()
	return true, o.save()
}

// save writes the marks as they are when it starts: saves made at the same time each write a
// newer state than the one before, so the last one written holds every change.
func (o *Owned) save() error {
	o.saving.Lock()
	defer o.saving.Unlock()
	o.mutex.Lock()
	data, err := json.MarshalIndent(o.games, "", " ")
	o.mutex.Unlock()
	if err != nil {
		return err
	}
	return durable.WriteFile(o.path, data)
}
