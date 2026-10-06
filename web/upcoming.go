package web

import (
	"sort"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The titles database has the games that can be pre-ordered in the eShop with their release
// date. The upcoming page shows them by month, with the wishlist and the series of the library
// first, and the games released in the last days.

const (
	// games released in these last days are shown as just released
	justReleasedDays = 14
	// the filter of the games on the wishlist or of a series of the library
	STATUS_FOR_YOU = STATUS_WANTED
)

// UpcomingGame is a game not released yet or just released, and why it matters to the user.
type UpcomingGame struct {
	Item TitleItem
	// the series of the library the game belongs to, with how many games of it the library has
	Saga *SagaProgress
}

// ForYou reports whether the game is on the wishlist or of a series of the library.
func (game UpcomingGame) ForYou() bool {
	return game.Item.Wished || game.Saga != nil
}

// UpcomingMonth is the games released in a month, the first first.
type UpcomingMonth struct {
	Month time.Time
	Games []UpcomingGame
}

// UpcomingPageData is the page of the upcoming games.
type UpcomingPageData struct {
	GlobalPageData
	Filter       *TitleItemFilter
	JustReleased []UpcomingGame
	Months       []UpcomingMonth
	// upcoming games, of them on the wishlist or of a series of the library, and shown
	Total  int
	ForYou int
	Shown  int
}

// today is the date of a time, without the time, as the release dates of the titles database.
func today(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// upcomingGames returns the games released from some days ago on that are not in the library,
// one of each name, the first released first.
func (web *Web) upcomingGames(lang string) []UpcomingGame {
	return web.derived("upcoming:"+lang, func() any {
		return web.upcomingGamesAt(lang, time.Now())
	}).([]UpcomingGame)
}

func (web *Web) upcomingGamesAt(lang string, now time.Time) []UpcomingGame {
	result := []UpcomingGame{}
	switchDB, localDB := web.state.get()
	if switchDB == nil {
		return result
	}
	from := today(now).AddDate(0, 0, -justReleasedDays)
	hideDemos := settings.ReadSettings(web.dataFolder).HideDemoGames
	wished := web.wishes().snapshot()

	// the names of the games of the library, in every store
	owned := map[string]bool{}
	if localDB != nil {
		for prefix, local := range localDB.TitlesMap {
			if title := switchDB.TitlesMap[prefix]; local.BaseExist && title != nil {
				owned[regionalName(title.Attributes.Name)] = true
			}
		}
	}
	sagas := map[string]*SagaProgress{}
	for _, saga := range web.sagaProgress(lang) {
		saga := saga
		sagas[saga.family] = &saga
	}

	byName := map[string]int{}
	for prefix, title := range switchDB.TitlesMap {
		attributes := title.Attributes
		if attributes.Id == "" || attributes.Name == "" || attributes.ReleaseDate == 0 {
			continue
		}
		release, err := intToTime(attributes.ReleaseDate)
		if err != nil || release.Before(from) {
			continue
		}
		if (hideDemos && isDemo(title, attributes.Name)) || title.Attributes.IsDemo || notAGame.MatchString(attributes.Name) {
			continue
		}
		name := regionalName(attributes.Name)
		if owned[name] {
			continue
		}
		if localDB != nil {
			if local, ok := localDB.TitlesMap[prefix]; ok && local.BaseExist {
				continue
			}
		}
		id := strings.ToUpper(attributes.Id)
		game := UpcomingGame{Item: TitleItem{
			Id:           id,
			Name:         titleName(switchDB, lang, id, attributes.Name),
			OriginalName: attributes.Name,
			ImageUrl:     coverUrl(localDB, attributes),
			Region:       attributes.Region,
			ReleaseDate:  release,
			Known:        true,
			Missing:      true,
			Wished:       wished[id],
			Genres:       attributes.Genres,
		}}
		if family := web.sagaFamilyOf(id, attributes.Name); family != "" {
			game.Saga = sagas[family]
		}
		// the same game in several stores: once, the earliest date, wished if a copy is
		if i, ok := byName[name]; ok {
			kept := &result[i]
			if game.Item.ReleaseDate.Before(kept.Item.ReleaseDate) {
				kept.Item.ReleaseDate = game.Item.ReleaseDate
			}
			kept.Item.Wished = kept.Item.Wished || game.Item.Wished
			if strings.ContainsAny(kept.Item.Name, "（(") && !strings.ContainsAny(game.Item.Name, "（(") {
				game.Item.ReleaseDate, game.Item.Wished = kept.Item.ReleaseDate, kept.Item.Wished
				*kept = game
			}
			if kept.Item.ImageUrl == "" {
				kept.Item.ImageUrl = game.Item.ImageUrl
			}
			continue
		}
		byName[name] = len(result)
		result = append(result, game)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i].Item, result[j].Item
		if !a.ReleaseDate.Equal(b.ReleaseDate) {
			return a.ReleaseDate.Before(b.ReleaseDate)
		}
		if result[i].ForYou() != result[j].ForYou() {
			return result[i].ForYou()
		}
		return sortName(a.Name) < sortName(b.Name)
	})
	return result
}

// upcomingPage groups the upcoming games by month, the just released ones apart.
func upcomingPage(games []UpcomingGame, filter *TitleItemFilter, now time.Time) UpcomingPageData {
	data := UpcomingPageData{Filter: filter}
	day := today(now)
	var query *searchQuery
	if filter.Keyword != "" {
		query = newSearchQuery(filter.Keyword)
	}
	for _, game := range games {
		released := game.Item.ReleaseDate.Before(day)
		if !released {
			data.Total++
			if game.ForYou() {
				data.ForYou++
			}
		}
		if filter.Status == STATUS_FOR_YOU && !game.ForYou() {
			continue
		}
		if query != nil {
			text := searchText(game.Item.Name + " " + game.Item.OriginalName)
			if !query.matches(text, strings.Fields(text)) {
				continue
			}
		}
		if released {
			data.JustReleased = append(data.JustReleased, game)
			continue
		}
		data.Shown++
		month := time.Date(game.Item.ReleaseDate.Year(), game.Item.ReleaseDate.Month(), 1, 0, 0, 0, 0, time.UTC)
		if n := len(data.Months); n == 0 || !data.Months[n-1].Month.Equal(month) {
			data.Months = append(data.Months, UpcomingMonth{Month: month})
		}
		data.Months[len(data.Months)-1].Games = append(data.Months[len(data.Months)-1].Games, game)
	}
	// the last released first
	for i, j := 0, len(data.JustReleased)-1; i < j; i, j = i+1, j-1 {
		data.JustReleased[i], data.JustReleased[j] = data.JustReleased[j], data.JustReleased[i]
	}
	return data
}

func (web *Web) HandleUpcoming() {
	fsPatterns := []string{
		"resources/layout.html",
		"resources/partials/components.html",
		"resources/pages/upcoming.html",
	}

	web.HandleFiltered("/upcoming.html", func(filter *TitleItemFilter, lang string) any {
		data := upcomingPage(web.upcomingGames(lang), filter, time.Now())
		data.GlobalPageData = web.globalPageData("upcoming")
		return data
	}, web.embedFS, fsPatterns...)
}
