package web

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// titleLanguages remembers the interface languages used by the browsers. Each file of
// translated title names is large (tens of MB), so only the languages in use are
// downloaded, not every supported one.
type titleLanguages struct {
	mutex sync.Mutex
	seen  map[string]bool
	// serializes reading and downloading the files
	fileMutex sync.Mutex
}

// titleLanguages are the languages whose title names are loaded: the one chosen in the
// settings, those asked for by browsers and those downloaded before.
func (web *Web) titleLanguages() []string {
	chosen := settings.ReadSettings(web.dataFolder).Language
	web.languages.mutex.Lock()
	defer web.languages.mutex.Unlock()
	result := []string{}
	for _, lang := range supportedLanguages {
		if lang == DEFAULT_LANGUAGE {
			continue
		}
		_, err := os.Stat(filepath.Join(web.dataFolder, "titles."+lang+".json"))
		if lang == chosen || web.languages.seen[lang] || err == nil {
			result = append(result, lang)
		}
	}
	return result
}

// noteLanguage records the language of a request. The title names of a language used for
// the first time are loaded in the background, so they appear without a synchronization.
func (web *Web) noteLanguage(lang string) {
	if lang == DEFAULT_LANGUAGE {
		return
	}
	web.languages.mutex.Lock()
	if web.languages.seen == nil {
		web.languages.seen = map[string]bool{}
	}
	first := !web.languages.seen[lang]
	web.languages.seen[lang] = true
	web.languages.mutex.Unlock()
	if first {
		go web.addTitleLanguage(lang)
	}
}

// addTitleLanguage adds the title names of a language to the loaded titles database.
func (web *Web) addTitleLanguage(lang string) {
	switchDB, _ := web.state.get()
	if switchDB == nil || web.state.IsSynchronizing() {
		// loaded by the synchronization
		return
	}
	if _, ok := switchDB.Localized[lang]; ok {
		return
	}
	titles, ok := web.loadTitleLanguage(lang, true)
	if !ok {
		return
	}
	web.state.addLocalized(lang, titles)
	web.invalidateDerived()
}

// addLocalized replaces the titles database with a copy that also has the names of a
// language, so pages reading the current one are not affected.
func (s *WebState) addLocalized(lang string, titles map[string]db.LocalizedTitle) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.switchDB == nil {
		return
	}
	updated := *s.switchDB
	updated.Localized = map[string]map[string]db.LocalizedTitle{lang: titles}
	for other, names := range s.switchDB.Localized {
		if other != lang {
			updated.Localized[other] = names
		}
	}
	s.switchDB = &updated
	s.version++
}
