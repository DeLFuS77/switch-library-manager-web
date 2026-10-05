package web

import (
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
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
		items, p := web.getLibrary(filter, lang)
		return TitleItemsPageData {
			GlobalPageData: web.globalPageData("index"),
			TitleItems: items,
			Filter: filter,
			Pagination: p,
		}
	}, web.embedFS, fsPatterns...)
}

func (web *Web) getLibrary(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination) {
	items := []TitleItem{}
	switchDB, localDB := web.state.get()

	if localDB == nil {
		return items, pagination.Calculate(filter.Page, filter.PerPage, 0)
	}

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

			release, err := intToTime(title.Attributes.ReleaseDate)
			if err != nil {
				web.sugarLogger.Error(fmt.Errorf("parsing time failed: %w", err))
			}
			item.ReleaseDate = release
		}

		items = append(items, item)
	}

	p := pagination.Calculate(filter.Page, filter.PerPage, len(items))

	if err := sortItems(filter, items); err != nil {
		web.sugarLogger.Error(err)
	}

	return items[p.Start:p.End], p
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
