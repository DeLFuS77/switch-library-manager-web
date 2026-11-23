package web

import (
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/internal/dlclang"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The language packs and the commentary of a game in the languages not chosen in Settings do not
// count as missing DLC, as if they were ignored (#200).

// languageHiddenDLC are the DLC, by their title ID in lower case, that are a language pack in a
// language not chosen; none while no language is chosen.
func (web *Web) languageHiddenDLC() map[string]struct{} {
	return web.derived("languageHiddenDLC", func() any {
		hidden := map[string]struct{}{}
		switchDB, _ := web.state.get()
		chosen := dlclang.Chosen(settings.ReadSettings(web.dataFolder).DLCLanguages)
		if switchDB == nil || len(chosen) == 0 {
			return hidden
		}
		for _, title := range switchDB.TitlesMap {
			for id, dlc := range title.Dlc {
				if dlclang.Hidden(dlc.Name, chosen) {
					hidden[strings.ToLower(id)] = struct{}{}
				}
			}
		}
		return hidden
	}).(map[string]struct{})
}

// ignoredDLC are the DLC that are not listed as missing: the ones ignored and the language packs
// in the languages not chosen.
func (web *Web) ignoredDLC(appSettings *settings.AppSettings) map[string]struct{} {
	ignored := toLowerSet(appSettings.IgnoreDLCTitleIds)
	for id := range web.languageHiddenDLC() {
		ignored[id] = struct{}{}
	}
	return ignored
}

// DLCLanguage is a language that can be chosen in Settings.
type DLCLanguage struct {
	Code   string
	Name   string
	Chosen bool
}

// dlcLanguages are the languages Settings shows, with the ones chosen.
func dlcLanguages(appSettings *settings.AppSettings) []DLCLanguage {
	chosen := dlclang.Chosen(appSettings.DLCLanguages)
	languages := make([]DLCLanguage, len(dlclang.Codes))
	for i, code := range dlclang.Codes {
		languages[i] = DLCLanguage{Code: code, Name: dlclang.Names[code], Chosen: chosen[code]}
	}
	return languages
}

// chosenDLCLanguages are the known codes of the form, in the order of Settings.
func chosenDLCLanguages(codes []string) []string {
	chosen := dlclang.Chosen(codes)
	list := []string{}
	for _, code := range dlclang.Codes {
		if chosen[code] {
			list = append(list, code)
		}
	}
	return list
}
