package web

import (
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func getType(gameFile *db.SwitchGameFiles) string {
	if gameFile.IsSplit {
		return "split"
	}

	if gameFile.MultiContent {
		return "multi-content"
	}

	ext := filepath.Ext(gameFile.File.ExtendedInfo.FileName)
	if len(ext) > 1 {
		return ext[1:]
	}

	return ""
}

// getLocalTitleName returns the best known name of a local title: the titles database,
// then the NACP (American English first), then the file name.
func getLocalTitleName(title *db.SwitchTitle, gameFile *db.SwitchGameFiles) string {
	if title != nil && usableName(title.Attributes.Name) {
		return title.Attributes.Name
	}

	if gameFile == nil {
		return ""
	}

	if gameFile.File.Metadata != nil && gameFile.File.Metadata.Ncap != nil {
		ncap := gameFile.File.Metadata.Ncap
		for _, language := range []string{"AmericanEnglish", "BritishEnglish"} {
			if name := ncap.TitleName[language].Title; usableName(name) {
				return name
			}
		}

		languages := make([]string, 0, len(ncap.TitleName))
		for language := range ncap.TitleName {
			languages = append(languages, language)
		}
		sort.Strings(languages)
		for _, language := range languages {
			if name := ncap.TitleName[language].Title; usableName(name) {
				return name
			}
		}
	}

	return strings.TrimSpace(db.ParseTitleNameFromFileName(gameFile.File.ExtendedInfo.FileName))
}

// demoName matches names that mark a demo, for demos the titles database does not flag: the
// word in brackets ("(Demo)", "【DEMO】"), "demo version", a name that ends with it ("… Demo",
// "… - Demo", "…-DEMO", "… Demo for KIOSK", "… DEMO EDITION", "… DEMO!"), a special demo
// ("Special Demo 2020", "Demo especial 2020") and the trial versions of the Japanese (体験版),
// Chinese (试玩版, 試玩版, 体验版, 體驗版) and Korean (체험판) stores. A word such as "Demon" is
// not the word demo.
var demoName = regexp.MustCompile(`(?i)[<(\[【]\s*(demo|trial)( version)?\s*!?\s*[>)\]】]|\bdemo version\b|(^|[\s\-–—:])(demo|démo|trial version)(\s+(edition|for kiosk|ver\.?))?\s*!?\s*$|\bspecial demo\b|(^|[\s:])demo especial\b|体験版|试玩版|試玩版|体验版|體驗版|체험판`)

// isDemo reports whether a game of the library is a demo.
func isDemo(title *db.SwitchTitle, name string) bool {
	return (title != nil && title.Attributes.IsDemo) || demoName.MatchString(name)
}

// localDemo reports whether the names a game gives itself, in any language, mark a demo: the
// titles database does not know every demo of every store.
func localDemo(meta *switchfs.ContentMetaAttributes) bool {
	if meta == nil || meta.Ncap == nil {
		return false
	}
	if meta.Ncap.Demo {
		return true
	}
	for _, name := range meta.Ncap.TitleName {
		if demoName.MatchString(name.Title) {
			return true
		}
	}
	return false
}

// usableName tells a name from a placeholder such as "-" that some entries of the titles
// database have.
func usableName(name string) bool {
	return strings.Trim(name, " -_.?·") != ""
}

func (web *Web) HandleIndex() {
	fsPatterns := []string{
		"resources/layout.html",
		"resources/partials/card.html",
		"resources/partials/filter.html",
		"resources/partials/pagination.html",
		"resources/pages/index.html",
	}

	web.HandleFiltered("/index.html", func(filter *TitleItemFilter, lang string) any {
		items, p, facets := web.getLibraryWithFacets(filter, lang)
		var recommendations []Recommendation
		// the library recommends games only from the favorites
		if filter.Plain() && facets.Favorites > 0 {
			recommendations = web.recommendations(lang)
		}
		return LibraryPageData{
			TitleItemsPageData: TitleItemsPageData{
				GlobalPageData:  web.globalPageData("index"),
				TitleItems:      items,
				Filter:          filter,
				Pagination:      p,
				Recommendations: recommendations,
				Views:           web.libraryViews(map[bool]string{true: "all", false: "mine"}[filter.ShowsMissing()], lang),
			},
			Facets:   facets,
			Setup:    web.setupStatus(),
			Stats:    web.getStatistics(lang),
			WhatsNew: web.whatsNew(lang),
		}
	}, web.embedFS, fsPatterns...)
}

func (web *Web) getLibrary(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination) {
	items, p, _ := web.getLibraryWithFacets(filter, lang)
	return items, p
}

func (web *Web) getLibraryWithFacets(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination, LibraryFacets) {
	all := web.sorted("library:"+lang, filter, func() []TitleItem { return web.buildLibrary(lang) })
	if filter.ShowsMissing() {
		all = web.sorted("library+missing:"+lang, filter, func() []TitleItem { return web.buildLibraryWithMissing(lang) })
	}

	// positions of the matching items; only the shown page is copied
	matched := make([]int, 0, len(all))
	facets := LibraryFacets{Formats: []string{}, Regions: []string{}, DemosHidden: settings.ReadSettings(web.dataFolder).HideDemoGames}
	formats := map[string]struct{}{}
	regions := map[string]struct{}{}
	recentSince := time.Now().AddDate(0, 0, -recentDays)
	collectionCounts := map[string]int{}
	genreCounts := map[string]int{}
	languageCounts := map[string]int{}
	leftOut, otherRegions := web.regionCopies(lang)

	for index := range all {
		item := &all[index]
		if leftOut[item.Id] || !filter.MatchesItem(item) {
			continue
		}

		// the formats are offered whatever the other filters are
		if item.Type != "" {
			formats[item.Type] = struct{}{}
		}
		if filter.Format != "" && item.Type != filter.Format {
			continue
		}
		if item.Region != "" {
			regions[item.Region] = struct{}{}
		}
		if filter.Region != "" && item.Region != filter.Region {
			continue
		}

		inCollection := filter.Collection == ""
		for _, name := range item.Collections {
			collectionCounts[name]++
			if strings.EqualFold(name, filter.Collection) {
				inCollection = true
			}
		}
		if !inCollection {
			continue
		}
		// genres, players and languages are counted before their own filters
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
			(filter.GameLanguage != "" && !hasValue(item.Languages, filter.GameLanguage)) {
			continue
		}
		// the kinds and extras are counted whatever they are set to, within the collection
		if item.Demo {
			facets.Demos++
		} else {
			facets.Games++
		}
		if item.ImageUrl == "" {
			facets.NoCover++
		}
		if !item.Known {
			facets.Unknown++
		}
		recent := item.Added.After(recentSince)
		if recent {
			facets.Recent++
		}
		if item.Favorite {
			facets.Favorites++
		}
		if (filter.Kind == KIND_GAME && item.Demo) || (filter.Kind == KIND_DEMO && !item.Demo) || (filter.Kind == "" && facets.DemosHidden && item.Demo) {
			continue
		}
		if item.Cheats {
			facets.Cheats++
		}
		if (filter.Extra == EXTRA_NO_COVER && item.ImageUrl != "") || (filter.Extra == EXTRA_UNKNOWN && item.Known) || (filter.Extra == EXTRA_RECENT && !recent) || (filter.Extra == EXTRA_FAVORITES && !item.Favorite) || (filter.Extra == EXTRA_CHEATS && !item.Cheats) {
			continue
		}

		if item.Missing {
			// games not in the library have no status: they show with "All" only
			if filter.Status != "" {
				continue
			}
			facets.Missing++
			facets.All++
			matched = append(matched, index)
			continue
		}
		complete := item.Known && !item.UpdateAvailable && item.MissingDlcCount == 0
		facets.All++
		if item.UpdateAvailable {
			facets.Update++
		}
		if item.MissingDlcCount > 0 {
			facets.Dlc++
		}
		if complete {
			facets.Complete++
		}

		switch filter.Status {
		case STATUS_UPDATE:
			if !item.UpdateAvailable {
				continue
			}
		case STATUS_DLC:
			if item.MissingDlcCount == 0 {
				continue
			}
		case STATUS_COMPLETE:
			if !complete {
				continue
			}
		}

		matched = append(matched, index)
	}

	for format := range formats {
		facets.Formats = append(facets.Formats, format)
	}
	sort.Strings(facets.Formats)
	for region := range regions {
		facets.Regions = append(facets.Regions, region)
	}
	sort.Strings(facets.Regions)
	facets.Genres = sortedCounts(genreCounts)
	facets.Languages = sortedCounts(languageCounts)
	for name, count := range collectionCounts {
		facets.Collections = append(facets.Collections, CollectionCount{Name: name, Count: count})
	}
	sort.Slice(facets.Collections, func(i, j int) bool {
		return strings.ToLower(facets.Collections[i].Name) < strings.ToLower(facets.Collections[j].Name)
	})

	// the items are already sorted
	p := pagination.Calculate(filter.Page, filter.PerPage, len(matched))
	items := make([]TitleItem, 0, p.End-p.Start)
	for _, index := range matched[p.Start:p.End] {
		item := all[index]
		item.OtherRegions = otherRegions[item.Id]
		items = append(items, item)
	}
	return items, p, facets
}

// libraryViews counts the games of each view of the Library. My games counts as the list of the
// Library does: without the demos when they are hidden, and the copies of a game from other
// regions once when the settings say so.
func (web *Web) libraryViews(current string, lang string) LibraryViews {
	mine := web.derived("libraryCount:"+lang, func() any {
		library := web.derived("library:"+lang, func() any { return web.buildLibrary(lang) }).([]TitleItem)
		hideDemos := settings.ReadSettings(web.dataFolder).HideDemoGames
		leftOut, _ := web.regionCopies(lang)
		count := 0
		for _, item := range library {
			if leftOut[item.Id] || (hideDemos && item.Demo) {
				continue
			}
			count++
		}
		return count
	}).(int)
	return LibraryViews{Current: current, Mine: mine, Missing: web.missingGamesCount()}
}

// buildLibraryWithMissing lists the games of the library and, greyed out, the games of the
// titles database that are not in it.
func (web *Web) buildLibraryWithMissing(lang string) []TitleItem {
	library := web.derived("library:"+lang, func() any { return web.buildLibrary(lang) }).([]TitleItem)
	missing := web.derived("missing:"+lang, func() any { return web.buildMissingGames(lang) }).([]TitleItem)
	collections := web.collections().snapshot()
	items := make([]TitleItem, 0, len(library)+len(missing))
	items = append(items, library...)
	for _, item := range missing {
		item.Missing = true
		item.Collections = collections[item.Id]
		items = append(items, item)
	}
	return items
}

// buildLibrary lists every game of the library with its status.
func (web *Web) buildLibrary(lang string) []TitleItem {
	items := []TitleItem{}
	switchDB, localDB := web.state.get()

	if localDB == nil {
		return items
	}

	settingsObj := settings.ReadSettings(web.dataFolder)
	ignoredUpdates := toLowerSet(settingsObj.IgnoreUpdateTitleIds)
	ignoredDlc := toLowerSet(settingsObj.IgnoreDLCTitleIds)
	added := web.history().addedTimes()
	collections := web.collections().snapshot()
	favorites := web.favorites().snapshot()
	timesToBeat := web.timesToBeat().snapshot()
	cheats := web.cheats()

	for k, v := range localDB.TitlesMap {
		if !v.BaseExist || v.File.Metadata == nil {
			continue
		}

		var title *db.SwitchTitle
		if switchDB != nil {
			title = switchDB.TitlesMap[k]
		}

		originalName := getLocalTitleName(title, v)
		name := titleName(switchDB, lang, v.File.Metadata.TitleId, originalName)

		version := ""
		if v.File.Metadata.Ncap != nil {
			version = v.File.Metadata.Ncap.DisplayVersion
		}

		if len(v.Updates) != 0 {
			version = ""
			if update, ok := v.Updates[v.LatestUpdate]; ok && update.Metadata != nil && update.Metadata.Ncap != nil {
				version = update.Metadata.Ncap.DisplayVersion
			}
		}

		item := TitleItem{
			Id:           strings.ToUpper(v.File.Metadata.TitleId),
			LocalUpdate:  v.LatestUpdate,
			Name:         name,
			OriginalName: originalName,
			Type:         strings.ToUpper(getType(v)),
			Version:      version,
			Known:        title != nil,
			Demo:         isDemo(title, originalName) || localDemo(v.File.Metadata),
			Size:         v.File.ExtendedInfo.Size,
		}
		item.Added = added["game:"+item.Id]
		item.TimeToBeat = timesToBeat[item.Id].Main()
		item.Collections = collections[item.Id]
		item.Selectable = true
		item.Favorite = favorites[item.Id]
		item.Cheats = cheats.has(item.Id, v.LatestUpdate)
		// every file once: a game card can hold the game, its update and DLC
		counted := map[string]bool{filepath.Join(v.File.ExtendedInfo.BaseFolder, v.File.ExtendedInfo.FileName): true}
		addSize := func(file db.ExtendedFileInfo) {
			if path := filepath.Join(file.BaseFolder, file.FileName); !counted[path] {
				counted[path] = true
				item.Size += file.Size
			}
		}
		for _, update := range v.Updates {
			addSize(update.ExtendedInfo)
		}
		for _, dlc := range v.Dlc {
			addSize(dlc.ExtendedInfo)
		}

		if required := installedRequirement(v); web.firmwareTooNew(required) {
			item.RequiredFirmware = firmwareVersion(required)
			item.FirmwareTooNew = true
		}

		if v.Icon != "" {
			item.ImageUrl = "/i/" + v.Icon
		} else if v.Banner != "" {
			item.ImageUrl = "/i/" + v.Banner
		}

		if title != nil {
			item.Region = title.Attributes.Region
			item.Genres = title.Attributes.Genres
			item.Players = title.Attributes.Players
			item.Languages = title.Attributes.Languages

			if _, ignored := ignoredUpdates[strings.ToLower(v.File.Metadata.TitleId)]; !ignored {
				for version := range title.Updates {
					if version > v.LatestUpdate {
						item.UpdateAvailable = true
						break
					}
				}
			}
			for id := range title.Dlc {
				_, owned := v.Dlc[id]
				_, ignored := ignoredDlc[id]
				if owned {
					item.DlcOwned++
					item.DlcTotal++
				} else if !ignored {
					item.MissingDlcCount++
					item.DlcTotal++
				}
			}

			release, err := intToTime(title.Attributes.ReleaseDate)
			if err != nil {
				web.sugarLogger.Error(fmt.Errorf("parsing time failed: %w", err))
			}
			item.ReleaseDate = release
		}

		items = append(items, item)
	}

	return items
}

// localTitleName is the name of a title of the library taken from its files, for titles
// the titles database has no name for.
func localTitleName(localDB *db.LocalSwitchFilesDB, titleId string) string {
	if localDB == nil {
		return ""
	}
	prefix, err := db.TitleIDPrefix(titleId)
	if err != nil {
		return ""
	}
	local, ok := localDB.TitlesMap[prefix]
	if !ok {
		return ""
	}
	for id, dlc := range local.Dlc {
		if strings.EqualFold(id, titleId) {
			return strings.TrimSpace(db.ParseTitleNameFromFileName(dlc.ExtendedInfo.FileName))
		}
	}
	if local.BaseExist {
		return getLocalTitleName(nil, local)
	}
	for _, update := range local.Updates {
		return strings.TrimSpace(db.ParseTitleNameFromFileName(update.ExtendedInfo.FileName))
	}
	return ""
}

// coverUrl returns the cover of a title: the cached one of the library, else the icon or
// the banner of the titles database.
func coverUrl(localDB *db.LocalSwitchFilesDB, attributes db.TitleAttributes) string {
	if url := localImageUrl(localDB, attributes.Id); url != "" {
		return url
	}
	if attributes.IconUrl != "" {
		return attributes.IconUrl
	}
	return attributes.BannerUrl
}

// localImageUrl returns the cover of a game from the local image cache, if it was
// downloaded during a scan, so pages work without access to the Nintendo servers.
func localImageUrl(localDB *db.LocalSwitchFilesDB, titleId string) string {
	if localDB == nil {
		return ""
	}
	prefix, err := db.TitleIDPrefix(titleId)
	if err != nil {
		return ""
	}
	if local, ok := localDB.TitlesMap[prefix]; ok {
		if local.Icon != "" {
			return "/i/" + local.Icon
		}
		if local.Banner != "" {
			return "/i/" + local.Banner
		}
	}
	return ""
}
