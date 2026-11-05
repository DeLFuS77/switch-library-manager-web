package web

import (
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
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
			Facets:           facets,
			Setup:            web.setupStatus(),
			Stats:            web.getStatistics(lang),
			WhatsNew:         web.whatsNew(lang),
			UploadFolders:    web.uploadFolders(),
			SmartCollections: web.smartCollectionViews(filter, lang),
			SmartSavable:     filter.Filters(),
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
	f := web.newLibraryFilter(filter, lang, all)
	filter.searchIn(all)
	for index := range all {
		if f.takes(index) {
			f.matched = append(f.matched, index)
		}
	}
	f.sortFacets()
	items, p := f.page()
	return items, p, f.facets
}

// libraryFilter goes through the games of the Library with a filter: each step counts the games
// for the numbers shown on its own filters, then leaves out the ones its filter does not take, so
// the numbers of a filter count the games the filters before it take.
type libraryFilter struct {
	filter *TitleItemFilter
	all    []TitleItem
	facets LibraryFacets
	// positions of the matching items; only the shown page is copied
	matched          []int
	formats          map[string]struct{}
	regions          map[string]struct{}
	recentSince      time.Time
	collectionCounts map[string]int
	genreCounts      map[string]int
	languageCounts   map[string]int
	// the copies of the games of several regions left out, and the regions of the others
	leftOut      map[string]bool
	otherRegions map[string][]string
	merged       map[string]regionStatus
	// each game once: of a game the library has, no card of its other regions; of a game it does
	// not have, one card of all of them
	regionsByName map[string][]string
	ownedNames    map[string]bool
	shownMissing  map[string]bool
	plays         map[string]userdata.PlayEntry
}

func (web *Web) newLibraryFilter(filter *TitleItemFilter, lang string, all []TitleItem) *libraryFilter {
	f := &libraryFilter{filter: filter, all: all, matched: make([]int, 0, len(all)),
		facets:  LibraryFacets{Formats: []string{}, Regions: []string{}, DemosHidden: settings.ReadSettings(web.dataFolder).HideDemoGames},
		formats: map[string]struct{}{}, regions: map[string]struct{}{}, recentSince: time.Now().AddDate(0, 0, -recentDays),
		collectionCounts: map[string]int{}, genreCounts: map[string]int{}, languageCounts: map[string]int{},
		ownedNames: map[string]bool{}, shownMissing: map[string]bool{}}
	f.leftOut, f.otherRegions = web.regionCopies(lang)
	f.merged = web.mergedStatus(lang)
	if settings.ReadSettings(web.dataFolder).CountRegionsOnce {
		f.regionsByName = web.regionsByName()
		for i := range all {
			if !all[i].Missing {
				f.ownedNames[regionalName(all[i].OriginalName)] = true
			}
		}
	}
	f.plays = web.play().All(filter.player)
	f.facets.Play = map[string]int{}
	return f
}

// takes counts the game at index for the numbers of the filters, and reports whether the
// filter takes it.
func (f *libraryFilter) takes(index int) bool {
	item := &f.all[index]
	if f.leftOut[item.Id] || !f.filter.MatchesItem(item) {
		return false
	}
	// a game of several regions is up to date when one of its copies is
	if status, ok := f.merged[item.Id]; ok {
		copied := *item
		status.apply(&copied)
		item = &copied
	}
	return f.formatAndRegion(item) && f.collection(item) && f.attributes(item) && f.kindAndExtra(item) && f.play(item) && f.status(item)
}

// formatAndRegion counts the formats and regions of the game, and takes it in the format and
// the region chosen, once when each game counts once.
func (f *libraryFilter) formatAndRegion(item *TitleItem) bool {
	filter := f.filter
	// the formats are offered whatever the other filters are
	if item.Type != "" {
		f.formats[item.Type] = struct{}{}
	}
	if filter.Format != "" && item.Type != filter.Format {
		return false
	}
	if item.Region != "" {
		f.regions[item.Region] = struct{}{}
	}
	// a game of several regions is of each of the regions of its copies
	for _, region := range f.otherRegions[item.Id] {
		f.regions[region] = struct{}{}
	}
	if filter.Region != "" && item.Region != filter.Region && !hasValue(f.otherRegions[item.Id], regionCode(filter.Region)) {
		return false
	}
	if item.Missing && f.regionsByName != nil {
		name := regionalName(item.OriginalName)
		if _, known := f.regionsByName[name]; known {
			if f.ownedNames[name] || f.shownMissing[name] {
				return false
			}
			f.shownMissing[name] = true
		}
	}
	return true
}

