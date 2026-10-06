package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Collections are the user's own groups of games ("Favorites", "Playing", "For the
// kids"...). A game can be in several of them; the library can be filtered by collection.

const (
	COLLECTIONS_FILENAME = "collections.json"
	maxCollectionName    = 40
	maxCollectionIds     = 500
)

// CollectionCount is a collection and the number of games in it.
type CollectionCount struct {
	Name  string
	Count int
}

type collectionStore struct {
	mutex sync.Mutex
	path  string
	// by upper case title ID: the collections of the game, sorted
	games map[string][]string
}

func (web *Web) collections() *collectionStore {
	web.collectionsOnce.Do(func() {
		store := &collectionStore{path: filepath.Join(web.dataFolder, COLLECTIONS_FILENAME)}
		store.reload()
		web.coll = store
	})
	return web.coll
}

// reload reads the collections again, also after a backup was restored.
func (c *collectionStore) reload() {
	games := map[string][]string{}
	if data, err := os.ReadFile(c.path); err == nil {
		json.Unmarshal(data, &games)
	}
	c.mutex.Lock()
	c.games = games
	c.mutex.Unlock()
}

// cleanCollectionName returns the name of a collection without surrounding or repeated
// spaces and control characters, or "" when it is not valid.
func cleanCollectionName(name string) string {
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if name == "" || utf8.RuneCountInString(name) > maxCollectionName {
		return ""
	}
	return name
}

// of returns the collections of a game.
func (c *collectionStore) of(id string) []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]string(nil), c.games[strings.ToUpper(id)]...)
}

// snapshot returns the collections of every game.
func (c *collectionStore) snapshot() map[string][]string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	games := make(map[string][]string, len(c.games))
	for id, names := range c.games {
		games[id] = names
	}
	return games
}

// names returns every collection, sorted by name.
func (c *collectionStore) names() []string {
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

// set adds the games to a collection or removes them, and saves the collections. A name
// that only differs in case from an existing collection is the same collection.
func (c *collectionStore) set(ids []string, name string, add bool) error {
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
	return os.WriteFile(c.path, data, 0644)
}

func (web *Web) HandleCollections() {
	web.router.HandleFunc("/collections", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		lang := web.requestLanguage(r)
		name := cleanCollectionName(r.FormValue("name"))
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
		if len(ids) == 0 || len(ids) > maxCollectionIds {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid Title ID (16 hexadecimal characters)")
			return
		}
		add := r.FormValue("action") != "remove"
		if err := web.collections().set(ids, name, add); err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, err.Error())
			return
		}
		// the library shows and filters the collections
		web.invalidateDerived()
		writeJSON(w, http.StatusOK, map[string]any{"ids": ids, "name": name, "added": add})
	}).Methods("POST")
}
