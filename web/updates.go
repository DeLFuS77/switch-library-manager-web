package web

import (
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"strings"
)

func (web *Web) HandleUpdates() {
	fsPatterns := []string {
		"resources/layout.html",
		"resources/partials/card.html",
		"resources/partials/filter.html",
		"resources/partials/pagination.html",
		"resources/partials/bulk.html",
		"resources/pages/updates.html",
	}

	web.HandleFiltered("/updates.html", func(filter *TitleItemFilter, lang string) any {
		items, p := web.getMissingUpdates(filter, lang)
		return TitleItemsPageData {
			GlobalPageData: web.globalPageData("updates"),
			TitleItems: items,
			Filter: filter,
			Pagination: p,
		}
	}, web.embedFS, fsPatterns...)
}

func (web *Web) getMissingUpdates(filter *TitleItemFilter, lang string) ([]TitleItem, pagination.Pagination) {
	return web.filterPage(filter, web.sorted("updates:"+lang, filter, func() []TitleItem { return web.buildMissingUpdates(lang) }))
}

// buildMissingUpdates lists the games and DLC of the library with a newer update.
func (web *Web) buildMissingUpdates(lang string) []TitleItem {
	items := []TitleItem{}

	switchDB, localDB := web.state.get()

	if switchDB == nil || localDB == nil {
		return items
	}

	missingUpdates := web.missingUpdates()

	for _, v := range missingUpdates {
		name := titleName(switchDB, lang, v.Attributes.Id, v.Attributes.Name)
		if name == "" {
			name = localTitleName(localDB, v.Attributes.Id)
		}
		if name == "" {
			name = strings.ToUpper(v.Attributes.Id)
		}
		{
			imageUrl := localImageUrl(localDB, v.Attributes.Id)
			if imageUrl == "" && v.Attributes.IconUrl != "" {
				imageUrl = v.Attributes.IconUrl
			} else if imageUrl == "" && v.Attributes.BannerUrl != "" {
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
				Name:             name,
				OriginalName:     v.Attributes.Name,
				Region:           v.Attributes.Region,
				ReleaseDate:      release,
				Type:             itemType,
				Known:            true,
			})
		}
	}

	return items
}
