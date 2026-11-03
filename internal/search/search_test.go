package search

import (
	"slices"
	"strings"
	"testing"
)

func TestForgivingSearch(t *testing.T) {
	name := Text("0100000000010000 The Legend of Zelda™: Breath of the Wild")
	words := strings.Fields(name)
	for query, want := range map[string]bool{
		"zelda":          true,
		"ZELDA breath":   true,
		"wild zelda":     true,
		"zelda brth":     true, // one letter missing
		"zeldaa":         true,
		"legnd":          true,
		"breath of fire": false,
		"mario":          false,
		"0100000000010":  true,
		"xyz":            false,
	} {
		if got := Matches(name, words, Text(query)); got != want {
			t.Errorf("%q: %v, want %v", query, got, want)
		}
	}
	pokemon := Text("Pokémon Escarlata")
	if !Matches(pokemon, strings.Fields(pokemon), Text("pokemon escarlta")) {
		t.Error("accents and a typo do not matter")
	}
	if Matches(pokemon, strings.Fields(pokemon), Text("poke violeta")) {
		t.Error("a word that is not there is not found")
	}
}

// Typing mistakes are forgiven only when no name of the list has the words as they are typed:
// "mario" finds the Mario games, not Marimo nor Bone Marrow.
func TestTyposOnlyWhenNothingIsWritten(t *testing.T) {
	names := []string{"Super Mario Odyssey", "Bone Marrow", "Marimo", "Mari & Bayu", "Mario Kart 8 Deluxe",
		"The Legend of Zelda: Breath of the Wild", "Pokémon Escarlata"}
	texts := make([]string, len(names))
	for i, name := range names {
		texts[i] = Text(name)
	}
	found := func(keyword string) []string {
		query := NewQuery(keyword)
		query.Narrow(len(texts), func(i int) string { return texts[i] })
		result := []string{}
		for i, text := range texts {
			if query.Matches(text, strings.Fields(text)) {
				result = append(result, names[i])
			}
		}
		return result
	}
	for keyword, want := range map[string][]string{
		"mario":            {"Super Mario Odyssey", "Mario Kart 8 Deluxe"},
		"MARIO kart":       {"Mario Kart 8 Deluxe"},
		"zelda brth":       {"The Legend of Zelda: Breath of the Wild"},
		"pokemon escarlta": {"Pokémon Escarlata"},
		"":                 names,
	} {
		if got := found(keyword); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", keyword, got, want)
		}
	}
	// nothing written so: the names close to the words are found
	if got := found("marioo"); !slices.Contains(got, "Super Mario Odyssey") || !slices.Contains(got, "Mario Kart 8 Deluxe") {
		t.Errorf("a typing mistake is still forgiven: %v", got)
	}

	// a query looks at each list anew
	query := NewQuery("mario")
	query.Narrow(len(texts), func(i int) string { return texts[i] })
	marimo := Text("Marimo")
	query.Narrow(1, func(int) string { return marimo })
	if !query.Matches(marimo, strings.Fields(marimo)) {
		t.Error("a list without Mario games forgives the typing mistake")
	}
}
