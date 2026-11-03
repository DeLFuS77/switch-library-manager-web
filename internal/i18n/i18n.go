// Package i18n holds the interface in every language of the app: its translations, and
// numbers, sizes and dates written as each language writes them. The English text is the
// translation key: a missing translation shows the English text.
package i18n

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const DEFAULT_LANGUAGE = "en"

// SupportedLanguages lists the interface languages, the first one is the default.
var SupportedLanguages = []string{"en", "es", "fr", "de", "it", "pt", "nl", "ru", "ja", "ko", "zh"}

var LanguageNames = map[string]string{
	"en": "English",
	"es": "Español",
	"fr": "Français",
	"de": "Deutsch",
	"it": "Italiano",
	"pt": "Português",
	"nl": "Nederlands",
	"ru": "Русский",
	"ja": "日本語",
	"ko": "한국어",
	"zh": "简体中文",
}

func Translate(lang string, text string) string {
	if translated, ok := Translations[lang][text]; ok {
		return translated
	}
	return text
}

func Translatef(lang string, text string, args ...any) string {
	return fmt.Sprintf(Translate(lang, text), args...)
}

// JSTranslations returns the translations of the texts used by web.js.
func JSTranslations(lang string) map[string]string {
	result := map[string]string{}
	for _, text := range JSTexts {
		if translated := Translate(lang, text); translated != text {
			result[text] = translated
		}
	}
	return result
}

func IsSupportedLanguage(lang string) bool {
	return SupportedLanguage(lang) != ""
}

// SupportedLanguage is lang as the list of supported languages has it, or "" when it is not
// one of them. The value comes from the list, not from what a browser sent: it names files of
// the data folder and goes in the addresses of downloads.
func SupportedLanguage(lang string) string {
	for _, supported := range SupportedLanguages {
		if supported == lang {
			return supported
		}
	}
	return ""
}

// LanguageFromHeader returns the first supported language of an Accept-Language header.
// Browsers list the languages in order of preference.
func LanguageFromHeader(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		if primary := SupportedLanguage(strings.SplitN(tag, "-", 2)[0]); primary != "" {
			return primary
		}
	}
	return DEFAULT_LANGUAGE
}

// thousands separators by language; the others use a comma
var thousandsSeparators = map[string]string{"es": ".", "pt": ".", "it": ".", "de": ".", "nl": ".", "fr": " ", "ru": " "}

// FormatNumber writes a number with the thousands separator of the language: 11382 is
// "11,382" in English and "11.382" in Spanish, where four-digit numbers have none.
func FormatNumber(lang string, value int) string {
	digits := strconv.Itoa(value)
	negative := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	minimum := 4
	if lang == "es" || lang == "pt" {
		minimum = 5
	}
	if len(digits) < minimum {
		if negative {
			return "-" + digits
		}
		return digits
	}
	separator, ok := thousandsSeparators[lang]
	if !ok {
		separator = ","
	}
	var builder strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			builder.WriteString(separator)
		}
		builder.WriteRune(digit)
	}
	if negative {
		return "-" + builder.String()
	}
	return builder.String()
}

// decimal separators by language, as web.js has them (FormatSize); the others use a point
var DecimalSeparators = map[string]string{"es": ",", "pt": ",", "it": ",", "de": ",", "nl": ",", "fr": ",", "ru": ","}

// FormatSizeIn is FormatSize with the decimal separator of the language: 1.5 GB in English is
// 1,5 GB in Spanish.
func FormatSizeIn(lang string, size int64) string {
	text := FormatSize(size)
	if separator, ok := DecimalSeparators[lang]; ok {
		return strings.Replace(text, ".", separator, 1)
	}
	return text
}

// FormatShortNumber writes a big number short, for the room of a phone: 24943 is "25k", and
// "2.5万" in Japanese and Chinese, "2.5만" in Korean, which count by ten thousands.
func FormatShortNumber(lang string, value int) string {
	switch lang {
	case "ja", "zh", "ko":
		unit := "万"
		if lang == "ko" {
			unit = "만"
		}
		tenths := (value + 500) / 1000
		if tenths%10 == 0 || tenths >= 1000 {
			return FormatNumber(lang, (tenths+5)/10) + unit
		}
		return strconv.Itoa(tenths/10) + "." + strconv.Itoa(tenths%10) + unit
	}
	return FormatNumber(lang, (value+500)/1000) + "k"
}

