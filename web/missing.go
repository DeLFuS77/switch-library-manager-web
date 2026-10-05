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
	items := []TitleItem{}

	switchDB, localDB := web.state.get()

	if switchDB == nil || localDB == nil {
		return items, pagination.Calculate(filter.Page, filter.PerPage, 0)
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
		if filter.Matches(v.Attributes.Id, name, v.Attributes.Name) {
			var imageUrl string
			if v.Attributes.IconUrl != "" {
				imageUrl = v.Attributes.IconUrl
			} else if v.Attributes.BannerUrl != "" {
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
				Region:      v.Attributes.Region,
				ReleaseDate: release,
			})
		}
	}

	p := pagination.Calculate(filter.Page, filter.PerPage, len(items))

	if err := sortItems(filter, items); err != nil {
		web.sugarLogger.Error(err)
	}

	return items[p.Start:p.End], p
}
