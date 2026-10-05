package web

import (
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"strings"
)

func (web *Web) HandleUpdates() {
	fsPatterns := []string {
		"resources/layout.html",
		"resources/partials/card.html",
		"resources/partials/filter.html",
		"resources/partials/pagination.html",
		"resources/pages/updates.html",
	}

	web.HandleFiltered("/updates.html", func(filter *TitleItemFilter) any {
		items, p := web.getMissingUpdates(filter)
		return TitleItemsPageData {
			GlobalPageData: web.globalPageData("updates"),
			TitleItems: items,
			Filter: filter,
			Pagination: p,
		}
	}, web.embedFS, fsPatterns...)
}

func (web *Web) getMissingUpdates(filter *TitleItemFilter) ([]TitleItem, pagination.Pagination) {
	items := []TitleItem{}

	switchDB, localDB := web.state.get()

	if switchDB == nil || localDB == nil {
		return items, pagination.Calculate(filter.Page, filter.PerPage, 0)
	}

	missingUpdates := process.ScanForMissingUpdates(localDB.TitlesMap, switchDB.TitlesMap)

	for _, v := range missingUpdates {
		if filter.Matches(v.Attributes.Id, v.Attributes.Name) {
			var imageUrl string
			if v.Attributes.IconUrl != "" {
				imageUrl = v.Attributes.IconUrl
			} else if v.Attributes.BannerUrl != "" {
				imageUrl = v.Attributes.BannerUrl
			}

			latest, err := strToTime("2006-01-02", v.LatestUpdateDate)
			if err != nil {
				web.sugarLogger.Error(fmt.Errorf("parsing time failed: %w", err))
			}

			release, err := intToTime(v.Attributes.ReleaseDate)
			if err != nil {
				web.sugarLogger.Error(fmt.Errorf("parsing time failed: %w", err))
			}

			itemType := ""
			if v.Meta != nil {
				itemType = strings.ToUpper(v.Meta.Type)
			}

			items = append(items, TitleItem {
				ImageUrl:         imageUrl,
				Id:               strings.ToUpper(v.Attributes.Id),
				LatestUpdate:     v.LatestUpdate,
				LatestUpdateDate: latest,
				LocalUpdate:      v.LocalUpdate,
				Name:             v.Attributes.Name,
				Region:           v.Attributes.Region,
				ReleaseDate:      release,
				Type:             itemType,
			})
		}
	}

	p := pagination.Calculate(filter.Page, filter.PerPage, len(items))

	if err := sortItems(filter, items); err != nil {
		web.sugarLogger.Error(err)
	}

	return items[p.Start:p.End], p
}
