package web

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The English text is the translation key: a missing translation shows the English text.

const DEFAULT_LANGUAGE = "en"

// supportedLanguages lists the interface languages, the first one is the default.
var supportedLanguages = []string{"en", "es"}

var languageNames = map[string]string{
	"en": "English",
	"es": "Español",
}

func translate(lang string, text string) string {
	if translated, ok := translations[lang][text]; ok {
		return translated
	}
	return text
}

func translatef(lang string, text string, args ...any) string {
	return fmt.Sprintf(translate(lang, text), args...)
}

// jsTranslations returns the translations of the texts used by web.js.
func jsTranslations(lang string) map[string]string {
	result := map[string]string{}
	for _, text := range jsTexts {
		if translated := translate(lang, text); translated != text {
			result[text] = translated
		}
	}
	return result
}

func i18nFuncs(lang string) template.FuncMap {
	return template.FuncMap{
		"t": func(text string, args ...any) string {
			if len(args) > 0 {
				return translatef(lang, text, args...)
			}
			return translate(lang, text)
		},
		"lang":           func() string { return lang },
		"jsTranslations": func() map[string]string { return jsTranslations(lang) },
		"languageName":   func(code string) string { return languageNames[code] },
	}
}

// templateSet holds the same templates once per interface language.
type templateSet map[string]*template.Template

func parseTemplates(fsys fs.FS, fsPatterns ...string) (templateSet, error) {
	base, err := template.New("layout").Funcs(funcMap).Funcs(i18nFuncs(DEFAULT_LANGUAGE)).ParseFS(fsys, fsPatterns...)
	if err != nil {
		return nil, err
	}

	set := templateSet{DEFAULT_LANGUAGE: base}
	for _, lang := range supportedLanguages {
		if lang == DEFAULT_LANGUAGE {
			continue
		}
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		set[lang] = clone.Funcs(i18nFuncs(lang))
	}
	return set, nil
}

func (set templateSet) execute(w io.Writer, lang string, data any) error {
	tmpl, ok := set[lang]
	if !ok {
		tmpl = set[DEFAULT_LANGUAGE]
	}
	return tmpl.ExecuteTemplate(w, "layout", data)
}

func isSupportedLanguage(lang string) bool {
	for _, supported := range supportedLanguages {
		if supported == lang {
			return true
		}
	}
	return false
}

// languageFromHeader returns the first supported language of an Accept-Language header.
// Browsers list the languages in order of preference.
func languageFromHeader(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		primary := strings.SplitN(tag, "-", 2)[0]
		if isSupportedLanguage(primary) {
			return primary
		}
	}
	return DEFAULT_LANGUAGE
}

// requestLanguage returns the interface language: the one chosen in the settings, or the
// browser's preferred language.
func (web *Web) requestLanguage(r *http.Request) string {
	if lang := settings.ReadSettings(web.dataFolder).Language; isSupportedLanguage(lang) {
		return lang
	}
	return languageFromHeader(r.Header.Get("Accept-Language"))
}
