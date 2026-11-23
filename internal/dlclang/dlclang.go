// Package dlclang tells the language of a DLC that is a language pack or the commentary of a
// game in one language, from its name in the titles database (#200).
package dlclang

import (
	"regexp"
	"strings"
)

// Codes are the languages that can be chosen, in the order Settings shows them.
var Codes = []string{"en", "es", "es-419", "fr", "de", "it", "nl", "pt", "pt-br", "pl", "ru", "tr", "ar", "ja", "ko", "zh-hans", "zh-hant"}

// Names are the names of the languages in English, translated where they are shown.
var Names = map[string]string{
	"en": "English", "es": "Spanish (Spain)", "es-419": "Spanish (Latin America)", "fr": "French", "de": "German",
	"it": "Italian", "nl": "Dutch", "pt": "Portuguese", "pt-br": "Portuguese (Brazil)", "pl": "Polish", "ru": "Russian",
	"tr": "Turkish", "ar": "Arabic", "ja": "Japanese", "ko": "Korean", "zh-hans": "Simplified Chinese", "zh-hant": "Traditional Chinese",
}

// a DLC is a language pack when its name says so
var packPattern = regexp.MustCompile(`(?i)\blanguage\s+(pack|dlc)\b|\bin-game\s+commentary\b`)

type rule struct {
	code    string
	pattern *regexp.Regexp
}

// the most precise first: a match is taken out of the name before the next rules look at it, so
// "Latin American Spanish" is not Spanish of Spain too
var rules = func() []rule {
	list := [][2]string{
		{"es-419", `latin[ -]american\s+spanish|spanish\s*\(latin\s+americ\w*\)|\(es-?la\)|\(esmx\)|\(la\)`},
		{"es", `castilian\s+spanish|spanish\s*\(spain\)|\(es(-es)?\)`},
		{"pt-br", `brazilian\s+portuguese|portuguese\s*\(brazil\)|\(pt-?br\)|\(br\)`},
		{"pt", `portuguese\s*\(portugal\)|european\s+portuguese|\(pt\)`},
		{"zh-hans", `simplified\s+chinese|chinese\s*\(?simplified\)?|\(zhs\)|\(zh-?hans\)|\(cn\)|\(sc\)`},
		{"zh-hant", `traditional\s+chinese|chinese\s*\(?traditional\)?|\(zht\)|\(zh-?hant\)|\(tc\)`},
		{"es", `spanish`},
		{"pt", `portuguese`},
		{"en", `english|\(en\)`},
		{"fr", `french|\(fr\)`},
		{"de", `german|deutsch|\(de\)`},
		{"it", `italian|\(it\)`},
		{"nl", `dutch|\(nl\)`},
		{"pl", `polish|\(pl\)`},
		{"ru", `russian|\(ru\)`},
		{"tr", `turkish|\(tr\)`},
		{"ar", `arabic|\(ar\)`},
		{"ja", `japanese|\(ja\)|\(jp\)`},
		{"ko", `korean|\(ko\)|\(kr\)`},
	}
	rules := make([]rule, len(list))
	for i, r := range list {
		rules[i] = rule{r[0], regexp.MustCompile(`(?i)` + r[1])}
	}
	return rules
}()

// Language is the language of a DLC that is a language pack, and whether it is one. A pack whose
// language is not known, or that names several, has no language: it is always shown.
func Language(name string) (string, bool) {
	if !packPattern.MatchString(name) {
		return "", false
	}
	rest := name
	found := ""
	for _, r := range rules {
		if !r.pattern.MatchString(rest) {
			continue
		}
		if found != "" && found != r.code {
			return "", true
		}
		found = r.code
		rest = r.pattern.ReplaceAllString(rest, " ")
	}
	return found, true
}

// Hidden says whether a DLC is a language pack in a language not chosen. Nothing is hidden while
// no language is chosen.
func Hidden(name string, chosen map[string]bool) bool {
	if len(chosen) == 0 {
		return false
	}
	code, pack := Language(name)
	return pack && code != "" && !chosen[code]
}

// Chosen keeps the known codes of a list, each once.
func Chosen(codes []string) map[string]bool {
	chosen := map[string]bool{}
	for _, code := range codes {
		code = strings.ToLower(strings.TrimSpace(code))
		if _, known := Names[code]; known {
			chosen[code] = true
		}
	}
	return chosen
}
