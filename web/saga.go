package web

import (
	"regexp"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"sort"
	"strings"
)

// The titles database does not know which games belong to the same series, so they are
// found by their names and their publisher: "Darkest Dungeon II" and "Darkest Dungeon®" share
// "darkest dungeon", and "Darksiders Genesis" belongs to "darksiders" because a game of the
// same publisher is named like that. Names alone are not enough: "Kingdom Two Crowns",
// "Kingdom Hearts" and "Kingdom Rush" share a word, not a series, and each has its publisher.

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
		"world": true, "puzzle": true, "pixel": true, "red": true, "black": true, "big": true, "mini": true, "tiny": true,
		"nintendo": true}
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
	// the publisher of every game, made comparable, and the series it belongs to
	publisherOf map[string]string
	familyOfId  map[string]string
	// the keys of the games of every publisher, sorted
	keysOf map[string][]string
	// how many keys begin with some words, in all and by publisher: "sonic" is a word of
	// Sega, "escape" begins the names of games of hundreds of publishers
	beginTotal map[string]int
	beginBy    map[string]int
	// the games of every series, and the series known to IGDB or Wikidata with their names
	membersOf map[string][]string
	known     *knownSeries
}

// publisherSuffixes are the words of a company form, left out to compare publishers.
var publisherSuffixes = map[string]bool{"inc": true, "ltd": true, "llc": true, "co": true, "corp": true, "corporation": true,
	"limited": true, "gmbh": true, "sa": true, "srl": true, "sl": true, "kk": true, "bv": true, "ab": true, "oy": true,
	"pty": true, "plc": true, "the": true, "ag": true, "sas": true, "spa": true}

// publisherKey is a publisher name made comparable: "Raw Fury AB" and "Raw Fury" are one.
func publisherKey(name string) string {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	})
	kept := words[:0]
	for _, word := range words {
		if !publisherSuffixes[word] {
			kept = append(kept, word)
		}
	}
	return strings.Join(kept, " ")
}

// sagaId is the identity of a series: its key and its publisher.
func sagaId(key string, publisher string) string {
	return key + "\x00" + publisher
}

// splitSagaId returns the key and the publisher of a series.
func splitSagaId(id string) (string, string) {
	key, publisher, _ := strings.Cut(id, "\x00")
	return key, publisher
}

func (web *Web) sagas() *sagaIndex {
	return web.derived("sagas", func() any {
		index := &sagaIndex{byKey: map[string][]string{}, keyOfId: map[string]string{}, publisherOf: map[string]string{}, familyOfId: map[string]string{}}
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
			index.publisherOf[prefix] = publisherKey(title.Attributes.Publisher)
		}
		sort.Strings(index.keys)
		index.keysOf = map[string][]string{}
		index.beginTotal, index.beginBy = map[string]int{}, map[string]int{}
		for _, key := range index.keys {
			seen := map[string]bool{}
			for _, id := range index.byKey[key] {
				if publisher := index.publisherOf[id]; publisher != "" && !seen[publisher] {
					seen[publisher] = true
					index.keysOf[publisher] = append(index.keysOf[publisher], key)
				}
			}
			words := strings.Fields(key)
			for n := 1; n < len(words); n++ {
				begin := strings.Join(words[:n], " ")
				index.beginTotal[begin]++
				for publisher := range seen {
					index.beginBy[sagaId(begin, publisher)]++
				}
			}
		}
		for prefix, key := range index.keyOfId {
			index.familyOfId[prefix] = index.family(key, index.publisherOf[prefix])
		}
		index.applyKnownSeries(web.buildKnownSeries(switchDB, index.byRegionalName(switchDB)))
		index.membersOf = map[string][]string{}
		for prefix, family := range index.familyOfId {
			index.membersOf[family] = append(index.membersOf[family], prefix)
		}
		return index
	}).(*sagaIndex)
}

// byRegionalName lists the groups of title ID of the games by regionalName.
func (index *sagaIndex) byRegionalName(switchDB *db.SwitchTitlesDB) map[string][]string {
	result := map[string][]string{}
	for prefix := range index.keyOfId {
		if title := switchDB.TitlesMap[prefix]; title != nil {
			name := regionalName(title.Attributes.Name)
			result[name] = append(result[name], prefix)
		}
	}
	return result
}

// applyKnownSeries puts the games known to IGDB or Wikidata in their series. A game without
// data joins the known series of the games of its guessed series, when they all agree on
// it, and the guessed series has a name of two words or more ("kingdom hearts"), or their
// names agree (one the beginning of the other): an edition, a demo or the copy of another
// store is in the series of its game, while two games that only share a first word
// ("Collection of Mana", "Collection of SaGa") stay apart.
func (index *sagaIndex) applyKnownSeries(known *knownSeries) {
	index.known = known
	// the known games of every guessed series, when they all agree on one known series
	agreed := map[string]string{}
	knownKeys := map[string][]string{}
	for prefix, family := range known.familyOf {
		guessed, ok := index.familyOfId[prefix]
		if !ok {
			continue
		}
		if previous, seen := agreed[guessed]; !seen {
			agreed[guessed] = family
		} else if previous != family {
			agreed[guessed] = ""
		}
		knownKeys[guessed] = append(knownKeys[guessed], index.keyOfId[prefix])
	}
	related := func(a, b string) bool {
		return a == b || strings.HasPrefix(a, b+" ") || strings.HasPrefix(b, a+" ")
	}
	for prefix, guessed := range index.familyOfId {
		if family, ok := known.familyOf[prefix]; ok {
			index.familyOfId[prefix] = family
			continue
		}
		family := agreed[guessed]
		if family == "" {
			continue
		}
		// a guessed series of two words or more ("kingdom hearts") is precise enough
		if guessedKey, _ := splitSagaId(guessed); strings.Contains(guessedKey, " ") {
			index.familyOfId[prefix] = family
			continue
		}
		for _, key := range knownKeys[guessed] {
			if related(index.keyOfId[prefix], key) {
				index.familyOfId[prefix] = family
				break
			}
		}
	}
	// games whose name is too short to guess a series, known all the same
	for prefix, family := range known.familyOf {
		if _, ok := index.familyOfId[prefix]; !ok {
			index.familyOfId[prefix] = family
		}
	}
}

// family returns the series of a game from its key and publisher: the shortest beginning of
// the key that is the key of a game of the same publisher ("darksiders genesis" belongs to
// "darksiders"), or that another game of the publisher starts with too ("sonic forces" and
// "sonic frontiers" are "sonic"). Without a publisher, only games of the very same key are a
// series.
func (index *sagaIndex) family(key string, publisher string) string {
	if publisher == "" {
		return sagaId(key, "")
	}
	words := strings.Fields(key)
	for n := 1; n < len(words); n++ {
		prefix := strings.Join(words[:n], " ")
		if n == 1 && (sagaGeneric[prefix] || len(prefix) < 4) {
			continue
		}
		for _, id := range index.byKey[prefix] {
			if index.publisherOf[id] == publisher {
				return sagaId(prefix, publisher)
			}
		}
		// a beginning shared with another game of the publisher, when it is a word of that
		// publisher: at least half of the names that begin so are its games
		if index.beginBy[sagaId(prefix, publisher)]*2 < index.beginTotal[prefix] {
			continue
		}
		keys := index.keysOf[publisher]
		for i := sort.SearchStrings(keys, prefix); i < len(keys) && strings.HasPrefix(keys[i], prefix+" "); i++ {
			if keys[i] != key {
				return sagaId(prefix, publisher)
			}
		}
	}
	return sagaId(key, publisher)
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
