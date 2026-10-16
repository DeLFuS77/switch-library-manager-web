package web

import (
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// cjkScript matches the scripts of the Japanese, Chinese and Korean stores.
var cjkScript = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]`)

// fitsLanguage tells whether a name can be read in the interface language: a Japanese,
// Chinese or Korean name only in those languages.
func fitsLanguage(lang string, name string) bool {
	switch lang {
	case "ja", "ko", "zh":
		return true
	}
	return !cjkScript.MatchString(name)
}

// nacpLanguages are the languages of the names a game gives itself, for each interface
// language, the closest first.
var nacpLanguages = map[string][]string{
	"en": {"AmericanEnglish", "BritishEnglish"},
	"es": {"Spanish", "LatinAmericanSpanish"},
	"fr": {"French", "CanadianFrench"},
	"de": {"German"},
	"it": {"Italian"},
	"pt": {"Portuguese", "BrazilianPortuguese"},
	"nl": {"Dutch"},
	"ru": {"Russian"},
}

// nacpName is the name a game gives itself in the interface language, else in English, if
// it can be read in that language.
func nacpName(nacp *switchfs.Nacp, lang string) string {
	if nacp == nil {
		return ""
	}
	for _, language := range append(append([]string{}, nacpLanguages[lang]...), nacpLanguages["en"]...) {
		if name := strings.TrimSpace(nacp.TitleName[language].Title); usableName(name) && fitsLanguage(lang, name) {
			return name
		}
	}
	return ""
}

// fileNacp gives the names a game of the library gives itself, by its title ID; set by the
// app when it starts.
var fileNacp atomic.Pointer[func(titleId string) *switchfs.Nacp]

// libraryNacp is the NACP of each game of the library, by title ID in upper case.
func (web *Web) libraryNacp(titleId string) *switchfs.Nacp {
	index := web.derived("libraryNacp", func() any {
		_, localDB := web.state.get()
		names := map[string]*switchfs.Nacp{}
		if localDB == nil {
			return names
		}
		for _, local := range localDB.TitlesMap {
			addNacp(names, local.File)
			for _, update := range local.Updates {
				// an update names the game too, often in more languages than an old base
				if update.Metadata != nil && update.Metadata.Ncap != nil {
					if prefix, err := db.TitleIDPrefix(update.Metadata.TitleId); err == nil {
						id := strings.ToUpper(prefix) + "000"
						if names[id] == nil {
							names[id] = update.Metadata.Ncap
						}
					}
				}
			}
		}
		return names
	}).(map[string]*switchfs.Nacp)
	return index[strings.ToUpper(titleId)]
}

func addNacp(names map[string]*switchfs.Nacp, file db.SwitchFileInfo) {
	if file.Metadata != nil && file.Metadata.Ncap != nil {
		names[strings.ToUpper(file.Metadata.TitleId)] = file.Metadata.Ncap
	}
}
