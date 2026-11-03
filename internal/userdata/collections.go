package userdata

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
)

const (
	COLLECTIONS_FILENAME = "collections.json"
	maxCollectionName    = 40
	MaxCollectionIds     = 500
)

// CollectionCount is a collection and the number of games in it.
type CollectionCount struct {
	Name  string
	Count int
}

// Collections are the collections the users put their games in, saved in the data folder.
type Collections struct {
	mutex sync.Mutex
	path  string
	// by upper case title ID: the collections of the game, sorted
	games map[string][]string
}

// LoadCollections reads the collections saved at path.
func LoadCollections(path string) *Collections {
	store := &Collections{path: path}
	store.Reload()
	return store
}

// Reload reads the collections again, also after a backup was restored.
func (c *Collections) Reload() {
	games := map[string][]string{}
	if data, ok := durable.ReadStore(c.path); ok {
		durable.ParseStore(c.path, data, &games)
	}
	c.mutex.Lock()
	c.games = games
	c.mutex.Unlock()
}

// CleanCollectionName returns the name of a collection without surrounding or repeated
// spaces and control characters, or "" when it is not valid.
func CleanCollectionName(name string) string {
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if name == "" || utf8.RuneCountInString(name) > maxCollectionName {
		return ""
	}
	return name
}

// Of returns the collections of a game.
func (c *Collections) Of(id string) []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]string(nil), c.games[strings.ToUpper(id)]...)
}

// Snapshot returns the collections of every game.
func (c *Collections) Snapshot() map[string][]string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	games := make(map[string][]string, len(c.games))
	for id, names := range c.games {
		games[id] = names
	}
	return games
}

// Names returns every collection, sorted by name.
func (c *Collections) Names() []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	set := map[string]struct{}{}
	for _, names := range c.games {
		for _, name := range names {
			set[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names
}

// Set adds the games to a collection or removes them, and saves the collections. A name
// that only differs in case from an existing collection is the same collection.
func (c *Collections) Set(ids []string, name string, add bool) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	for _, names := range c.games {
		for _, existing := range names {
			if strings.EqualFold(existing, name) {
				name = existing
			}
		}
	}
	for _, id := range ids {
		id = strings.ToUpper(id)
		names := []string{}
		for _, existing := range c.games[id] {
			if existing != name {
				names = append(names, existing)
			}
		}
		if add {
			names = append(names, name)
			sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
		}
		if len(names) == 0 {
			delete(c.games, id)
		} else {
			c.games[id] = names
		}
	}
	data, err := json.MarshalIndent(c.games, "", " ")
	if err != nil {
		return err
	}
	return durable.WriteFile(c.path, data)
}
