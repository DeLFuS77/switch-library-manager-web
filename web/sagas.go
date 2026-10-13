package web

import (
	"encoding/base64"
	"github.com/gorilla/mux"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/pagination"
)

// The series of the library with how many of their games it has: "4 of 6", with the games that
// are missing, on their own page and on the page of each game.

const (
	sagasPerPage = 24
	// missing games shown in each series of the page
	sagaMissingShown = 6
)

// SagaProgress is a series with the games the library has of it.
type SagaProgress struct {
	// the key of the series (see sagaIndex.family)
	family   string
	Name     string
	ImageUrl string
	Owned    int
	Total    int
	// the games of the series, the oldest first
	Games []TitleItem
	// search text of the series and its games
	searchKey   string
	searchWords []string
}

// Url is the page of the series, with all its games.
func (s SagaProgress) Url() string {
	if s.family == "" {
		return ""
	}
	return "/saga/" + base64.RawURLEncoding.EncodeToString([]byte(s.family)) + ".html"
}

func (s SagaProgress) Percent() int {
	if s.Total == 0 {
		return 0
	}
	return s.Owned * 100 / s.Total
}

func (s SagaProgress) Complete() bool {
	return s.Total > 0 && s.Owned == s.Total
}

// MissingGames returns the first games of the series that are not in the library.
func (s SagaProgress) MissingGames() []TitleItem {
	missing := []TitleItem{}
	for _, game := range s.Games {
		if game.Missing {
			missing = append(missing, game)
			if len(missing) == sagaMissingShown {
				break
			}
		}
	}
	return missing
}

// MoreMissing is how many missing games MissingGames leaves out.
func (s SagaProgress) MoreMissing() int {
	return s.Total - s.Owned - len(s.MissingGames())
}

// sagaDemo matches the demos the titles database does not flag, as "Super Mario Odyssey Demo
// for KIOSK" or "Puzzle Puppers Demo": a series lists games, not demos. Only the forms of a
// demo count, so "Trial of Mana" stays a game.
var sagaDemo = regexp.MustCompile(`(?i)\b(demo|trial)(\s+(version|edition))?\s*[)\]]?\s*$|\bdemo\s+(version|for\s+kiosk)\b|\bkiosk\b|体験版`)

// sagaMembers returns the games of a series, one of each name (the copy in the library first),
// the oldest first.
func (web *Web) sagaMembers(family string, lang string) []TitleItem {
	switchDB, localDB := web.state.get()
	if switchDB == nil || family == "" {
		return nil
	}
	index := web.sagas()
	wished := web.wishes().snapshot()
	items := []TitleItem{}
	for _, prefix := range index.membersOf[family] {
		title := switchDB.TitlesMap[prefix]
		// demos are no game of the series, whatever the setting of the library
		if title == nil || title.Attributes.Id == "" || isDemo(title, title.Attributes.Name) || sagaDemo.MatchString(title.Attributes.Name) || notAGame.MatchString(title.Attributes.Name) {
			continue
		}
		item := TitleItem{
			Id:           strings.ToUpper(title.Attributes.Id),
			Name:         titleName(switchDB, lang, title.Attributes.Id, title.Attributes.Name),
			OriginalName: title.Attributes.Name,
			ImageUrl:     coverUrl(localDB, title.Attributes),
			Region:       title.Attributes.Region,
			Missing:      true,
			Wished:       wished[strings.ToUpper(title.Attributes.Id)],
		}
		if localDB != nil {
			if local, ok := localDB.TitlesMap[prefix]; ok && local.BaseExist {
				item.Missing = false
				item.InLibrary = true
			}
		}
		item.ReleaseDate, _ = intToTime(title.Attributes.ReleaseDate)
		items = append(items, item)
	}
	items = withoutRegionalCopies(items, "")
	sort.Slice(items, func(i, j int) bool {
		if !items[i].ReleaseDate.Equal(items[j].ReleaseDate) {
			return items[i].ReleaseDate.Before(items[j].ReleaseDate)
		}
		return items[i].Name < items[j].Name
	})
	return items
}

