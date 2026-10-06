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
