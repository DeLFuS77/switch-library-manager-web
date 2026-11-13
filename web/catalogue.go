package web

import (
	"math"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
)

// the lists a catalogue can be of: the Library ("", its views All and My games) or the Missing
// games
const CATALOGUE_MISSING = "missing"

// CataloguePageData is the catalogue: every game of a view of the Library with its filter, on
// one sheet to print or save as PDF. The games go to the script of the page, which lays them out
// as covers or as a list; the sheet gives no path of a file.
type CataloguePageData struct {
	GlobalPageData
	Filter *TitleItemFilter
	// the view of the games: "all", "mine" or "missing", and the page they come from
	View    string
	BackUrl string
	Games   []catalogueGame
	Date    time.Time
}

// catalogueGame is a game as the catalogue shows it.
type catalogueGame struct {
	Name    string `json:"name"`
	Id      string `json:"id"`
	Version string `json:"version,omitempty"`
	Region  string `json:"region,omitempty"`
	Size    string `json:"size,omitempty"`
	// the thumbnail of the cover ("" without one)
	Cover string `json:"cover,omitempty"`
	// a game the library does not have (the views All and Missing ones)
	Missing bool `json:"missing,omitempty"`
}

// HandleCatalogue serves the catalogue of the Library and of the Missing games, with the same
// filter as the page it is opened from. Everyone who sees the library sees it.
func (web *Web) HandleCatalogue() {
	fsPatterns := []string{
		"resources/layout.html",
		"resources/partials/filter.html",
		"resources/pages/catalogue.html",
	}

	web.HandleFiltered("/catalogue.html", func(filter *TitleItemFilter, lang string) any {
		return web.catalogue(filter, lang)
	}, web.embedFS, fsPatterns...)
}

// catalogue lists every game of the filter, on one page: the order of the list it comes from,
// without its pages.
func (web *Web) catalogue(filter *TitleItemFilter, lang string) CataloguePageData {
	chosen := *filter
	all := *filter
	all.Page, all.PerPage = 1, math.MaxInt32
	data := CataloguePageData{GlobalPageData: web.globalPageData("catalogue"), Filter: &chosen, Date: time.Now()}
	var items []TitleItem
	if filter.List == CATALOGUE_MISSING {
		items, _, _ = web.getMissingGamesWithFacets(&all, lang)
		data.View, data.BackUrl = "missing", "/missing.html"
	} else {
		items, _, _ = web.getLibraryWithFacets(&all, lang)
		data.View, data.BackUrl = "all", "/index.html"
		if !filter.ShowsMissing() {
			data.View = "mine"
		}
	}
	// the same list, back where it was: both consoles are asked for, over the one the list
	// remembers
	console := chosen.Console
	if console == "" {
		console = CONSOLE_ALL
	}
	data.BackUrl += "?" + chosen.query("list", "", "console", console).Encode()
	data.Games = make([]catalogueGame, 0, len(items))
	for _, item := range items {
		game := catalogueGame{Name: item.Name, Id: item.Id, Version: item.Version, Region: item.Region, Missing: item.Missing}
		if item.Size > 0 {
			game.Size = i18n.FormatSizeIn(lang, item.Size)
		}
		if item.ImageUrl != "" {
			game.Cover = thumbUrl(item.ImageUrl)
		}
		data.Games = append(data.Games, game)
	}
	return data
}

// Wishlist reports whether the catalogue is of the games of the wishlist.
func (data CataloguePageData) Wishlist() bool {
	return data.View == "missing" && data.Filter.Status == userdata.STATUS_WANTED
}
