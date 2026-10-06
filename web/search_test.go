package web

import (
	"strings"
	"testing"
)

func TestForgivingSearch(t *testing.T) {
	name := searchText("0100000000010000 The Legend of Zelda™: Breath of the Wild")
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
		if got := searchMatches(name, words, searchText(query)); got != want {
			t.Errorf("%q: %v, want %v", query, got, want)
		}
	}
	pokemon := searchText("Pokémon Escarlata")
	if !searchMatches(pokemon, strings.Fields(pokemon), searchText("pokemon escarlta")) {
		t.Error("accents and a typo do not matter")
	}
	if searchMatches(pokemon, strings.Fields(pokemon), searchText("poke violeta")) {
		t.Error("a word that is not there is not found")
	}
}
