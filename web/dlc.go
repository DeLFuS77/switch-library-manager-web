package web

import (
	"github.com/dtrunk90/switch-library-manager-web/db"
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
		"resources/partials/bulk.html",
		"resources/pages/dlc.html",
	}

	web.HandleFiltered("/dlc.html", func(filter *TitleItemFilter, lang string) any {
		items, p := web.getMissingDLC(filter, lang)
		return TitleItemsPageData {
			GlobalPageData: web.globalPageData("dlc"),
			TitleItems: items,
			Filter: filter,
			Pagination: p,
		}
	}, web.embedFS, fsPatterns...)
}

func (web *Web) getMissingDLC(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination) {
	items := []TitleItem{}

	switchDB, localDB := web.state.get()

	if switchDB == nil || localDB == nil {
		return items, pagination.Calculate(filter.Page, filter.PerPage, 0)
	}

	settingsObj := settings.ReadSettings(web.dataFolder)
	missingDLC := process.ScanForMissingDLC(localDB.TitlesMap, switchDB.TitlesMap, toLowerSet(settingsObj.IgnoreDLCTitleIds))

	for _, v := range missingDLC {
		name := titleName(switchDB, lang, v.Attributes.Id, v.Attributes.Name)
		if filter.Matches(v.Attributes.Id, name, v.Attributes.Name) {
			missingDlc := make([]db.TitleAttributes, len(v.MissingDLCItems))
			for i, dlc := range v.MissingDLCItems {
				dlc.Name = titleName(switchDB, lang, dlc.Id, dlc.Name)
				missingDlc[i] = dlc
			}

			imageUrl := localImageUrl(localDB, v.Attributes.Id)
			if imageUrl == "" && v.Attributes.IconUrl != "" {
				imageUrl = v.Attributes.IconUrl
			} else if imageUrl == "" && v.Attributes.BannerUrl != "" {
				imageUrl = v.Attributes.BannerUrl
			}

			items = append(items, TitleItem {
				ImageUrl:         imageUrl,
				Id:               strings.ToUpper(v.Attributes.Id),
				MissingDLC:       v.MissingDLC,
				MissingDLCItems:  missingDlc,
				Name:             name,
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
