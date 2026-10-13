package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The series of a game come, in this order, from IGDB (the collections of the games of the
// library, when the user set IGDB keys), from Wikidata (titles.series.json, made with the
// titles data: the series of the known Switch games, linked to their title ID or name) and
// last from the names and publishers of the games (see saga.go). People keep the first two,
// so they name a series exactly; the names only guess.

const SERIES_JSON_FILENAME = "titles.series.json"

// wikidataSeries is titles.series.json.
type wikidataSeries struct {
	// the name of every series by language
	Series map[string]map[string]string `json:"series"`
	// the series of base games by upper case title ID, and by English name
	Ids   map[string]string `json:"ids"`
	Names map[string]string `json:"names"`
}

// downloadSeries fetches a newer titles.series.json; failures keep the saved copy.
func (web *Web) downloadSeries() {
	settingsObj := settings.ReadSettings(web.dataFolder)
	path := filepath.Join(web.dataFolder, SERIES_JSON_FILENAME)
	etag := settingsObj.LocalizedTitlesEtags["series"]
	if _, err := os.Stat(path); err != nil {
		etag = ""
	}
	result, err := db.LoadAndUpdateAllowingEmpty([]string{settings.DEFAULT_SERIES_JSON_URL}, path, etag)
	if err != nil {
		web.sugarLogger.Debugf("The series of Wikidata are not available: %v", err)
		return
	}
	result.File.Close()
	if result.Etag != etag {
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			if s.LocalizedTitlesEtags == nil {
				s.LocalizedTitlesEtags = map[string]string{}
			}
			s.LocalizedTitlesEtags["series"] = result.Etag
		})
	}
	web.invalidateDerived()
}

// seriesFile is titles.series.json as read, kept while the file does not change, with its
// names made comparable.
type seriesFile struct {
	mutex   sync.Mutex
	size    int64
	modTime time.Time
	data    *wikidataSeries
	names   map[string]string
}

// loadSeries returns the saved titles.series.json and the series of its names by
// regionalName, read again only when the file changed.
func (web *Web) loadSeries() (*wikidataSeries, map[string]string) {
	cache := &web.seriesFile
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	path := filepath.Join(web.dataFolder, SERIES_JSON_FILENAME)
	info, err := os.Stat(path)
	if err != nil {
		return &wikidataSeries{}, map[string]string{}
	}
	if cache.data != nil && info.Size() == cache.size && info.ModTime().Equal(cache.modTime) {
		return cache.data, cache.names
	}
	data := &wikidataSeries{}
	if bytes, err := os.ReadFile(path); err == nil {
		json.Unmarshal(bytes, data)
	}
	names := make(map[string]string, len(data.Names))
	for name, id := range data.Names {
		names[regionalName(name)] = id
	}
	cache.size, cache.modTime, cache.data, cache.names = info.Size(), info.ModTime(), data, names
	return data, names
}

// knownSeries is the series of the games of the titles database known to IGDB or Wikidata,
// by group of title ID, and the names of those series.
type knownSeries struct {
	familyOf map[string]string
	// by series, the name by language ("" for a name of IGDB, in English)
	labels map[string]map[string]string
}

// seriesLabel is the name of a known series in a language, or "".
func (k *knownSeries) seriesLabel(family string, lang string) string {
	if k == nil {
		return ""
	}
	names := k.labels[family]
	if name := names[lang]; name != "" {
		return name
	}
	if name := names["en"]; name != "" {
		return name
	}
	return names[""]
}

// broadestCollection chooses the series of a game in several: the one with the most
// Switch games, as "Final Fantasy" rather than "Final Fantasy Pixel Remaster"; the lowest
// number when they have as many. Collections without a name or games are left out.
func broadestCollection(ids []int, collections map[int]igdbCollection) (int, bool) {
	best, found := 0, false
	for _, id := range ids {
		collection, ok := collections[id]
		if !ok || collection.Name == "" || len(collection.Members) == 0 {
			continue
		}
		size, bestSize := len(collection.Members), len(collections[best].Members)
		if !found || size > bestSize || size == bestSize && id < best {
			best, found = id, true
		}
	}
	return best, found
}

// buildKnownSeries finds the series of the games of the titles database in IGDB and
// Wikidata. byName lists the groups of title ID of the games by regionalName. A game looked
// up on IGDB is in its own series first; the other Switch games of a series join it next,
// the broadest series first; the order never depends on the order of a map.
func (web *Web) buildKnownSeries(switchDB *db.SwitchTitlesDB, byName map[string][]string) *knownSeries {
	known := &knownSeries{familyOf: map[string]string{}, labels: map[string]map[string]string{}}
	assign := func(prefix string, family string) {
		if _, done := known.familyOf[prefix]; !done {
			known.familyOf[prefix] = family
		}
	}

	collections := web.igdbCollections().snapshot()
	games := web.timesToBeat().snapshot()
	ids := make([]string, 0, len(games))
	for id := range games {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	chosen := map[int]bool{}
	for _, id := range ids {
		collection, ok := broadestCollection(games[id].Collections, collections)
		if !ok || len(id) < 13 {
			continue
		}
		family := "igdb:" + strconv.Itoa(collection)
		known.labels[family] = map[string]string{"": collections[collection].Name}
		assign(strings.ToLower(id[:13]), family)
		chosen[collection] = true
	}
	order := make([]int, 0, len(chosen))
	for collection := range chosen {
		order = append(order, collection)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := len(collections[order[i]].Members), len(collections[order[j]].Members)
		return a > b || a == b && order[i] < order[j]
	})
	for _, collection := range order {
		family := "igdb:" + strconv.Itoa(collection)
		for _, member := range collections[collection].Members {
			for _, prefix := range byName[regionalName(member)] {
				assign(prefix, family)
			}
		}
	}

	// Wikidata: by title ID, else by name
	series, names := web.loadSeries()
	if switchDB != nil {
		for prefix, title := range switchDB.TitlesMap {
			if _, done := known.familyOf[prefix]; done || title.Attributes.Id == "" {
				continue
			}
			id, ok := series.Ids[strings.ToUpper(title.Attributes.Id)]
			if !ok {
				id, ok = names[regionalName(title.Attributes.Name)]
			}
			if !ok || series.Series[id] == nil {
				continue
			}
			family := "wd:" + id
			known.labels[family] = series.Series[id]
			known.familyOf[prefix] = family
		}
	}
	return known
}
