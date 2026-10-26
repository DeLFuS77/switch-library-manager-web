package web

import (
	"sort"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// gameLanguageNames are the languages of the games of the titles database, by code, in
// English; the interface translates them.
var gameLanguageNames = map[string]string{
	"de": "German", "en": "English", "es": "Spanish", "fr": "French", "it": "Italian", "ja": "Japanese", "ko": "Korean",
	"nl": "Dutch", "pl": "Polish", "pt": "Portuguese", "ru": "Russian", "th": "Thai", "zh": "Chinese",
}

// gameLanguageName returns the English name of a language code, or the code.
func gameLanguageName(code string) string {
	if name, ok := gameLanguageNames[code]; ok {
		return name
	}
	return strings.ToUpper(code)
}

// languageFlagCountry is the flag of each language of a game: the country of the language, as
// round flags apart from the flags of the regions. English is the United Kingdom, Chinese China.
var languageFlagCountry = map[string]string{
	"en": "gb", "es": "es", "fr": "fr", "de": "de", "it": "it", "pt": "pt", "nl": "nl",
	"ru": "ru", "ja": "jp", "ko": "kr", "zh": "cn", "pl": "pl", "th": "th",
}

// cardLanguages is how many language flags a card shows: the others are counted
const cardLanguages = 3

// LanguageFlag is a language of a game and its flag.
type LanguageFlag struct {
	Code string
	Flag string
}

// LanguageFlags are the languages of a game for its card: the language of the interface first,
// a few shown and the others on hover.
type LanguageFlags struct {
	Shown  []LanguageFlag
	Hidden []LanguageFlag
}

func languageFlags(codes []string, lang string) LanguageFlags {
	all := []LanguageFlag{}
	for _, code := range codes {
		if flag, ok := languageFlagCountry[code]; ok {
			language := LanguageFlag{Code: code, Flag: flag}
			if code == lang {
				all = append([]LanguageFlag{language}, all...)
			} else {
				all = append(all, language)
			}
		}
	}
	if len(all) <= cardLanguages+1 {
		return LanguageFlags{Shown: all}
	}
	return LanguageFlags{Shown: all[:cardLanguages], Hidden: all[cardLanguages:]}
}

// showRegion tells whether the flags of the region of a card say something: most games have one
// title ID for every store, which the titles database gives the United States, so that flag
// alone is left out.
func showRegion(item TitleItem) bool {
	return item.Region != "" && (regionCode(item.Region) != "US" || len(item.OtherFlags) > 0 || len(item.MissingFlags) > 0 || len(item.HiddenRegions) > 0)
}

var knownGenreSet = func() map[string]bool {
	set := map[string]bool{}
	for _, genre := range db.Genres {
		set[genre] = true
	}
	return set
}()

// NamedCount is a value of a filter and the number of games with it.
type NamedCount struct {
	Name  string
	Count int
}

// sortedCounts returns the counts, the most frequent first.
func sortedCounts(counts map[string]int) []NamedCount {
	result := make([]NamedCount, 0, len(counts))
	for name, count := range counts {
		result = append(result, NamedCount{Name: name, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Name < result[j].Name
	})
	return result
}

// hasValue reports whether a list contains a value.
func hasValue(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// playersMatch reports whether a game fits the players filter: "1" only one player, "2"
// and "4" at least that many.
func playersMatch(players int, filter string) bool {
	switch filter {
	case "1":
		return players == 1
	case "2":
		return players >= 2
	case "4":
		return players >= 4
	}
	return true
}
