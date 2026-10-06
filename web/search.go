package web

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Searching forgives the way a name is written: accents, case, signs and the order of the
// words do not matter, and small typing mistakes are accepted ("zelda brth" finds "The
// Legend of Zelda: Breath of the Wild", "pokemon escarlta" finds "Pokémon Escarlata").

// letters with accents and their plain letter
var plainLetters = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ä': 'a', 'ã': 'a', 'å': 'a', 'ā': 'a',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e', 'ē': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i', 'ī': 'i',
	'ó': 'o', 'ò': 'o', 'ô': 'o', 'ö': 'o', 'õ': 'o', 'ø': 'o', 'ō': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u', 'ū': 'u',
	'ñ': 'n', 'ç': 'c', 'ß': 's', 'ý': 'y', 'ÿ': 'y',
}

// searchText returns a text in lower case without accents, with the signs as spaces.
func searchText(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	space := false
	for _, r := range strings.ToLower(text) {
		if plain, ok := plainLetters[r]; ok {
			r = plain
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			space = false
		} else if !space {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}

// searchQuery is a search prepared once for every item of a list.
type searchQuery struct {
	text   string
	tokens []searchToken
}

type searchToken struct {
	text  string
	runes []rune
	typos int
}

func newSearchQuery(keyword string) *searchQuery {
	query := &searchQuery{text: searchText(keyword)}
	for _, token := range strings.Fields(query.text) {
		runes := []rune(token)
		query.tokens = append(query.tokens, searchToken{text: token, runes: runes, typos: allowedTypos(len(runes))})
	}
	return query
}

// matches reports whether every word of the query is in the text (see searchText), or
// close to one of its words.
func (q *searchQuery) matches(text string, words []string) bool {
	if q.text == "" || strings.Contains(text, q.text) {
		return true
	}
	for i := range q.tokens {
		token := &q.tokens[i]
		if strings.Contains(text, token.text) {
			continue
		}
		if !closeToAWord(token, words) {
			return false
		}
	}
	return true
}

// searchMatches is matches for a query used once.
func searchMatches(text string, words []string, query string) bool {
	return newSearchQuery(query).matches(text, words)
}

// typos allowed in a word of the query: none for short words
func allowedTypos(length int) int {
	switch {
	case length < 4:
		return 0
	case length < 8:
		return 1
	}
	return 2
}

// closeToAWord reports whether a word of the query is a word of the text, or its beginning,
// with a few typing mistakes, or the word with letters left out.
func closeToAWord(token *searchToken, words []string) bool {
	if token.typos == 0 {
		return false
	}
	var buffer [64]rune
	for _, word := range words {
		if word == "" || len(word) > len(buffer) {
			continue
		}
		candidate := toRunes(word, buffer[:0])
		if lettersInOrder(token.runes, candidate) {
			return true
		}
		// the beginning of a longer word: "escarl" is the beginning of "escarlata"
		if len(candidate) > len(token.runes)+token.typos {
			candidate = candidate[:len(token.runes)+token.typos]
		}
		if len(candidate)+token.typos < len(token.runes) {
			continue
		}
		if prefixDistance(token.runes, candidate) <= token.typos {
			return true
		}
	}
	return false
}

// toRunes decodes a word into buffer without allocating.
func toRunes(word string, buffer []rune) []rune {
	for len(word) > 0 {
		r, size := utf8.DecodeRuneInString(word)
		buffer = append(buffer, r)
		word = word[size:]
	}
	return buffer
}

// prefixDistance is the number of letters to add, remove or change to make the query the
// beginning of the word (an edit distance that does not count the rest of the word).
func prefixDistance(query []rune, word []rune) int {
	var rows [2][65]int
	if len(word) >= len(rows[0]) {
		word = word[:len(rows[0])-1]
	}
	previous, current := rows[0][:len(word)+1], rows[1][:len(word)+1]
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(query); i++ {
		current[0] = i
		for j := 1; j <= len(word); j++ {
			cost := 1
			if query[i-1] == word[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	// the query is matched entirely, the word only up to any point
	best := previous[0]
	for _, value := range previous {
		best = min(best, value)
	}
	return best
}

// lettersInOrder reports whether the letters of the query are in the word, in order and
// from its first letter, as when letters are left out: "brth" for "breath".
func lettersInOrder(query []rune, word []rune) bool {
	if len(query) < 3 || len(word) == 0 || query[0] != word[0] || len(word)*2 > len(query)*3+2 {
		return false
	}
	next := 0
	for _, r := range word {
		if next < len(query) && r == query[next] {
			next++
		}
	}
	return next == len(query)
}
