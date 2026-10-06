package web

import (
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// Updates and DLC of games that are not in the library are reported one by one in Issues
// ("base file is missing"). The orphans page groups them by game, with what the library has
// of each one, so it is clear which games are missing and can be wished or looked for.

func (web *Web) HandleOrphans() {
	fsPatterns := []string{
		"resources/layout.html",
		"resources/partials/card.html",
		"resources/partials/filter.html",
		"resources/partials/pagination.html",
		"resources/pages/orphans.html",
	}

	web.HandleFiltered("/orphans.html", func(filter *TitleItemFilter, lang string) any {
		items, p := web.filterPage(filter, web.sorted("orphans:"+lang, filter, func() []TitleItem { return web.buildOrphans(lang) }))
		return TitleItemsPageData{
			GlobalPageData: web.globalPageData("issues"),
			WishedCount:    web.wishes().count(),
			TitleItems:     items,
			Filter:         filter,
			Pagination:     p,
		}
	}, web.embedFS, fsPatterns...)
}

// orphanCount is the number of games with updates or DLC in the library but not the game.
func (web *Web) orphanCount() int {
	return web.derived("orphanCount", func() any { return len(web.buildOrphans(DEFAULT_LANGUAGE)) }).(int)
}

// buildOrphans lists the games of which the library has updates or DLC but not the game.
func (web *Web) buildOrphans(lang string) []TitleItem {
	items := []TitleItem{}
	switchDB, localDB := web.state.get()
	if localDB == nil {
		return items
	}
	wished := web.wishes().snapshot()
	for prefix, local := range localDB.TitlesMap {
		if local.BaseExist || (len(local.Updates) == 0 && len(local.Dlc) == 0) {
			continue
		}
		id := strings.ToUpper(prefix + "000")
		var title *db.SwitchTitle
		if switchDB != nil {
			title = switchDB.TitlesMap[prefix]
		}
		item := TitleItem{
			Id:            id,
			OrphanUpdates: len(local.Updates),
			OrphanDlc:     len(local.Dlc),
			Known:         title != nil,
			Wished:        wished[id],
		}
		attributes := db.TitleAttributes{Id: id}
		if title != nil {
			attributes = title.Attributes
			item.Region = attributes.Region
			item.ReleaseDate, _ = intToTime(attributes.ReleaseDate)
			item.Genres, item.Players, item.Languages = attributes.Genres, attributes.Players, attributes.Languages
		}
		item.OriginalName = attributes.Name
		item.Name = titleName(switchDB, lang, id, attributes.Name)
		if item.Name == "" {
			item.Name = localTitleName(localDB, id)
		}
		if item.Name == "" {
			item.Name = id
		}
		item.ImageUrl = coverUrl(localDB, attributes)
		items = append(items, item)
	}
	return items
}