// sagaFamilyOf returns the key of the series of a title, or "".
func (web *Web) sagaFamilyOf(titleId string, name string) string {
	if len(titleId) < 13 {
		return ""
	}
	index := web.sagas()
	if family, ok := index.familyOfId[strings.ToLower(titleId[:13])]; ok {
		return family
	}
	key := sagaKey(name)
	if key == "" {
		return ""
	}
	return index.family(key, "")
}

// sameSaga returns the series of a title: its other games, owned or not, the oldest first, and
// how many games of the series the library has, counting the title itself.
func (web *Web) sameSaga(titleId string, name string, lang string) SagaProgress {
	family := web.sagaFamilyOf(titleId, name)
	if family == "" {
		return SagaProgress{}
	}
	_, localDB := web.state.get()
	selfOwned := false
	if localDB != nil {
		if local, ok := localDB.TitlesMap[strings.ToLower(titleId[:13])]; ok && local.BaseExist {
			selfOwned = true
		}
	}
	self := regionalName(name)
	progress := SagaProgress{family: family, Total: 1}
	if selfOwned {
		progress.Owned = 1
	}
	for _, item := range web.sagaMembers(family, lang) {
		if len(item.Id) >= 13 && strings.EqualFold(item.Id[:13], titleId[:13]) || regionalName(item.Name) == self {
			continue
		}
		progress.Total++
		if !item.Missing {
			progress.Owned++
		}
		progress.Games = append(progress.Games, item)
	}
	if len(progress.Games) > maxSagaTitles {
		progress.Games = progress.Games[:maxSagaTitles]
	}
	return progress
}

// sagaName is the name of a series: the shortest name of its games without the subtitle, of
// the games named like the series ("Darkest Dungeon" rather than "Darkest Dungeon II").
func sagaName(family string, games []TitleItem) string {
	family, _ = splitSagaId(family)
	name, fallback := "", ""
	for _, game := range games {
		candidate := sagaTitle(game.Name)
		if candidate == "" {
			continue
		}
		if fallback == "" || len(candidate) < len(fallback) {
			fallback = candidate
		}
		if sagaKey(game.OriginalName) == family && (name == "" || len(candidate) < len(name)) {
			name = candidate
		}
	}
	if name == "" {
		name = fallback
	}
	if name == "" {
		return family
	}
	return name
}

// sagaTitle is a name without subtitle, marks, number and edition: "Darksiders III" is
// "Darksiders".
func sagaTitle(name string) string {
	name = strings.NewReplacer("™", "", "®", "", "©", "").Replace(name)
	words := strings.Fields(sagaCut.ReplaceAllString(name, ""))
	for len(words) > 1 {
		last := strings.ToLower(words[len(words)-1])
		if !sagaNumber.MatchString(last) && !sagaEdition[last] {
			break
		}
		words = words[:len(words)-1]
	}
	return strings.Join(words, " ")
}

// sagaProgress returns the series of which the library has a game and that have more than one,
// the ones closest to being complete first.
func (web *Web) sagaProgress(lang string) []SagaProgress {
	return web.derived("sagaProgress:"+lang, func() any {
		result := []SagaProgress{}
		switchDB, localDB := web.state.get()
		if switchDB == nil || localDB == nil {
			return result
		}
		index := web.sagas()
		families := map[string]bool{}
		for prefix, local := range localDB.TitlesMap {
			if !local.BaseExist {
				continue
			}
			if family, ok := index.familyOfId[prefix]; ok {
				families[family] = true
			}
		}
		for family := range families {
			games := web.sagaMembers(family, lang)
			if len(games) < 2 {
				continue
			}
			saga := newSagaProgress(family, games, index.known, lang)
			if saga.Owned == 0 {
				continue
			}
			words := []string{family}
			for _, game := range games {
				words = append(words, game.Name, game.OriginalName)
			}
			saga.searchKey = searchText(strings.Join(words, " "))
			saga.searchWords = strings.Fields(saga.searchKey)
			result = append(result, saga)
		}
		sort.Slice(result, func(i, j int) bool {
			a, b := result[i], result[j]
			if a.Total-a.Owned != b.Total-b.Owned {
				return a.Total-a.Owned < b.Total-b.Owned
			}
			if a.Owned != b.Owned {
				return a.Owned > b.Owned
			}
			return sortName(a.Name) < sortName(b.Name)
		})
		return result
	}).([]SagaProgress)
}

