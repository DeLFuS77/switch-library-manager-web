package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
)

// interfaceLanguageFile keeps, in the data folder, the language the interface was last used in.
const interfaceLanguageFile = "interface-language.txt"

// interfaceLanguage remembers the language the interface was last used in, for what the app
// sends by itself (see notificationLanguage).
type interfaceLanguage struct {
	mutex  sync.Mutex
	value  string
	loaded bool
}

// load reads the language kept by the run before, once; the mutex is held.
func (l *interfaceLanguage) load(dataFolder string) {
	if l.loaded {
		return
	}
	l.loaded = true
	if dataFolder == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(dataFolder, interfaceLanguageFile))
	if err != nil {
		return
	}
	if lang := strings.TrimSpace(string(data)); i18n.IsSupportedLanguage(lang) {
		l.value = lang
	}
}

// noteInterfaceLanguage remembers the language a page was shown in. Only a browser, which tells
// its languages, or a user who chose one counts: a script calling the API tells none and would
// turn the notifications to English.
func (web *Web) noteInterfaceLanguage(r *http.Request, lang string, chosenByUser bool) {
	if !i18n.IsSupportedLanguage(lang) || (!chosenByUser && r.Header.Get("Accept-Language") == "") {
		return
	}
	l := &web.lastLanguage
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.load(web.dataFolder)
	if l.value == lang {
		return
	}
	l.value = lang
	if web.dataFolder == "" {
		return
	}
	if err := durable.WriteFile(filepath.Join(web.dataFolder, interfaceLanguageFile), []byte(lang+"\n")); err != nil && web.sugarLogger != nil {
		web.sugarLogger.Warnf("Failed to keep the language of the interface: %v", err)
	}
}

// notificationLanguage is the language of what the app sends by itself, such as notifications:
// the one chosen in the settings, else the one the interface was last used in, kept across
// restarts, else English. Without a language chosen in the settings the interface follows the
// browser or the user, and the notifications were always in English.
func (web *Web) notificationLanguage(chosen string) string {
	if i18n.IsSupportedLanguage(chosen) {
		return chosen
	}
	l := &web.lastLanguage
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.load(web.dataFolder)
	if l.value != "" {
		return l.value
	}
	return i18n.DEFAULT_LANGUAGE
}
