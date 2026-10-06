package web

import (
	"math"
	"sort"
	"strings"
	"time"
)

// "You might like": games that are not in the library and look like the favorites, by their
// genres and series. Without favorites, the library tells what the user likes.

const (
	maxRecommendations = 12
	// games of the same series among the recommendations
	maxRecommendationsPerSaga = 2
)

// Recommendation is a missing game and why it is recommended: a game of the same series, or the
// genre it shares with the games the user likes.
type Recommendation struct {
	Item  TitleItem
	Saga  string
	Genre string
}

// tasteProfile is what the user likes: the share of each genre, and the series of the favorites
// (strong) and of the library (weak), with the name of a game of each.
type tasteProfile struct {
	genres        map[string]float64
	favoriteSagas map[string]string
	ownedSagas    map[string]string
	// the names of the games of the library (see regionalName)
	ownedNames map[string]bool
}

func (web *Web) tasteProfile(lang string) tasteProfile {
	profile := tasteProfile{genres: map[string]float64{}, favoriteSagas: map[string]string{}, ownedSagas: map[string]string{}, ownedNames: map[string]bool{}}
	switchDB, localDB := web.state.get()
	if switchDB == nil || localDB == nil {
		return profile
	}
	index := web.sagas()
	favorites := web.favorites().snapshot()
	count := func(genres []string, counts map[string]float64) {
		for _, genre := range genres {
			counts[genre]++
		}
	}
	libraryGenres := map[string]float64{}
	favoriteGenres := map[string]float64{}
	for prefix, local := range localDB.TitlesMap {
		title := switchDB.TitlesMap[prefix]
		if !local.BaseExist || title == nil {
			continue
		}
		id := strings.ToUpper(title.Attributes.Id)
		family := ""
		if key, ok := index.keyOfId[prefix]; ok {
			family = index.family(key)
		}
		name := titleName(switchDB, lang, title.Attributes.Id, title.Attributes.Name)
		profile.ownedNames[regionalName(title.Attributes.Name)] = true
		count(title.Attributes.Genres, libraryGenres)
		if favorites[id] {
			count(title.Attributes.Genres, favoriteGenres)
			if family != "" {
				profile.favoriteSagas[family] = name
			}
		}
		if family != "" {
			profile.ownedSagas[family] = name
		}
	}
	genres := libraryGenres
	if len(favoriteGenres) > 0 {
		genres = favoriteGenres
	}
	total := 0.0
	for _, n := range genres {
		total += n
	}
	for genre, n := range genres {
		profile.genres[genre] = n / total
	}
	return profile
}

// owns reports whether the library has a game of this name, in another store, or the game
// that a name like "Super Mario Maker 2 - Media Review" belongs to.
func (profile tasteProfile) owns(name string) bool {
	name = regionalName(name)
	if profile.ownedNames[name] {
		return true
	}
	for i := range name {
		if (strings.HasPrefix(name[i:], " - ") || strings.HasPrefix(name[i:], ": ")) && profile.ownedNames[strings.TrimSpace(name[:i])] {
			return true
		}
	}
	return false
}

// recommendations returns the missing games that look most like what the user likes.
func (web *Web) recommendations(lang string) []Recommendation {
	return web.derived("recommendations:"+lang, func() any {
		return web.recommendationsAt(lang, time.Now())
	}).([]Recommendation)
}

func (web *Web) recommendationsAt(lang string, now time.Time) []Recommendation {
	result := []Recommendation{}
	profile := web.tasteProfile(lang)
	if len(profile.genres) == 0 && len(profile.favoriteSagas) == 0 {
		return result
	}
	index := web.sagas()
	type candidate struct {
		Recommendation
		family string
		score  float64
	}
	candidates := []candidate{}
	for _, item := range web.buildMissingGames(lang) {
		if item.Wished || item.Demo || notAGame.MatchString(item.OriginalName) || item.ImageUrl == "" || item.ReleaseDate.IsZero() || item.ReleaseDate.After(now) {
			continue
		}
		if profile.owns(item.OriginalName) {
			continue
		}
		c := candidate{Recommendation: Recommendation{Item: item}}
		if key, ok := index.keyOfId[strings.ToLower(item.Id[:13])]; ok {
			c.family = index.family(key)
		}
		// a game that shares more of the genres the user likes scores more
		best := 0.0
		for _, genre := range item.Genres {
			share := profile.genres[genre]
			c.score += share
			if share > best {
				best, c.Genre = share, genre
			}
		}
		if name, ok := profile.favoriteSagas[c.family]; ok && c.family != "" {
			c.score += 1
			c.Saga = name
		} else if name, ok := profile.ownedSagas[c.family]; ok && c.family != "" {
			c.score += .3
			c.Saga = name
		}
		if c.score == 0 {
			continue
		}
		// newer games first among the similar ones
		years := now.Sub(item.ReleaseDate).Hours() / 24 / 365
		c.score += .05 * math.Max(0, 1-years/8)
		// bigger games are translated to more languages
		c.score += .05 * math.Min(float64(len(item.Languages)), 10) / 10
		// the copy named without a translation first
		if strings.ContainsAny(item.Name, "（(") {
			c.score -= .001
		}
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].Item.Id < candidates[j].Item.Id
	})
	seen := map[string]bool{}
	perSaga := map[string]int{}
	for _, c := range candidates {
		name := regionalName(c.Item.OriginalName)
		if seen[name] || (c.family != "" && perSaga[c.family] >= maxRecommendationsPerSaga) {
			continue
		}
		seen[name] = true
		perSaga[c.family]++
		result = append(result, c.Recommendation)
		if len(result) == maxRecommendations {
			break
		}
	}
	return result
}
