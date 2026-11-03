package web

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func i18nFuncs(lang string) template.FuncMap {
	return template.FuncMap{
		"t": func(text string, args ...any) string {
			if len(args) > 0 {
				return i18n.Translatef(lang, text, args...)
			}
			return i18n.Translate(lang, text)
		},
		// tCode translates a text whose arguments are shown as code, like a word to look for in
		// a log: tCode "Look for %v in the log." "SETUP CODE"
		"tCode": func(text string, args ...any) template.HTML {
			codes := make([]any, len(args))
			for i, arg := range args {
				codes[i] = "<code>" + template.HTMLEscapeString(fmt.Sprint(arg)) + "</code>"
			}
			return template.HTML(fmt.Sprintf(template.HTMLEscapeString(i18n.Translate(lang, text)), codes...))
		},
		// tStrong translates a text whose arguments are shown in bold, like a name
		"tStrong": func(text string, args ...any) template.HTML {
			strong := make([]any, len(args))
			for i, arg := range args {
				strong[i] = "<strong>" + template.HTMLEscapeString(fmt.Sprint(arg)) + "</strong>"
			}
			return template.HTML(fmt.Sprintf(template.HTMLEscapeString(i18n.Translate(lang, text)), strong...))
		},
		"lang":           func() string { return lang },
		"jsTranslations": func() map[string]string { return i18n.JSTranslations(lang) },
		"languageName":   func(code string) string { return i18n.LanguageNames[code] },
		"formatTime":     func(value time.Time) string { return i18n.FormatDate(lang, value) },
		// a date of the titles database (20240131), or "" when it has none
		"releaseDate": func(value int) string {
			if date, err := intToTime(value); err == nil && value > 0 {
				return i18n.FormatDate(lang, date)
			}
			return ""
		},
		"formatMonth":    func(value time.Time) string { return i18n.FormatMonth(lang, value) },
		"formatDay":      func(value time.Time) string { return i18n.FormatDay(lang, value) },
		"num":            func(value int) string { return i18n.FormatNumber(lang, value) },
		"numShort":       func(value int) string { return i18n.FormatShortNumber(lang, value) },
		"formatDateTime": func(value time.Time) string { return i18n.FormatDateTime(lang, value) },
		"issue":          func(text string) string { return i18n.TranslateIssue(lang, text) },
		// sizes with the decimal separator of the language
		"formatSize":  func(size int64) string { return i18n.FormatSizeIn(lang, size) },
		"formatBytes": func(size int) string { return i18n.FormatSizeIn(lang, int64(size)) },
	}
}

// templateSet holds the same templates once per interface language.
type templateSet map[string]*template.Template

func parseTemplates(fsys fs.FS, fsPatterns ...string) (templateSet, error) {
	// components shared by every page
	fsPatterns = append(fsPatterns, "resources/partials/components.html")
	base, err := template.New("layout").Funcs(funcMap).Funcs(i18nFuncs(i18n.DEFAULT_LANGUAGE)).ParseFS(fsys, fsPatterns...)
	if err != nil {
		return nil, err
	}

	set := templateSet{i18n.DEFAULT_LANGUAGE: base}
	for _, lang := range i18n.SupportedLanguages {
		if lang == i18n.DEFAULT_LANGUAGE {
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
		tmpl = set[i18n.DEFAULT_LANGUAGE]
	}
	return tmpl.ExecuteTemplate(w, "layout", data)
}

// executeTemplate renders one named template of the page, e.g. a part refreshed by script.
func (set templateSet) executeTemplate(w io.Writer, lang string, name string, data any) error {
	tmpl, ok := set[lang]
	if !ok {
		tmpl = set[i18n.DEFAULT_LANGUAGE]
	}
	return tmpl.ExecuteTemplate(w, name, data)
}

// requestLanguage returns the interface language: the one chosen in the settings, or the
// browser's preferred language.
func (web *Web) requestLanguage(r *http.Request) string {
	// the language chosen by the user comes first
	if principal := principalFrom(r); principal != nil && principal.Name != "" && web.auth != nil {
		if user, ok := web.auth.users.Get(principal.Name); ok && i18n.IsSupportedLanguage(user.Language) {
			web.noteLanguage(user.Language)
			return user.Language
		}
	}
	lang := settings.ReadSettings(web.dataFolder).Language
	if !i18n.IsSupportedLanguage(lang) {
		lang = i18n.LanguageFromHeader(r.Header.Get("Accept-Language"))
	}
	web.noteLanguage(lang)
	return lang
}

// titleName returns the name of a title in the interface language, or fallback when the
// titles database has no translation. A fallback in a script of another language (the
// Japanese name of a game the database knows from the Japanese store only) gives way to the
// name the game gives itself in the interface language or in English, when it has one, and
// so does a placeholder such as "-".
func titleName(switchDB *db.SwitchTitlesDB, lang string, titleId string, fallback string) string {
	if localized, ok := switchDB.LocalizedTitle(lang, titleId); ok && usableName(localized.Name) {
		return localized.Name
	}
	// a placeholder such as "-" gives way too
	if !fitsLanguage(lang, fallback) || (fallback != "" && !usableName(fallback)) {
		if lookup := fileNacp.Load(); lookup != nil {
			if name := nacpName((*lookup)(titleId), lang); name != "" {
				return name
			}
		}
	}
	return fallback
}
