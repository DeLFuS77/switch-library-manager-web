package web

import (
	"sort"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func (web *Web) HandleDLC() {
	fsPatterns := []string{
		"resources/layout.html",
		"resources/partials/filter.html",
		"resources/partials/pagination.html",
		"resources/partials/bulk.html",
		"resources/pages/dlc.html",
	}

	web.HandleFiltered("/dlc.html", func(filter *TitleItemFilter, lang string) any {
		items, p := web.getMissingDLC(filter, lang)
		return TitleItemsPageData{
			GlobalPageData: web.globalPageData("dlc"),
			TitleItems:     items,
			Filter:         filter,
			Pagination:     p,
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
	hidden := web.languageHiddenDLC()
	ignored := toLowerSet(settings.ReadSettings(web.dataFolder).IgnoreDLCTitleIds)
	day := dateNumber(time.Now())

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
					dlc.Name = i18n.Translate(lang, "DLC without a name in the titles database")
				}
				missingDlc[i] = dlc
			}

			// the language packs in the languages not chosen, to see them all the same (#200)
			hiddenDlc := []db.TitleAttributes{}
			if prefix, err := db.TitleIDPrefix(v.Attributes.Id); err == nil && len(hidden) > 0 {
				local := localDB.TitlesMap[prefix]
				if title := switchDB.TitlesMap[prefix]; title != nil && local != nil {
					for id, dlc := range title.Dlc {
						_, languageHidden := hidden[id]
						_, owned := local.Dlc[id]
						_, ignoredHere := ignored[id]
						if languageHidden && !owned && !ignoredHere && dlc.OutBy(day) {
							dlc.Name = titleName(switchDB, lang, dlc.Id, dlc.Name)
							dlc.Id = strings.ToUpper(dlc.Id)
							hiddenDlc = append(hiddenDlc, dlc)
						}
					}
				}
			}
			sort.Slice(hiddenDlc, func(i, j int) bool { return hiddenDlc[i].Name < hiddenDlc[j].Name })

			imageUrl := coverUrl(switchDB, localDB, v.Attributes)

			items = append(items, TitleItem{
				ImageUrl:        imageUrl,
				Id:              strings.ToUpper(v.Attributes.Id),
				MissingDLC:      v.MissingDLC,
				MissingDLCItems: missingDlc,
				LanguageHidden:  hiddenDlc,
				Name:            name,
				OriginalName:    v.Attributes.Name,
				Region:          v.Attributes.Region,
				Known:           true,
				DlcOwned:        v.OwnedDLC,
				DlcTotal:        v.OwnedDLC + len(missingDlc),
			})
		}
	}

	return items
}
