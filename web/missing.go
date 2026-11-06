package web

import (
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"sort"
	"strings"
	"time"
)

func (web *Web) HandleMissing() {
	fsPatterns := []string{
		"resources/layout.html",
		"resources/partials/card.html",
		"resources/partials/filter.html",
		"resources/partials/pagination.html",
		"resources/pages/missing.html",
	}

	web.HandleFiltered("/missing.html", func(filter *TitleItemFilter, lang string) any {
		items, p, facets := web.getMissingGamesWithFacets(filter, lang)
		data := MissingPageData{TitleItemsPageData: TitleItemsPageData{
			GlobalPageData: web.globalPageData("missing"),
			WishedCount:    web.wishes().Count(),
			TitleItems:     items,
			Filter:         filter,
			Pagination:     p,
			Views:          web.libraryViews("missing", lang, filter.Console).withConsole(filter, facets, settings.ReadSettings(web.dataFolder).HasSwitch2),
		}, Facets: facets}
		if filter.Plain() {
			data.Recommendations = web.recommendations(lang)
		}
		return data
	}, web.embedFS, fsPatterns...)
}

// MissingPageData is the Missing Games page: the games, and the filters they can be narrowed by.
type MissingPageData struct {
	TitleItemsPageData
	Facets LibraryFacets
}

func (web *Web) getMissingGames(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination) {
	items, p, _ := web.getMissingGamesWithFacets(filter, lang)
	return items, p
}

// getMissingGamesWithFacets gives the page of the missing games that match the filter: the
// Wishlist, the search, the region, the genre, the players, the language and games or demos.
// Each filter is counted before its own choice, like in the Library.
func (web *Web) getMissingGamesWithFacets(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination, LibraryFacets) {
	all := web.sorted("missingGames:"+lang, filter, func() []TitleItem { return web.buildMissingGames(lang) })
	facets := LibraryFacets{Formats: []string{}, Regions: []string{}, DemosHidden: settings.ReadSettings(web.dataFolder).HideDemoGames}
	regions := map[string]struct{}{}
	genreCounts := map[string]int{}
	languageCounts := map[string]int{}
	matched := make([]int, 0, len(all))
	once := web.newMissingOnce()
	filter.searchIn(all)
	for index := range all {
		item := &all[index]
		if (filter.Status == userdata.STATUS_WANTED && !item.Wished) || !filter.MatchesItem(item) {
			continue
		}
		if item.Region != "" {
			regions[item.Region] = struct{}{}
		}
		if filter.Region != "" && item.Region != filter.Region {
			continue
		}
		if !once.takes(item) {
			continue
		}
		for _, genre := range item.Genres {
			genreCounts[genre]++
		}
		for _, language := range item.Languages {
			languageCounts[language]++
		}
		switch {
		case item.Players == 1:
			facets.SinglePlayer++
		case item.Players >= 4:
			facets.TwoPlayers++
			facets.FourPlayers++
		case item.Players >= 2:
			facets.TwoPlayers++
		}
		if (filter.Genre != "" && !hasValue(item.Genres, filter.Genre)) || !playersMatch(item.Players, filter.Players) ||
			(filter.GameLanguage != "" && !hasValue(item.Languages, filter.GameLanguage)) || !scoreMatches(item.Score, filter.Score) {
			continue
		}
		// the console and the kind are the last filters: each console counts the games of its own
		// tab, with the kind chosen, and the kinds the games of the console chosen
		switch2 := item.IsSwitch2()
		kind := !((filter.Kind == KIND_GAME && item.Demo) || (filter.Kind == KIND_DEMO && !item.Demo))
		if kind && switch2 {
			facets.Switch2++
		} else if kind {
			facets.Switch1++
		}
		if (filter.Console == CONSOLE_SWITCH && switch2) || (filter.Console == CONSOLE_SWITCH2 && !switch2) {
			continue
		}
		if item.Demo {
			facets.Demos++
		} else {
			facets.Games++
		}
		if !kind {
			continue
		}
		facets.All++
		matched = append(matched, index)
	}
	for region := range regions {
		facets.Regions = append(facets.Regions, region)
	}
	sort.Strings(facets.Regions)
	facets.Genres = sortedCounts(genreCounts)
	facets.Languages = sortedCounts(languageCounts)

	// the items are already sorted; only the shown page is copied
	p := pagination.Calculate(filter.Page, filter.PerPage, len(matched))
	items := make([]TitleItem, 0, p.End-p.Start)
	for _, index := range matched[p.Start:p.End] {
		item := all[index]
		// one card of all the regions of the game, as in the Library
		if once != nil {
			item.MissingFlags = missingRegions(item, once.byName)
			if room := coverFlags - 1; len(item.MissingFlags) > room {
				item.MissingFlags, item.HiddenRegions = item.MissingFlags[:room], item.MissingFlags[room:]
			}
		}
		items = append(items, item)
	}
	return items, p, facets
}

// missingOnce shows each missing game once when the settings count the games of several regions
// once: a game the library has from one region is not missing from the others, and a game it
// does not have is one card of all its regions, the first one met.
type missingOnce struct {
	byName map[string][]string
	owned  map[string]bool
	shown  map[string]bool
}

// newMissingOnce is nil when each region counts on its own.
func (web *Web) newMissingOnce() *missingOnce {
	if !settings.ReadSettings(web.dataFolder).CountRegionsOnce {
		return nil
	}
	return &missingOnce{byName: web.regionsByName(), owned: web.ownedNames(), shown: map[string]bool{}}
}

// takes reports whether a missing game shows, and keeps that it did; a game of the Switch 2
// alone is no copy of the game of the Switch of its name.
func (m *missingOnce) takes(item *TitleItem) bool {
	if m == nil || item.IsSwitch2() {
		return true
	}
	name := regionalName(item.OriginalName)
	if _, known := m.byName[name]; !known {
		return true
	}
	if m.owned[name] || m.shown[name] {
		return false
	}
	m.shown[name] = true
	return true
}

