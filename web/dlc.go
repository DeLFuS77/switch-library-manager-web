package web

import (
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
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
	return web.filterPage(filter, web.sorted("dlc:"+lang, filter, func() []TitleItem { return web.buildMissingDLC(lang) }))
}

// buildMissingDLC lists the games of the library with DLC that are not in the library.
func (web *Web) buildMissingDLC(lang string) []TitleItem {
	items := []TitleItem{}

	switchDB, localDB := web.state.get()

	if switchDB == nil || localDB == nil {
		return items
	}

	missingDLC := web.missingDLC()

	for _, v := range missingDLC {
		name := titleName(switchDB, lang, v.Attributes.Id, v.Attributes.Name)
		if name == "" {
			name = localTitleName(localDB, v.Attributes.Id)
		}
		if name == "" {
			name = strings.ToUpper(v.Attributes.Id)
		}
		{
			missingDlc := make([]db.TitleAttributes, len(v.MissingDLCItems))
			for i, dlc := range v.MissingDLCItems {
				dlc.Name = titleName(switchDB, lang, dlc.Id, dlc.Name)
				if dlc.Name == "" {
					dlc.Name = translate(lang, "DLC without a name in the titles database")
				}
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
				OriginalName:     v.Attributes.Name,
				Region:           v.Attributes.Region,
				Known:            true,
			})
		}
	}

	return items
}
