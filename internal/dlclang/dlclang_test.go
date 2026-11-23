package dlclang

import "testing"

// The names of language packs as the titles database has them (#200).
func TestLanguage(t *testing.T) {
	cases := map[string]string{
		"EA SPORTS FC 26 In-Game Commentary - Spanish":                        "es",
		"EA SPORTS FC 26 Latin American Spanish - In-Game Commentary":         "es-419",
		"EA SPORTS FC 26 In-Game Commentary - Brazilian Portuguese":           "pt-br",
		"FIFA 22 In-Game Commentary – Portuguese (Brazil)":                    "pt-br",
		"EA SPORTS FC 26 Simplified Chinese - In-Game Commentary":             "zh-hans",
		"EA SPORTS FC 25 In-Game Commentary - German":                         "de",
		"Hogwarts Legacy: French Language Pack":                               "fr",
		"Castilian Spanish Language Pack":                                     "es",
		"(ESMX) Thronebreaker: The Witcher Tales Language Pack":               "es-419",
		"Traditional Chinese Language Pack":                                   "zh-hant",
		"The Witcher 3: Wild Hunt - Complete Edition Language Pack (RU)":      "ru",
		"(PTBR) Thronebreaker: The Witcher Tales Language Pack":               "pt-br",
		"(ZHS) Thronebreaker: The Witcher Tales Language Pack":                "zh-hans",
		"(KR) The Witcher 3: Wild Hunt Language Pack":                         "ko",
		"Biomutant Chinese Simplified Language DLC":                           "zh-hans",
		"METAL GEAR SOLID - Master Collection Version Japanese Language Pack": "ja",
		"The Prince of Tennis Doki Doki Survival - Language Pack":             "",
	}
	for name, want := range cases {
		got, pack := Language(name)
		if !pack || got != want {
			t.Errorf("%s: %q %v, want %q", name, got, pack, want)
		}
	}
	for _, name := range []string{"Borderlands 3: Additional Voiceover Languages", "The Language Of Love", "Hogwarts Legacy: Onyx Hippogriff Mount", "Spin & Match Puzzle Learn at Once 3 Languages"} {
		if _, pack := Language(name); pack {
			t.Errorf("%s is not a language pack", name)
		}
	}
}

func TestHidden(t *testing.T) {
	chosen := Chosen([]string{"ES", "en", "xx"})
	if len(chosen) != 2 {
		t.Fatalf("chosen %v", chosen)
	}
	if !Hidden("Hogwarts Legacy: German Language Pack", chosen) || Hidden("Hogwarts Legacy: Spanish Language Pack", chosen) {
		t.Error("only the packs of other languages are hidden")
	}
	if Hidden("The Prince of Tennis Doki Doki Survival - Language Pack", chosen) || Hidden("Hogwarts Legacy: Onyx Hippogriff Mount", chosen) {
		t.Error("a pack of no known language and a DLC that is not a pack are shown")
	}
	if Hidden("Hogwarts Legacy: German Language Pack", nil) {
		t.Error("with no language chosen everything is shown")
	}
}
