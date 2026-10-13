package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// loadSeries reads the saved titles.series.json.
func (web *Web) loadSeries() *wikidataSeries {
	data := &wikidataSeries{}
	if bytes, err := os.ReadFile(filepath.Join(web.dataFolder, SERIES_JSON_FILENAME)); err == nil {
		json.Unmarshal(bytes, data)
	}
	return data
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
	names := k.labels[family]
	if name := names[lang]; name != "" {
		return name
	}
	if name := names["en"]; name != "" {
		return name
	}
	return names[""]
}

// broadest chooses the series of a game in several: the one with the most games, as
// "Final Fantasy" rather than "Final Fantasy Pixel Remaster".
func broadest(candidates []string, size func(string) int) string {
	best := ""
	for _, candidate := range candidates {
		if best == "" || size(candidate) > size(best) || size(candidate) == size(best) && candidate < best {
			best = candidate
		}
	}
	return best
}

// buildKnownSeries finds the series of the games of the titles database in IGDB and
// Wikidata. byName lists the groups of title ID of the games by regionalName.
func (web *Web) buildKnownSeries(switchDB *db.SwitchTitlesDB, byName map[string][]string) *knownSeries {
	known := &knownSeries{familyOf: map[string]string{}, labels: map[string]map[string]string{}}
	assign := func(prefix string, family string) {
		if _, done := known.familyOf[prefix]; !done {
			known.familyOf[prefix] = family
		}
	}

	// IGDB: the collections of the games looked up, and the other Switch games in them
	collections := web.igdbCollections().snapshot()
	for id, game := range web.timesToBeat().snapshot() {
		if len(game.Collections) == 0 || len(id) < 13 {
			continue
		}
		candidates := []string{}
		for _, collection := range game.Collections {
			if _, ok := collections[collection]; ok {
				candidates = append(candidates, strconv.Itoa(collection))
			}
		}
		chosen := broadest(candidates, func(c string) int {
			number, _ := strconv.Atoi(c)
			return len(collections[number].Members)
		})
		if chosen == "" {
			continue
		}
		number, _ := strconv.Atoi(chosen)
		collection := collections[number]
		family := "igdb:" + chosen
		known.labels[family] = map[string]string{"": collection.Name}
		assign(strings.ToLower(id[:13]), family)
		for _, member := range collection.Members {
			for _, prefix := range byName[regionalName(member)] {
				assign(prefix, family)
			}
		}
	}

	// Wikidata: by title ID, else by name
	series := web.loadSeries()
	names := map[string]string{}
	for name, id := range series.Names {
		names[regionalName(name)] = id
	}
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
