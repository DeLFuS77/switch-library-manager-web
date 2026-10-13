package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The collections of IGDB are its series ("Super Mario", "Mario Kart"): the games of the
// library looked up on IGDB tell theirs, and the Switch games of each collection are asked
// once, then again after a while, and kept in a file of the data folder.

const (
	IGDB_SERIES_FILENAME = "igdbseries.json"
	// collections asked at once
	igdbCollectionBatch = 10
	igdbCollectionAge   = 90 * 24 * time.Hour
)

// igdbCollection is a series of IGDB with the names of its Switch games.
type igdbCollection struct {
	Name    string    `json:"name"`
	Members []string  `json:"members,omitempty"`
	Checked time.Time `json:"checked"`
}

type igdbCollectionStore struct {
	mutex       sync.Mutex
	path        string
	collections map[int]igdbCollection
}

func (web *Web) igdbCollections() *igdbCollectionStore {
	web.igdbSeriesOnce.Do(func() {
		store := &igdbCollectionStore{path: filepath.Join(web.dataFolder, IGDB_SERIES_FILENAME), collections: map[int]igdbCollection{}}
		if data, err := os.ReadFile(store.path); err == nil {
			json.Unmarshal(data, &store.collections)
		}
		web.igdbSeries = store
	})
	return web.igdbSeries
}

func (s *igdbCollectionStore) snapshot() map[int]igdbCollection {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	result := make(map[int]igdbCollection, len(s.collections))
	for id, collection := range s.collections {
		result[id] = collection
	}
	return result
}

func (s *igdbCollectionStore) set(id int, collection igdbCollection) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.collections[id] = collection
}

func (s *igdbCollectionStore) save() error {
	s.mutex.Lock()
	data, err := json.Marshal(s.collections)
	s.mutex.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data)
}

// missingIgdbCollections are the collections of the games looked up that were never
// asked, or a while ago.
func (web *Web) missingIgdbCollections(now time.Time) []int {
	known := web.igdbCollections().snapshot()
	seen := map[int]bool{}
	missing := []int{}
	for _, game := range web.timesToBeat().snapshot() {
		for _, id := range game.Collections {
			if seen[id] {
				continue
			}
			seen[id] = true
			if collection, ok := known[id]; !ok || now.Sub(collection.Checked) > igdbCollectionAge {
				missing = append(missing, id)
			}
		}
	}
	sort.Ints(missing)
	return missing
}

// lookupIgdbCollections asks IGDB the Switch games of the collections not known yet.
func (web *Web) lookupIgdbCollections(ctx context.Context, client *igdbClient) error {
	missing := web.missingIgdbCollections(time.Now())
	if len(missing) == 0 {
		return nil
	}
	store := web.igdbCollections()
	for start := 0; start < len(missing); start += igdbCollectionBatch {
		batch := missing[start:min(start+igdbCollectionBatch, len(missing))]
		ids := make([]string, len(batch))
		for i, id := range batch {
			ids[i] = fmt.Sprint(id)
		}
		var found []struct {
			Id    int    `json:"id"`
			Name  string `json:"name"`
			Games []struct {
				Name      string `json:"name"`
				Platforms []int  `json:"platforms"`
			} `json:"games"`
		}
		query := fmt.Sprintf(`fields name,games.name,games.platforms; where id = (%s); limit %d;`, strings.Join(ids, ","), len(batch))
		if err := client.query(ctx, "collections", query, &found); err != nil {
			store.save()
			return err
		}
		now := time.Now()
		answered := map[int]bool{}
		for _, collection := range found {
			answered[collection.Id] = true
			members := []string{}
			for _, game := range collection.Games {
				for _, platform := range game.Platforms {
					if platform == igdbSwitchPlatform {
						members = append(members, game.Name)
						break
					}
				}
			}
			store.set(collection.Id, igdbCollection{Name: collection.Name, Members: members, Checked: now})
		}
		// a collection removed from IGDB is not asked again for a while
		for _, id := range batch {
			if !answered[id] {
				store.set(id, igdbCollection{Checked: now})
			}
		}
	}
	err := store.save()
	web.invalidateDerived()
	return err
}
