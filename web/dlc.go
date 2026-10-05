package web

import (
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"strings"
)

func (web *Web) HandleDLC() {
	fsPatterns := []string {
		"resources/layout.html",
		"resources/partials/filter.html",
		"resources/partials/pagination.html",
		"resources/pages/dlc.html",
	}

	web.HandleFiltered("/dlc.html", func(filter *TitleItemFilter) any {
		items, p := web.getMissingDLC(filter)
		return TitleItemsPageData {
			GlobalPageData: web.globalPageData("dlc"),
			TitleItems: items,
			Filter: filter,
			Pagination: p,
		}
	}, web.embedFS, fsPatterns...)
}

func (web *Web) getMissingDLC(filter *TitleItemFilter) ([]TitleItem, pagination.Pagination) {
	items := []TitleItem{}

	switchDB, localDB := web.state.get()

	if switchDB == nil || localDB == nil {
		return items, pagination.Calculate(filter.Page, filter.PerPage, 0)
	}

	settingsObj := settings.ReadSettings(web.dataFolder)
	missingDLC := process.ScanForMissingDLC(localDB.TitlesMap, switchDB.TitlesMap, toLowerSet(settingsObj.IgnoreDLCTitleIds))

	for _, v := range missingDLC {
		if filter.Matches(v.Attributes.Id, v.Attributes.Name) {
			var imageUrl string
			if v.Attributes.IconUrl != "" {
				imageUrl = v.Attributes.IconUrl
			} else if v.Attributes.BannerUrl != "" {
				imageUrl = v.Attributes.BannerUrl
			}

			items = append(items, TitleItem {
				ImageUrl:         imageUrl,
				Id:               strings.ToUpper(v.Attributes.Id),
				MissingDLC:       v.MissingDLC,
				Name:             v.Attributes.Name,
				Region:           v.Attributes.Region,
			})
		}
	}

	p := pagination.Calculate(filter.Page, filter.PerPage, len(items))

	if err := sortItems(filter, items); err != nil {
		web.sugarLogger.Error(err)
	}

	return items[p.Start:p.End], p
}
