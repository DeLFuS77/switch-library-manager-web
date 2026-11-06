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
			Views:          web.libraryViews("missing", lang).withConsole(filter, facets, settings.ReadSettings(web.dataFolder).HasSwitch2),
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
		// the console, counted before its own choice as in the Library
		switch2 := item.IsSwitch2()
		if switch2 {
			facets.Switch2++
		} else {
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
		if (filter.Kind == KIND_GAME && item.Demo) || (filter.Kind == KIND_DEMO && !item.Demo) {
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
		items = append(items, all[index])
	}
	return items, p, facets
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
	return web.derived("missingGamesCount", func() any {
		switchDB, localDB := web.state.get()
		if switchDB == nil || localDB == nil {
			return 0
		}
		check := web.newMissingCheck(localDB, time.Now())
		count := 0
		for k, v := range switchDB.TitlesMap {
			if check.missing(k, v) {
				count++
			}
		}
		return count
	}).(int)
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