// SagaPageData is the page of a series, with all its games.
type SagaPageData struct {
	GlobalPageData
	Saga SagaProgress
}

// sagaPage returns a series with all its games, from the key of its page.
func (web *Web) sagaPage(id string, lang string) (SagaProgress, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(raw) == 0 {
		return SagaProgress{}, false
	}
	family := string(raw)
	games := web.sagaMembers(family, lang)
	if len(games) == 0 {
		return SagaProgress{}, false
	}
	return newSagaProgress(family, games, web.sagas().known, lang), true
}

// newSagaProgress is a series with its games: how many the library has, the cover of the
// first one it has, and its name, known or made from the names of its games.
func newSagaProgress(family string, games []TitleItem, known *knownSeries, lang string) SagaProgress {
	saga := SagaProgress{family: family, Total: len(games), Games: games}
	for _, game := range games {
		if !game.Missing {
			saga.Owned++
			if saga.ImageUrl == "" {
				saga.ImageUrl = game.ImageUrl
			}
		}
	}
	saga.Name = known.seriesLabel(family, lang)
	if saga.Name == "" {
		saga.Name = sagaName(family, games)
	}
	return saga
}

// SagasPageData is the page of the series.
type SagasPageData struct {
	GlobalPageData
	Sagas      []SagaProgress
	Filter     *TitleItemFilter
	Pagination pagination.Pagination
	// series to complete and complete ones, of the whole library
	Incomplete int
	Complete   int
}

func (web *Web) HandleSagas() {
	fsPatterns := []string{
		"resources/layout.html",
		"resources/partials/pagination.html",
		"resources/partials/components.html",
		"resources/pages/sagas.html",
	}

	sagaTemplates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/partials/card.html",
		"resources/partials/components.html", "resources/pages/saga.html")
	web.router.HandleFunc("/saga/{id}.html", func(w http.ResponseWriter, r *http.Request) {
		saga, ok := web.sagaPage(mux.Vars(r)["id"], web.requestLanguage(r))
		if !ok {
			http.NotFound(w, r)
			return
		}
		web.render(w, r, sagaTemplates, SagaPageData{GlobalPageData: web.globalPageData("sagas"), Saga: saga})
	}).Methods("GET")

	web.HandleFiltered("/sagas.html", func(filter *TitleItemFilter, lang string) any {
		data := SagasPageData{GlobalPageData: web.globalPageData("sagas"), Filter: filter}
		all := web.sagaProgress(lang)
		var query *searchQuery
		if filter.Keyword != "" {
			query = newSearchQuery(filter.Keyword)
		}
		shown := []SagaProgress{}
		for _, saga := range all {
			if saga.Complete() {
				data.Complete++
			} else {
				data.Incomplete++
			}
			if saga.Complete() != (filter.Status == STATUS_COMPLETE) {
				continue
			}
			if query != nil && !query.matches(saga.searchKey, saga.searchWords) {
				continue
			}
			shown = append(shown, saga)
		}
		data.Pagination = pagination.Calculate(filter.Page, sagasPerPage, len(shown))
		data.Sagas = shown[data.Pagination.Start:data.Pagination.End]
		return data
	}, web.embedFS, fsPatterns...)
}