// ownedNames are the names (regionalName) of the games of the library, as their cards have them,
// and of the games marked as owned.
func (web *Web) ownedNames() map[string]bool {
	return web.derived("ownedNames", func() any {
		names := map[string]bool{}
		switchDB, localDB := web.state.get()
		if localDB == nil {
			return names
		}
		for k, v := range localDB.TitlesMap {
			if !v.BaseExist || v.File.Metadata == nil {
				continue
			}
			var title *db.SwitchTitle
			if switchDB != nil {
				title = switchDB.TitlesMap[k]
			}
			names[regionalName(getLocalTitleName(title, v))] = true
		}
		if switchDB != nil {
			for prefix := range web.markedPrefixes() {
				if title := switchDB.TitlesMap[prefix]; title != nil {
					names[regionalName(title.Attributes.Name)] = true
				}
			}
		}
		return names
	}).(map[string]bool)
}

// missingCheck tells the games of the titles database the library is missing, as the Missing
// Games page lists them.
type missingCheck struct {
	localDB *db.LocalSwitchFilesDB
	// the games marked as owned without a file, by prefix
	marked    map[string]time.Time
	hideDemos bool
	// the user has a Switch 2, and today as a date of the titles database
	switch2 bool
	today   int
}

func (web *Web) newMissingCheck(localDB *db.LocalSwitchFilesDB, now time.Time) missingCheck {
	settingsObj := settings.ReadSettings(web.dataFolder)
	return missingCheck{localDB: localDB, marked: web.markedPrefixes(), hideDemos: settingsObj.HideDemoGames, switch2: settingsObj.HasSwitch2, today: dateNumber(now)}
}

// missing reports whether the game of a prefix is missing: not in the library nor marked as
// owned, with a name, not a demo when they are hidden, and a game of the Switch 2 alone only for
// who has the console and once it is out (a game without a date is shown, with "No date").
func (c missingCheck) missing(prefix string, title *db.SwitchTitle) bool {
	attributes := title.Attributes
	if attributes.Name == "" || attributes.Id == "" || hasGame(c.localDB, c.marked, prefix) {
		return false
	}
	if db.Switch2(attributes.Id) && (!c.switch2 || attributes.ReleaseDate > c.today) {
		return false
	}
	return !c.hideDemos || !isDemo(title, attributes.Name)
}

// missingGamesCount counts the games of the titles database that are not in the library, with
// the filters of the Missing Games page, for the header and the statistics, as buildMissingGames.
func (web *Web) missingGamesCount() int {
	return web.missingGamesCounts().All
}

// consoleCounts are the games of a view of the Library: all of them, and those of each console.
type consoleCounts struct {
	All     int
	Switch1 int
	Switch2 int
}

func (c *consoleCounts) add(switch2 bool) {
	c.All++
	if switch2 {
		c.Switch2++
	} else {
		c.Switch1++
	}
}

// of is the number of the console chosen ("" both).
func (c consoleCounts) of(console string) int {
	switch console {
	case CONSOLE_SWITCH:
		return c.Switch1
	case CONSOLE_SWITCH2:
		return c.Switch2
	}
	return c.All
}

// missingGamesCounts counts the missing games as the Missing Games page shows them without a
// filter: each game once when the settings say so, of each console.
func (web *Web) missingGamesCounts() consoleCounts {
	return web.derived("missingGamesCounts", func() any {
		counts := consoleCounts{}
		switchDB, localDB := web.state.get()
		if switchDB == nil || localDB == nil {
			return counts
		}
		check := web.newMissingCheck(localDB, time.Now())
		once := web.newMissingOnce()
		for k, v := range switchDB.TitlesMap {
			if !check.missing(k, v) {
				continue
			}
			item := TitleItem{Id: strings.ToUpper(v.Attributes.Id), OriginalName: v.Attributes.Name}
			if once.takes(&item) {
				counts.add(item.IsSwitch2())
			}
		}
		return counts
	}).(consoleCounts)
}

// buildMissingGames lists every game of the titles database that is not in the library nor
// marked as owned. The games of the Switch 2 alone are listed only with "I have a Switch 2": a
// library of the Switch does not miss them (a game of the library is never missing, whatever
// its console).
func (web *Web) buildMissingGames(lang string) []TitleItem {
	items := []TitleItem{}

	switchDB, localDB := web.state.get()

	if switchDB == nil || localDB == nil {
		return items
	}

	check := web.newMissingCheck(localDB, time.Now())
	wished := web.wishes().Snapshot()

	for k, v := range switchDB.TitlesMap {
		if !check.missing(k, v) {
			continue
		}

		name := titleName(switchDB, lang, v.Attributes.Id, v.Attributes.Name)
		{
			imageUrl := coverUrl(switchDB, localDB, v.Attributes)

			release, err := intToTime(v.Attributes.ReleaseDate)
			if err != nil {
				web.sugarLogger.Error(fmt.Errorf("parsing time failed: %w", err))
			}

			items = append(items, TitleItem{
				ImageUrl:     imageUrl,
				Id:           strings.ToUpper(v.Attributes.Id),
				Name:         name,
				OriginalName: v.Attributes.Name,
				Region:       v.Attributes.Region,
				ReleaseDate:  release,
				Known:        true,
				Wished:       wished[strings.ToUpper(v.Attributes.Id)],
				Demo:         isDemo(v, v.Attributes.Name),
				Genres:       v.Attributes.Genres,
				Players:      v.Attributes.Players,
				Languages:    v.Attributes.Languages,
			})
		}
	}

	return items
}
