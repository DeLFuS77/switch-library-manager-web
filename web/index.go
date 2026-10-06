package web

import (
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/settings"
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
	if title != nil && title.Attributes.Name != "" {
		return title.Attributes.Name
	}

	if gameFile == nil {
		return ""
	}

	if gameFile.File.Metadata != nil && gameFile.File.Metadata.Ncap != nil {
		ncap := gameFile.File.Metadata.Ncap
		if name := ncap.TitleName["AmericanEnglish"].Title; name != "" {
			return name
		}

		languages := make([]string, 0, len(ncap.TitleName))
		for language := range ncap.TitleName {
			languages = append(languages, language)
		}
		sort.Strings(languages)
		for _, language := range languages {
			if name := ncap.TitleName[language].Title; name != "" {
				return name
			}
		}
	}

	return strings.TrimSpace(db.ParseTitleNameFromFileName(gameFile.File.ExtendedInfo.FileName))
}

// demoName matches names that mark a demo, for demos the titles database does not flag.
var demoName = regexp.MustCompile(`(?i)[<(\[]\s*(demo|trial)( version)?\s*[>)\]]|\bdemo version\b|体験版`)

// isDemo reports whether a game of the library is a demo.
func isDemo(title *db.SwitchTitle, name string) bool {
	return (title != nil && title.Attributes.IsDemo) || demoName.MatchString(name)
}

func (web *Web) HandleIndex() {
	fsPatterns := []string {
		"resources/layout.html",
		"resources/partials/card.html",
		"resources/partials/filter.html",
		"resources/partials/pagination.html",
		"resources/pages/index.html",
	}

	web.HandleFiltered("/index.html", func(filter *TitleItemFilter, lang string) any {
		items, p, facets := web.getLibraryWithFacets(filter, lang)
		return LibraryPageData {
			TitleItemsPageData: TitleItemsPageData {
				GlobalPageData: web.globalPageData("index"),
				TitleItems: items,
				Filter: filter,
				Pagination: p,
			},
			Facets: facets,
			Setup: web.setupStatus(),
			Stats: web.getStatistics(lang),
		}
	}, web.embedFS, fsPatterns...)
}

func (web *Web) getLibrary(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination) {
	items, p, _ := web.getLibraryWithFacets(filter, lang)
	return items, p
}

func (web *Web) getLibraryWithFacets(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination, LibraryFacets) {
	all := web.sorted("library:"+lang, filter, func() []TitleItem { return web.buildLibrary(lang) })

	// positions of the matching items; only the shown page is copied
	matched := make([]int, 0, len(all))
	facets := LibraryFacets{Formats: []string{}, Regions: []string{}, DemosHidden: settings.ReadSettings(web.dataFolder).HideDemoGames}
	formats := map[string]struct{}{}
	regions := map[string]struct{}{}
	recentSince := time.Now().AddDate(0, 0, -recentDays)

	for index := range all {
		item := &all[index]
		if !filter.Matches(item.Id, item.Name, item.OriginalName) {
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

		// the kinds and extras are counted whatever they are set to
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
		if (filter.Kind == KIND_GAME && item.Demo) || (filter.Kind == KIND_DEMO && !item.Demo) || (filter.Kind == "" && facets.DemosHidden && item.Demo) {
			continue
		}
		if (filter.Extra == EXTRA_NO_COVER && item.ImageUrl != "") || (filter.Extra == EXTRA_UNKNOWN && item.Known) || (filter.Extra == EXTRA_RECENT && !recent) {
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

	// the items are already sorted
	p := pagination.Calculate(filter.Page, filter.PerPage, len(matched))
	items := make([]TitleItem, 0, p.End-p.Start)
	for _, index := range matched[p.Start:p.End] {
		items = append(items, all[index])
	}
	return items, p, facets
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

		item := TitleItem {
			Id:           strings.ToUpper(v.File.Metadata.TitleId),
			LocalUpdate:  v.LatestUpdate,
			Name:         name,
			OriginalName: originalName,
			Type:         strings.ToUpper(getType(v)),
			Version:      version,
			Known:        title != nil,
			Demo:         isDemo(title, originalName),
			Size:         v.File.ExtendedInfo.Size,
		}
		item.Added = added["game:"+item.Id]
		for _, update := range v.Updates {
			item.Size += update.ExtendedInfo.Size
		}
		for _, dlc := range v.Dlc {
			item.Size += dlc.ExtendedInfo.Size
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

// localImageUrl returns the cover of a game from the local image cache, if it was
// downloaded during a scan, so pages work without access to the Nintendo servers.
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
