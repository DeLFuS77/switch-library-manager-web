package web

import (
	"regexp"
	"sort"
	"strings"
)

// The titles database does not know which games belong to the same series, so they are
// found by their names: "Darkest Dungeon II" and "Darkest Dungeon®" share "darkest dungeon",
// and "Darksiders Genesis" belongs to "darksiders" because a game is named like that.

const maxSagaTitles = 24

var (
	// subtitles and editions start with these
	sagaCut = regexp.MustCompile(`\s*(:|\s[-–—]\s|\(|\[|~).*$`)
	// numbers of the games of a series: 2, 3D, II, V3...
	sagaNumber  = regexp.MustCompile(`^(v?\d+d?|[ivx]+|[a-z])$`)
	sagaEdition = map[string]bool{"edition": true, "deluxe": true, "definitive": true, "deathinitive": true, "remastered": true,
		"remaster": true, "remake": true, "hd": true, "complete": true, "ultimate": true, "collection": true, "anniversary": true,
		"goty": true, "dx": true, "plus": true, "enhanced": true, "special": true, "gold": true, "premium": true, "bundle": true,
		"switch": true, "nintendo": true, "demo": true, "trial": true}
	// a single word like these is no series
	sagaGeneric = map[string]bool{"the": true, "super": true, "new": true, "my": true, "dark": true, "little": true, "pro": true,
		"mega": true, "ultra": true, "star": true, "space": true, "grand": true, "real": true, "just": true, "game": true,
		"world": true, "puzzle": true, "pixel": true, "red": true, "black": true, "big": true, "mini": true, "tiny": true}
)

// sagaKey returns the name of a game without its number, edition and subtitle, in lower case.
func sagaKey(name string) string {
	name = strings.ToLower(name)
	name = sagaCut.ReplaceAllString(name, "")
	words := strings.FieldsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 && r != '™' && r != '®' && r != '©')
	})
	if len(words) > 0 && words[0] == "the" {
		words = words[1:]
	}
	for len(words) > 1 && (sagaNumber.MatchString(words[len(words)-1]) || sagaEdition[words[len(words)-1]]) {
		words = words[:len(words)-1]
	}
	key := strings.Join(words, " ")
	if len(key) < 3 {
		return ""
	}
	return key
}

// sagaIndex is the games of the titles database by series key, with the keys sorted.
type sagaIndex struct {
	keys    []string
	byKey   map[string][]string
	keyOfId map[string]string
}

func (web *Web) sagas() *sagaIndex {
	return web.derived("sagas", func() any {
		index := &sagaIndex{byKey: map[string][]string{}, keyOfId: map[string]string{}}
		switchDB, _ := web.state.get()
		if switchDB == nil {
			return index
		}
		for prefix, title := range switchDB.TitlesMap {
			key := sagaKey(title.Attributes.Name)
			if key == "" {
				continue
			}
			if _, ok := index.byKey[key]; !ok {
				index.keys = append(index.keys, key)
			}
			index.byKey[key] = append(index.byKey[key], prefix)
			index.keyOfId[prefix] = key
		}
		sort.Strings(index.keys)
		return index
	}).(*sagaIndex)
}

// family returns the key of the series of a key: its shortest beginning that is the key of
// a game too, so "darksiders genesis" belongs to "darksiders".
func (index *sagaIndex) family(key string) string {
	words := strings.Fields(key)
	for n := 1; n < len(words); n++ {
		prefix := strings.Join(words[:n], " ")
		if n == 1 && (sagaGeneric[prefix] || len(prefix) < 4) {
			continue
		}
		if _, ok := index.byKey[prefix]; ok {
			return prefix
		}
	}
	return key
}

// regionalName is a name without the translation some stores add in brackets, e.g.
// "Darksiders Genesis（ダークサイダーズ ジェネシス）", in lower case without marks and signs.
func regionalName(name string) string {
	if i := strings.IndexAny(name, "（(["); i > 0 {
		name = name[:i]
	}
	return searchText(name)
}

// notAGame matches the trials, network tests and apps of a game that the titles database does
// not flag as demos; they are left out of the series and the recommendations.
var notAGame = regexp.MustCompile(`(?i)\bnetwork test\b|\btest ver(sion)?\b|\b(open|closed) beta\b|\bprototype (orders|missions)\b|misiones prototipo|\bmedia review\b`)

// withoutRegionalCopies keeps one game of each name: the same game sold in several stores is
// shown once, the copy in the library first; the game of the page itself is left out.
func withoutRegionalCopies(items []TitleItem, self string) []TitleItem {
	// the copy in the library first, then a copy named without a translation
	translated := func(item TitleItem) bool { return strings.ContainsAny(item.Name, "（(") }
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Missing != items[j].Missing {
			return !items[i].Missing
		}
		return !translated(items[i]) && translated(items[j])
	})
	seen := map[string]bool{regionalName(self): true}
	result := items[:0]
	for _, item := range items {
		key := regionalName(item.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, item)
	}
	return result
}
