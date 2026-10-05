package web

import (
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"strings"
)

func (web *Web) HandleMissing() {
	fsPatterns := []string {
		"resources/layout.html",
		"resources/partials/card.html",
		"resources/partials/filter.html",
		"resources/partials/pagination.html",
		"resources/pages/missing.html",
	}

	web.HandleFiltered("/missing.html", func(filter *TitleItemFilter, lang string) any {
		items, p := web.getMissingGames(filter, lang)
		return TitleItemsPageData {
			GlobalPageData: web.globalPageData("missing"),
			TitleItems: items,
			Filter: filter,
			Pagination: p,
		}
	}, web.embedFS, fsPatterns...)
}

func (web *Web) getMissingGames(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination) {
	return web.filterPage(filter, web.sorted("missingGames:"+lang, filter, func() []TitleItem { return web.buildMissingGames(lang) }))
}

// buildMissingGames lists every game of the titles database that is not in the library.
func (web *Web) buildMissingGames(lang string) []TitleItem {
	items := []TitleItem{}

	switchDB, localDB := web.state.get()

	if switchDB == nil || localDB == nil {
		return items
	}

	hideDemoGames := settings.ReadSettings(web.dataFolder).HideDemoGames

	for k, v := range switchDB.TitlesMap {
		if local, ok := localDB.TitlesMap[k]; ok && local.BaseExist {
			continue
		}

		if v.Attributes.Name == "" || v.Attributes.Id == "" {
			continue
		}

		if hideDemoGames && v.Attributes.IsDemo {
			continue
		}

		name := titleName(switchDB, lang, v.Attributes.Id, v.Attributes.Name)
		{
			imageUrl := localImageUrl(localDB, v.Attributes.Id)
			if imageUrl == "" && v.Attributes.IconUrl != "" {
				imageUrl = v.Attributes.IconUrl
			} else if imageUrl == "" && v.Attributes.BannerUrl != "" {
				imageUrl = v.Attributes.BannerUrl
			}

			release, err := intToTime(v.Attributes.ReleaseDate)
			if err != nil {
				web.sugarLogger.Error(fmt.Errorf("parsing time failed: %w", err))
			}

			items = append(items, TitleItem {
				ImageUrl:    imageUrl,
				Id:          strings.ToUpper(v.Attributes.Id),
				Name:        name,
				OriginalName: v.Attributes.Name,
				Region:      v.Attributes.Region,
				ReleaseDate: release,
				Known:       true,
			})
		}
	}

	return items
}
