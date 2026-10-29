package web

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
)

// Of a game the library has from several regions, the user can choose the copy that stands for
// it, instead of the one suggested: the library shows that one when each game counts once, with
// the flags of all, and Space keeps it.

// REGION_CHOICES_FILENAME holds the copies chosen, one title ID for each game.
const REGION_CHOICES_FILENAME = "regions.json"

type regionChoices struct {
	mutex sync.Mutex
	path  string
	// the title IDs chosen, upper case
	ids map[string]bool
}

func (web *Web) regionChoices() *regionChoices {
	web.regionChoiceOnce.Do(func() {
		store := &regionChoices{path: filepath.Join(web.dataFolder, REGION_CHOICES_FILENAME), ids: map[string]bool{}}
		if data, ok := readStore(store.path); ok {
			parseStore(store.path, data, &store.ids)
		}
		if store.ids == nil {
			store.ids = map[string]bool{}
		}
		web.regionChoice = store
	})
	return web.regionChoice
}

// snapshot returns the title IDs chosen.
func (c *regionChoices) snapshot() map[string]bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	set := make(map[string]bool, len(c.ids))
	for id := range c.ids {
		set[id] = true
	}
	return set
}

// choose makes id the copy chosen of the game whose copies are group, forgetting what was chosen
// among them before; with no id, the suggested copy stands for the game again.
func (c *regionChoices) choose(id string, group []string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	for _, other := range group {
		delete(c.ids, strings.ToUpper(other))
	}
	if id != "" {
		c.ids[strings.ToUpper(id)] = true
	}
	if data, err := json.Marshal(c.ids); err == nil {
		writeFileAtomic(c.path, data)
	}
}

// applyRegionChoice makes the copy chosen, when one of the group is, the one kept instead of the
// suggested one; the suggested one stays marked as such.
func applyRegionChoice(group *RegionGroup, chosen map[string]bool) {
	index := -1
	for i, copy := range group.Copies {
		if chosen[strings.ToUpper(copy.Id)] {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	group.Chosen = true
	group.Savable = 0
	for i := range group.Copies {
		copy := &group.Copies[i]
		copy.Keep = i == index
		copy.Chosen = i == index
		if !copy.Keep {
			group.Savable += copy.Size
		}
	}
	group.Name = group.Copies[index].Name
	// the copy kept first, as the suggested one is when nothing is chosen
	kept := group.Copies[index]
	copy(group.Copies[1:index+1], group.Copies[:index])
	group.Copies[0] = kept
}

// regionGroupOf is the game with copies of several regions that id is a copy of, or nil.
func (web *Web) regionGroupOf(id string, lang string) *RegionGroup {
	for _, group := range web.regionalDuplicates(lang) {
		for _, copy := range group.Copies {
			if strings.EqualFold(copy.Id, id) {
				return &group
			}
		}
	}
	return nil
}

var errRegionNotCopy = errorString("This game has no copy from another region in the library.")

func (web *Web) handleRegionMain() {
	// the copy that stands for a game of several regions, or the suggested one again (no id
	// but the one of a copy, with suggested=true)
	web.router.HandleFunc("/regions/main", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		if !titleIdPattern.MatchString(id) {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid Title ID (16 hexadecimal characters)")
			return
		}
		group := web.regionGroupOf(id, lang)
		if group == nil {
			writeGlobalError(w, http.StatusBadRequest, lang, errRegionNotCopy.Error())
			return
		}
		ids := make([]string, 0, len(group.Copies))
		for _, copy := range group.Copies {
			ids = append(ids, copy.Id)
		}
		chosen := id
		if r.FormValue("suggested") == "true" {
			chosen = ""
		}
		web.regionChoices().choose(chosen, ids)
		web.invalidateDerived()
		writeJSON(w, http.StatusOK, map[string]any{"main": chosen})
	}).Methods("POST")
}