var MonthAbbreviations = map[string][12]string{
	"es": {"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"},
	"fr": {"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."},
	"de": {"Jan.", "Feb.", "März", "Apr.", "Mai", "Juni", "Juli", "Aug.", "Sept.", "Okt.", "Nov.", "Dez."},
	"it": {"gen", "feb", "mar", "apr", "mag", "giu", "lug", "ago", "set", "ott", "nov", "dic"},
	"pt": {"jan.", "fev.", "mar.", "abr.", "mai.", "jun.", "jul.", "ago.", "set.", "out.", "nov.", "dez."},
	"nl": {"jan.", "feb.", "mrt.", "apr.", "mei", "jun.", "jul.", "aug.", "sep.", "okt.", "nov.", "dec."},
	"ru": {"янв.", "февр.", "мар.", "апр.", "мая", "июн.", "июл.", "авг.", "сент.", "окт.", "нояб.", "дек."},
}

// languages that write dates as year, month and day with their own signs
var DateSigns = map[string][3]string{
	"ja": {"年", "月", "日"},
	"zh": {"年", "月", "日"},
	"ko": {"년 ", "월 ", "일"},
}

// FormatDate formats a date for the interface language: "Oct 5, 2026" / "5 oct 2026".
func FormatDate(lang string, value time.Time) string {
	if value.IsZero() {
		return ""
	}
	if signs, ok := DateSigns[lang]; ok {
		return fmt.Sprintf("%d%s%d%s%d%s", value.Year(), signs[0], value.Month(), signs[1], value.Day(), signs[2])
	}
	if months, ok := MonthAbbreviations[lang]; ok {
		if lang == "de" {
			return fmt.Sprintf("%d. %s %d", value.Day(), months[value.Month()-1], value.Year())
		}
		return fmt.Sprintf("%d %s %d", value.Day(), months[value.Month()-1], value.Year())
	}
	return value.Format("Jan 2, 2006")
}

var monthNames = map[string][12]string{
	"es": {"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"},
	"fr": {"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"},
	"de": {"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"},
	"it": {"gennaio", "febbraio", "marzo", "aprile", "maggio", "giugno", "luglio", "agosto", "settembre", "ottobre", "novembre", "dicembre"},
	"pt": {"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"},
	"nl": {"januari", "februari", "maart", "april", "mei", "juni", "juli", "augustus", "september", "oktober", "november", "december"},
	"ru": {"январь", "февраль", "март", "апрель", "май", "июнь", "июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"},
}

// FormatMonth formats a month and its year: "October 2026" / "octubre de 2026".
func FormatMonth(lang string, value time.Time) string {
	if signs, ok := DateSigns[lang]; ok {
		return fmt.Sprintf("%d%s%d%s", value.Year(), signs[0], value.Month(), strings.TrimSpace(signs[1]))
	}
	if months, ok := monthNames[lang]; ok {
		// a title: the month starts with a capital letter
		month := []rune(months[value.Month()-1])
		month[0] = unicode.ToUpper(month[0])
		switch lang {
		case "es", "pt":
			return fmt.Sprintf("%s de %d", string(month), value.Year())
		}
		return fmt.Sprintf("%s %d", string(month), value.Year())
	}
	return value.Format("January 2006")
}

// FormatDay formats a day of the month without the year: "Oct 8" / "8 oct".
func FormatDay(lang string, value time.Time) string {
	if signs, ok := DateSigns[lang]; ok {
		return fmt.Sprintf("%d%s%d%s", value.Month(), signs[1], value.Day(), signs[2])
	}
	if months, ok := MonthAbbreviations[lang]; ok {
		if lang == "de" {
			return fmt.Sprintf("%d. %s", value.Day(), months[value.Month()-1])
		}
		return fmt.Sprintf("%d %s", value.Day(), months[value.Month()-1])
	}
	return value.Format("Jan 2")
}

// FormatDateTime formats a local date and time for the interface language.
func FormatDateTime(lang string, value time.Time) string {
	if value.IsZero() {
		return Translate(lang, "never")
	}
	value = value.Local()
	return FormatDate(lang, value) + " " + value.Format("15:04")
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
	{regexp.MustCompile(`^not signed by Nintendo: (.*)$`), "not signed by Nintendo: %v"},
	{regexp.MustCompile(`^no title key \(rights ID (\w+)\): the file has no ticket and title\.keys does not list it$`), "no title key (rights ID %v): the file has no ticket and title.keys does not list it"},
}

// TranslateIssue translates an issue text created by the scanner.
func TranslateIssue(lang string, text string) string {
	if lang == DEFAULT_LANGUAGE {
		return text
	}
	if rest, ok := strings.CutPrefix(text, "identified by file name only, "); ok {
		return Translatef(lang, "identified by file name only, %v", TranslateIssue(lang, rest))
	}
	for _, issue := range issuePatterns {
		if match := issue.pattern.FindStringSubmatch(text); match != nil {
			args := make([]any, 0, len(match)-1)
			for _, group := range match[1:] {
				args = append(args, Translate(lang, group))
			}
			return Translatef(lang, issue.text, args...)
		}
	}
	return text
}

func FormatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}