// collection counts the collections of the game, and takes it in the collection chosen.
func (f *libraryFilter) collection(item *TitleItem) bool {
	inCollection := f.filter.Collection == ""
	for _, name := range item.Collections {
		f.collectionCounts[name]++
		if strings.EqualFold(name, f.filter.Collection) {
			inCollection = true
		}
	}
	return inCollection
}

// attributes counts the genres, players, languages and scores of the game before their own
// filters, and takes it with the ones chosen.
func (f *libraryFilter) attributes(item *TitleItem) bool {
	filter, facets := f.filter, &f.facets
	for _, genre := range item.Genres {
		f.genreCounts[genre]++
	}
	for _, language := range item.Languages {
		f.languageCounts[language]++
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
	countScore(facets, item.Score)
	return !((filter.Genre != "" && !hasValue(item.Genres, filter.Genre)) || !playersMatch(item.Players, filter.Players) ||
		(filter.GameLanguage != "" && !hasValue(item.Languages, filter.GameLanguage)) || !scoreMatches(item.Score, filter.Score))
}

// kindAndExtra counts the kinds and extras of the game whatever they are set to, within the
// collection, and takes it in the kind and the extra chosen.
func (f *libraryFilter) kindAndExtra(item *TitleItem) bool {
	filter, facets := f.filter, &f.facets
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
	recent := item.Added.After(f.recentSince)
	if recent {
		facets.Recent++
	}
	if item.Favorite {
		facets.Favorites++
	}
	if (filter.Kind == KIND_GAME && item.Demo) || (filter.Kind == KIND_DEMO && !item.Demo) || (filter.Kind == "" && facets.DemosHidden && item.Demo) {
		return false
	}
	if item.Cheats {
		facets.Cheats++
	}
	if item.CheatsOther {
		facets.CheatsOther++
	}
	if item.FirmwareTooNew {
		facets.FirmwareTooNew++
	}
	if (filter.Extra == EXTRA_NO_COVER && item.ImageUrl != "") || (filter.Extra == EXTRA_UNKNOWN && item.Known) || (filter.Extra == EXTRA_RECENT && !recent) || (filter.Extra == EXTRA_FAVORITES && !item.Favorite) || (filter.Extra == EXTRA_CHEATS && !item.Cheats) || (filter.Extra == EXTRA_CHEATS_OTHER && !item.CheatsOther) || (filter.Extra == EXTRA_FIRMWARE && !item.FirmwareTooNew) {
		return false
	}
	return true
}

// play counts how the game goes for the user whatever is chosen, then takes it in the state
// chosen.
func (f *libraryFilter) play(item *TitleItem) bool {
	if !item.Missing {
		state := f.plays[item.Id].Status
		if state == "" {
			state = "none"
		}
		f.facets.Play[state]++
		return f.filter.Play == "" || state == f.filter.Play
	}
	return f.filter.Play == ""
}

// status counts the game among the games with an update, DLC or complete, and takes it in the
// status chosen; a game not in the library has none, and shows with "All" only.
func (f *libraryFilter) status(item *TitleItem) bool {
	facets := &f.facets
	if item.Missing {
		if f.filter.Status != "" {
			return false
		}
		facets.Missing++
		facets.All++
		return true
	}
	// without a file its version is not known: it is neither up to date nor behind
	if item.NoFile {
		facets.All++
		return f.filter.Status == ""
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

	switch f.filter.Status {
	case STATUS_UPDATE:
		return item.UpdateAvailable
	case STATUS_DLC:
		return item.MissingDlcCount > 0
	case STATUS_COMPLETE:
		return complete
	}
	return true
}

// sortFacets puts the formats, regions, genres, languages and collections found in order.
func (f *libraryFilter) sortFacets() {
	facets := &f.facets
	for format := range f.formats {
		facets.Formats = append(facets.Formats, format)
	}
	sort.Strings(facets.Formats)
	for region := range f.regions {
		facets.Regions = append(facets.Regions, region)
	}
	sort.Strings(facets.Regions)
	facets.Genres = sortedCounts(f.genreCounts)
	facets.Languages = sortedCounts(f.languageCounts)
	for name, count := range f.collectionCounts {
		facets.Collections = append(facets.Collections, userdata.CollectionCount{Name: name, Count: count})
	}
	sort.Slice(facets.Collections, func(i, j int) bool {
		return strings.ToLower(facets.Collections[i].Name) < strings.ToLower(facets.Collections[j].Name)
	})
}

// page copies the games of the shown page, with their regions and how they go for the user.
func (f *libraryFilter) page() ([]TitleItem, pagination.Pagination) {
	// the items are already sorted
	p := pagination.Calculate(f.filter.Page, f.filter.PerPage, len(f.matched))
	items := make([]TitleItem, 0, p.End-p.Start)
	for _, index := range f.matched[p.Start:p.End] {
		item := f.all[index]
		item.OtherRegions, item.OtherFlags = len(f.otherRegions[item.Id]), f.otherRegions[item.Id]
		if status, ok := f.merged[item.Id]; ok {
			status.apply(&item)
		}
		if f.regionsByName != nil {
			item.MissingFlags = missingRegions(item, f.regionsByName)
			if room := max(coverFlags-1-len(item.OtherFlags), 0); len(item.MissingFlags) > room {
				item.MissingFlags, item.HiddenRegions = item.MissingFlags[:room], item.MissingFlags[room:]
			}
		}
		item.Play = f.plays[item.Id]
		items = append(items, item)
	}
	return items, p
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
	collections := web.collections().Snapshot()
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
	collections := web.collections().Snapshot()
	favorites := web.favorites().Snapshot()
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
		item.Score = timesToBeat[item.Id].Score()
		item.Collections = collections[item.Id]
		item.Selectable = true
		item.Favorite = favorites[item.Id]
		item.Cheats = cheats.has(item.Id, v.LatestUpdate)
		item.CheatsOther = !item.Cheats && cheats.hasAny(item.Id)
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

		if icon := gameIcon(switchDB, k, v); icon != "" {
			item.ImageUrl = "/i/" + icon
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

	return append(items, web.noFileItems(switchDB, localDB, lang, ignoredDlc, collections, favorites, timesToBeat)...)
}

// noFileItems are the games marked as owned that have no file in the library (see owned.go):
// games of the library without a version, so without updates or DLC to ask for. A game the titles
// database does not know is left out, as nothing is known of it.
func (web *Web) noFileItems(switchDB *db.SwitchTitlesDB, localDB *db.LocalSwitchFilesDB, lang string, ignoredDlc map[string]struct{},
	collections map[string][]string, favorites map[string]bool, timesToBeat map[string]TimeToBeat) []TitleItem {
	items := []TitleItem{}
	if switchDB == nil {
		return items
	}
	for prefix, marked := range web.markedPrefixes() {
		if local, ok := localDB.TitlesMap[prefix]; ok && local.BaseExist {
			continue
		}
		title := switchDB.TitlesMap[prefix]
		if title == nil || title.Attributes.Id == "" || !usableName(title.Attributes.Name) {
			continue
		}
		attributes := title.Attributes
		item := TitleItem{
			Id:           strings.ToUpper(attributes.Id),
			Name:         titleName(switchDB, lang, attributes.Id, attributes.Name),
			OriginalName: attributes.Name,
			ImageUrl:     coverUrl(switchDB, localDB, attributes),
			Region:       attributes.Region,
			Known:        true,
			Demo:         isDemo(title, attributes.Name),
			Genres:       attributes.Genres,
			Players:      attributes.Players,
			Languages:    attributes.Languages,
			Added:        marked,
			NoFile:       true,
		}
		item.ReleaseDate, _ = intToTime(attributes.ReleaseDate)
		item.TimeToBeat = timesToBeat[item.Id].Main()
		item.Score = timesToBeat[item.Id].Score()
		item.Collections = collections[item.Id]
		item.Favorite = favorites[item.Id]
		for id := range title.Dlc {
			if _, ignored := ignoredDlc[id]; !ignored {
				item.KnownDlc++
			}
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

// coverUrl returns the cover of a title: the one the user chose, the cached one of the library,
// else the icon or the banner of the titles database.
func coverUrl(switchDB *db.SwitchTitlesDB, localDB *db.LocalSwitchFilesDB, attributes db.TitleAttributes) string {
	if url := editedCover(switchDB, attributes.Id); url != "" {
		return url
	}
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
