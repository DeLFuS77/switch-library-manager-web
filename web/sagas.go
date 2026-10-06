package web

import (
	"sort"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/settings"
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

// sagaMembers returns the games of a series, one of each name (the copy in the library first),
// the oldest first.
func (web *Web) sagaMembers(family string, lang string) []TitleItem {
	switchDB, localDB := web.state.get()
	if switchDB == nil || family == "" {
		return nil
	}
	index := web.sagas()
	hideDemos := settings.ReadSettings(web.dataFolder).HideDemoGames
	items := []TitleItem{}
	for i := sort.SearchStrings(index.keys, family); i < len(index.keys); i++ {
		k := index.keys[i]
		if k != family && !strings.HasPrefix(k, family+" ") {
			break
		}
		for _, prefix := range index.byKey[k] {
			title := switchDB.TitlesMap[prefix]
			if title == nil || title.Attributes.Id == "" || (hideDemos && isDemo(title, title.Attributes.Name)) || notAGame.MatchString(title.Attributes.Name) {
				continue
			}
			item := TitleItem{
				Id:           strings.ToUpper(title.Attributes.Id),
				Name:         titleName(switchDB, lang, title.Attributes.Id, title.Attributes.Name),
				OriginalName: title.Attributes.Name,
				ImageUrl:     coverUrl(localDB, title.Attributes),
				Region:       title.Attributes.Region,
				Missing:      true,
			}
			if localDB != nil {
				if local, ok := localDB.TitlesMap[prefix]; ok && local.BaseExist {
					item.Missing = false
				}
			}
			item.ReleaseDate, _ = intToTime(title.Attributes.ReleaseDate)
			items = append(items, item)
		}
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
	key, ok := index.keyOfId[strings.ToLower(titleId[:13])]
	if !ok {
		key = sagaKey(name)
	}
	if key == "" {
		return ""
	}
	return index.family(key)
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
	progress := SagaProgress{Total: 1}
	if selfOwned {
		progress.Owned = 1
	}
	for _, item := range web.sagaMembers(family, lang) {
		if strings.EqualFold(item.Id[:13], titleId[:13]) || regionalName(item.Name) == self {
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
			if key, ok := index.keyOfId[prefix]; ok {
				families[index.family(key)] = true
			}
		}
		for family := range families {
			games := web.sagaMembers(family, lang)
			if len(games) < 2 {
				continue
			}
			saga := SagaProgress{Total: len(games), Games: games}
			words := []string{family}
			for _, game := range games {
				words = append(words, game.Name, game.OriginalName)
				if !game.Missing {
					saga.Owned++
					if saga.ImageUrl == "" {
						saga.ImageUrl = game.ImageUrl
					}
				}
			}
			if saga.Owned == 0 {
				continue
			}
			saga.Name = sagaName(family, games)
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
