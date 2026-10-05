package web

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
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
		"formatTime":     func(value time.Time) string { return formatDate(lang, value) },
		"formatDateTime": func(value time.Time) string { return formatDateTime(lang, value) },
		"issue":          func(text string) string { return translateIssue(lang, text) },
	}
}

// templateSet holds the same templates once per interface language.
type templateSet map[string]*template.Template

func parseTemplates(fsys fs.FS, fsPatterns ...string) (templateSet, error) {
	// components shared by every page
	fsPatterns = append(fsPatterns, "resources/partials/components.html")
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

// executeTemplate renders one named template of the page, e.g. a part refreshed by script.
func (set templateSet) executeTemplate(w io.Writer, lang string, name string, data any) error {
	tmpl, ok := set[lang]
	if !ok {
		tmpl = set[DEFAULT_LANGUAGE]
	}
	return tmpl.ExecuteTemplate(w, name, data)
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

var monthAbbreviations = map[string][12]string{
	"es": {"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"},
}

// formatDate formats a date for the interface language: "Oct 5, 2026" / "5 oct 2026".
func formatDate(lang string, value time.Time) string {
	if value.IsZero() {
		return ""
	}
	if months, ok := monthAbbreviations[lang]; ok {
		return fmt.Sprintf("%d %s %d", value.Day(), months[value.Month()-1], value.Year())
	}
	return value.Format("Jan 2, 2006")
}

// formatDateTime formats a local date and time for the interface language.
func formatDateTime(lang string, value time.Time) string {
	if value.IsZero() {
		return translate(lang, "never")
	}
	value = value.Local()
	return formatDate(lang, value) + " " + value.Format("15:04")
}

// Issue texts are created in English by the scanner and contain file paths and technical
// details, so they are translated by pattern; the captured parts stay as they are.
var issuePatterns = []struct {
	pattern *regexp.Regexp
	text    string
}{
	{regexp.MustCompile(`^file type is not supported$`), "file type is not supported"},
	{regexp.MustCompile(`^base file is missing$`), "base file is missing"},
	{regexp.MustCompile(`^duplicate (update|base|DLC) file \((.*)\)$`), "duplicate %v file (%v)"},
	{regexp.MustCompile(`^old update file, newer update exist locally \((.*)\)$`), "old update file, newer update exist locally (%v)"},
	{regexp.MustCompile(`^old DLC file, newer version exist locally \((.*)\)$`), "old DLC file, newer version exist locally (%v)"},
	{regexp.MustCompile(`^failed to read (NSP|XCI|split files): prod\.keys has no (\S+)\. The title needs keys from a newer firmware: update prod\.keys, or add \[TitleID\]\[vVersion\] to the file name$`),
		"failed to read %v: prod.keys has no %v. The title needs keys from a newer firmware: update prod.keys, or add [TitleID][vVersion] to the file name"},
	{regexp.MustCompile(`^failed to read (NSP|XCI|split files) \[reason: (.*)\]$`), "failed to read %v [reason: %v]"},
	{regexp.MustCompile(`^unable to determine title-Id / version - (.*)$`), "unable to determine title ID / version - %v"},
	{regexp.MustCompile(`^damaged file: (.*)$`), "damaged file: %v"},
}

// translateIssue translates an issue text created by the scanner.
func translateIssue(lang string, text string) string {
	if lang == DEFAULT_LANGUAGE {
		return text
	}
	if rest, ok := strings.CutPrefix(text, "identified by file name only, "); ok {
		return translatef(lang, "identified by file name only, %v", translateIssue(lang, rest))
	}
	for _, issue := range issuePatterns {
		if match := issue.pattern.FindStringSubmatch(text); match != nil {
			args := make([]any, 0, len(match)-1)
			for _, group := range match[1:] {
				args = append(args, translate(lang, group))
			}
			return translatef(lang, issue.text, args...)
		}
	}
	return text
}

// titleName returns the name of a title in the interface language, or fallback when the
// titles database has no translation.
func titleName(switchDB *db.SwitchTitlesDB, lang string, titleId string, fallback string) string {
	if localized, ok := switchDB.LocalizedTitle(lang, titleId); ok {
		return localized.Name
	}
	return fallback
}
