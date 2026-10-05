package web

import (
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"path/filepath"
	"sort"
	"strings"
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
		}
	}, web.embedFS, fsPatterns...)
}

func (web *Web) getLibrary(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination) {
	items, p, _ := web.getLibraryWithFacets(filter, lang)
	return items, p
}

func (web *Web) getLibraryWithFacets(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination, LibraryFacets) {
	items := []TitleItem{}
	facets := LibraryFacets{Formats: []string{}}
	formats := map[string]struct{}{}
	switchDB, localDB := web.state.get()

	if localDB == nil {
		return items, pagination.Calculate(filter.Page, filter.PerPage, 0), facets
	}

	settingsObj := settings.ReadSettings(web.dataFolder)
	ignoredUpdates := toLowerSet(settingsObj.IgnoreUpdateTitleIds)
	ignoredDlc := toLowerSet(settingsObj.IgnoreDLCTitleIds)

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
		if !filter.Matches(v.File.Metadata.TitleId, name, originalName) {
			continue
		}

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
			Id:          strings.ToUpper(v.File.Metadata.TitleId),
			LocalUpdate: v.LatestUpdate,
			Name:        name,
			Type:        strings.ToUpper(getType(v)),
			Version:     version,
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
				if !owned && !ignored {
					item.MissingDlcCount++
				}
			}

			release, err := intToTime(title.Attributes.ReleaseDate)
			if err != nil {
				web.sugarLogger.Error(fmt.Errorf("parsing time failed: %w", err))
			}
			item.ReleaseDate = release
		}

		// the formats are offered whatever the other filters are
		if item.Type != "" {
			formats[item.Type] = struct{}{}
		}
		if filter.Format != "" && item.Type != filter.Format {
			continue
		}

		complete := title != nil && !item.UpdateAvailable && item.MissingDlcCount == 0
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

		items = append(items, item)
	}

	for format := range formats {
		facets.Formats = append(facets.Formats, format)
	}
	sort.Strings(facets.Formats)

	p := pagination.Calculate(filter.Page, filter.PerPage, len(items))

	if err := sortItems(filter, items); err != nil {
		web.sugarLogger.Error(err)
	}

	return items[p.Start:p.End], p, facets
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
