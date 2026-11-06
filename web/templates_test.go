package web

import (
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// All pages must parse with the layout and partials, in every language.
func TestTemplatesParse(t *testing.T) {
	root := os.DirFS("..")
	pages, err := filepath.Glob("../resources/pages/*.html")
	if err != nil || len(pages) == 0 {
		t.Fatalf("no pages found: %v", err)
	}
	for _, page := range pages {
		name := "resources/pages/" + filepath.Base(page)
		set, err := parseTemplates(root, "resources/layout.html", "resources/partials/*.html", name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, lang := range i18n.SupportedLanguages {
			if set[lang] == nil || set[lang].Lookup("main") == nil {
				t.Fatalf("%s: missing template for %s", name, lang)
			}
		}
	}
}

// Every translation key must be used, and every translated text must be non-empty.
func TestTranslationsAreUsed(t *testing.T) {
	var sources strings.Builder
	for _, pattern := range []string{"../resources/*.html", "../resources/*/*.html", "../resources/js/*.js", "*.go", "../internal/*/*.go", "../process/*.go", "../db/*.go", "../switchfs/*.go"} {
		files, _ := filepath.Glob(pattern)
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") || strings.HasPrefix(filepath.Base(file), "translations") {
				continue
			}
			data, _ := os.ReadFile(file)
			sources.Write(data)
		}
	}
	// built at runtime by intervalLabel
	for _, hours := range []int{0, 6, 12, 24, 168} {
		sources.WriteString(funcMap["intervalLabel"].(func(int) string)(hours))
	}
	all := sources.String()
	for lang, texts := range i18n.Translations {
		for key, value := range texts {
			if strings.TrimSpace(value) == "" {
				t.Errorf("%s: empty translation for %q", lang, key)
			}
			if !strings.Contains(all, key) && !strings.Contains(all, strings.ReplaceAll(key, `"`, `\"`)) {
				t.Errorf("%s: unused translation key %q", lang, key)
			}
		}
	}
}

// Every text marked for translation in the templates and web.js has a Spanish translation.
func TestSpanishTranslationIsComplete(t *testing.T) {
	untranslated := map[string]bool{
		// placeholders and names are the same in every language
		"Switch Library Manager": true, "DLC": true, "ID": true,
		"{TITLE_NAME}": true, "{TITLE_ID}": true, "{VERSION}": true, "{VERSION_TXT}": true,
		"{REGION}": true, "{TYPE}": true, "{DLC_NAME}": true,
	}
	marked := regexp.MustCompile(`\{\{t "([^"]+)"`)
	files, _ := filepath.Glob("../resources/*/*.html")
	layout, _ := filepath.Glob("../resources/*.html")
	for _, file := range append(files, layout...) {
		data, _ := os.ReadFile(file)
		for _, match := range marked.FindAllStringSubmatch(string(data), -1) {
			if _, ok := i18n.Translations["es"][match[1]]; !ok && !untranslated[match[1]] {
				t.Errorf("%s: no Spanish translation for %q", filepath.Base(file), match[1])
			}
		}
	}
	for _, text := range i18n.JSTexts {
		if _, ok := i18n.Translations["es"][text]; !ok {
			t.Errorf("web.js: no Spanish translation for %q", text)
		}
	}
}

// Every text web.js passes to t() is sent to the browser (i18n.JSTexts) and translated in every
// language: t() only knows the texts of i18n.JSTexts, the others are shown in English.
func TestJsTextsAreSentAndTranslated(t *testing.T) {
	// t('...'), t("...") and t(`...`) without ${}
	literal := regexp.MustCompile(`\bt\(\s*(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"|` + "`([^`$]*)`" + `)`)
	unescape := strings.NewReplacer(`\'`, `'`, `\"`, `"`, `\\`, `\`)
	sent := map[string]bool{}
	for _, text := range i18n.JSTexts {
		sent[text] = true
	}
	files, _ := filepath.Glob("../resources/js/*.js")
	if len(files) == 0 {
		t.Fatal("no scripts found")
	}
	found := 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range literal.FindAllStringSubmatch(string(data), -1) {
			text := unescape.Replace(match[1] + match[2] + match[3])
			found++
			if !sent[text] {
				t.Errorf("%s: %q is not in jsTexts", filepath.Base(file), text)
			}
			for _, lang := range i18n.SupportedLanguages {
				if _, ok := i18n.Translations[lang][text]; !ok && lang != i18n.DEFAULT_LANGUAGE {
					t.Errorf("%s: no %s translation for %q", filepath.Base(file), lang, text)
				}
			}
		}
	}
	if found < 20 {
		t.Fatalf("only %d texts found", found)
	}
}

func TestLanguageFromHeader(t *testing.T) {
	for header, want := range map[string]string{
		"":                        "en",
		"es-ES,es;q=0.9,en;q=0.8": "es",
		"en-US,en;q=0.9,es;q=0.8": "en",
		"de-DE,de;q=0.9,es;q=0.7": "de",
		"fr-FR,fr;q=0.9":          "fr",
		"sv-SE,sv;q=0.9,it;q=0.5": "it",
		"nl-NL,nl;q=0.9":          "nl",
		"zh-CN,zh;q=0.9":          "zh",
		"pt-BR":                   "pt",
		"pl-PL,pl;q=0.9":          "en",
		"ja-JP,ja;q=0.9":          "ja",
		"ES":                      "es",
	} {
		if got := i18n.LanguageFromHeader(header); got != want {
			t.Errorf("languageFromHeader(%q) = %q, want %q", header, got, want)
		}
	}
	if i18n.Translatef("es", "%v DLC missing", 3) != "Faltan 3 DLC" || i18n.Translate("xx", "Library") != "Library" {
		t.Fatal("unexpected translation")
	}
}

// A language that is not one of the list names no file and no download.
func TestTitleLanguageOnlyOfTheList(t *testing.T) {
	if i18n.SupportedLanguage("es") != "es" || i18n.SupportedLanguage("ES") != "" || i18n.SupportedLanguage("../es") != "" {
		t.Fatal("unexpected supported language")
	}
	web := newTestWeb(t)
	if _, ok := web.loadTitleLanguage("../../es", true); ok {
		t.Fatal("titles of a language not in the list were loaded")
	}
}

func TestLocalizedDatesAndIssues(t *testing.T) {
	date := time.Date(2026, 10, 5, 13, 5, 0, 0, time.Local)
	if got := i18n.FormatDate("es", date); got != "5 oct 2026" {
		t.Errorf("es date = %q", got)
	}
	if got := i18n.FormatDate("en", date); got != "Oct 5, 2026" {
		t.Errorf("en date = %q", got)
	}
	for lang, want := range map[string]string{"ja": "2026年10月5日", "zh": "2026年10月5日", "ko": "2026년 10월 5일", "nl": "5 okt. 2026", "ru": "5 окт. 2026"} {
		if got := i18n.FormatDate(lang, date); got != want {
			t.Errorf("%s date = %q, want %q", lang, got, want)
		}
	}
	if got := i18n.FormatDateTime("es", date); got != "5 oct 2026 13:05" {
		t.Errorf("es date time = %q", got)
	}
	if i18n.FormatDate("es", time.Time{}) != "" || i18n.FormatDateTime("es", time.Time{}) != "nunca" {
		t.Error("zero dates")
	}

	issues := map[string]string{
		"file type is not supported":                           "tipo de archivo no compatible",
		"duplicate update file (C:\roms\a [v1].nsp)":           "archivo de actualización duplicado (C:\roms\a [v1].nsp)",
		"old update file, newer update exist locally (/x.nsp)": "actualización antigua, hay una más nueva (/x.nsp)",
		"failed to read NSP: prod.keys has no key_area_key_application_15. The title needs keys from a newer firmware: update prod.keys, or add [TitleID][vVersion] to the file name": "no se pudo leer el NSP: a prod.keys le falta key_area_key_application_15. El juego necesita claves de un firmware más nuevo: actualiza prod.keys o añade [TitleID][vVersión] al nombre del archivo",
		"identified by file name only, failed to read XCI [reason: EOF]": "identificado solo por el nombre del archivo; no se pudo leer el XCI [motivo: EOF]",
		"something new and unknown":                                      "something new and unknown",
	}
	for text, want := range issues {
		if got := i18n.TranslateIssue("es", text); got != want {
			t.Errorf("translateIssue(%q)\n got %q\nwant %q", text, got, want)
		}
		if got := i18n.TranslateIssue("en", text); got != text {
			t.Errorf("English issue changed: %q", got)
		}
	}
}

// Every interface language translates the same texts as Spanish, with the same placeholders.
func TestAllLanguagesAreComplete(t *testing.T) {
	placeholders := regexp.MustCompile(`%[vsd%]`)
	for _, lang := range i18n.SupportedLanguages {
		if lang == i18n.DEFAULT_LANGUAGE || lang == "es" {
			continue
		}
		if i18n.LanguageNames[lang] == "" || len(i18n.MonthAbbreviations[lang]) == 0 {
			t.Errorf("%s: no name or months", lang)
		}
		for text := range i18n.Translations["es"] {
			translated, ok := i18n.Translations[lang][text]
			if !ok {
				t.Errorf("%s: no translation for %q", lang, text)
				continue
			}
			if strings.Join(placeholders.FindAllString(text, -1), "") != strings.Join(placeholders.FindAllString(translated, -1), "") {
				t.Errorf("%s: placeholders differ in %q", lang, translated)
			}
		}
		for text := range i18n.Translations[lang] {
			if _, ok := i18n.Translations["es"][text]; !ok {
				t.Errorf("%s: translation of an unknown text %q", lang, text)
			}
		}
	}
}

func TestTitleLanguagesOnlyThoseInUse(t *testing.T) {
	web := newTestWeb(t)
	if got := web.titleLanguages(); len(got) != 0 {
		t.Fatalf("nothing is used yet: %v", got)
	}
	os.WriteFile(filepath.Join(web.dataFolder, "titles.es.json"), []byte("{}"), 0644)
	web.noteLanguage("fr")
	web.noteLanguage("en")
	if got := strings.Join(web.titleLanguages(), ","); got != "es,fr" {
		t.Fatalf("got %s", got)
	}
}

func TestNumbersHaveTheSeparatorOfTheLanguage(t *testing.T) {
	for _, c := range []struct {
		lang  string
		value int
		want  string
	}{
		{"en", 11382, "11,382"}, {"en", 999, "999"}, {"en", 1234567, "1,234,567"}, {"es", 3111, "3111"}, {"es", 11382, "11.382"},
		{"de", 3111, "3.111"}, {"fr", 11382, "11\u202f382"}, {"ja", 2547, "2,547"}, {"en", 0, "0"}, {"en", -1500, "-1,500"},
	} {
		if got := i18n.FormatNumber(c.lang, c.value); got != c.want {
			t.Errorf("%s %d: %q, want %q", c.lang, c.value, got, c.want)
		}
	}
}

// The cards of the status of the Library are its filter: each one counts the games it shows,
// with the separator of the language, the one shown is marked, and the ⓘ of a card tells what it
// counts.
func TestLibraryStatusCards(t *testing.T) {
	web := newTestWeb(t)
	set, err := parseTemplates(os.DirFS(".."), "resources/layout.html", "resources/partials/card.html", "resources/partials/filter.html",
		"resources/partials/pagination.html", "resources/pages/index.html")
	if err != nil {
		t.Fatal(err)
	}
	global := web.globalPageData("index")
	global.HasLibrary = true
	filter := defaultFilter()
	filter.Status = STATUS_UPDATE
	data := LibraryPageData{
		TitleItemsPageData: TitleItemsPageData{GlobalPageData: global, Filter: filter, Views: web.libraryViews("all", "es", "")},
		Facets:             LibraryFacets{All: 24116, Missing: 10338, Owned: 13778, WithFile: 13774, Size: 3 << 40, Update: 2610, Dlc: 840, DlcMissing: 4236, Complete: 10725},
	}
	var page strings.Builder
	if err := set.execute(&page, "es", data); err != nil {
		t.Fatal(err)
	}
	out := page.String()
	for _, want := range []string{`<span class="kpi-value">24.116</span>`, "13.778 tuyos · " + i18n.FormatSizeIn("es", 3<<40), `<span class="kpi-value">2610</span>`, "4236 DLC en total",
		`<span class="kpi-value">10.725</span>`, "Al día · 77%", "el 77% de los 13.774 juegos con archivo", `style="width: 77%"`,
		`class="kpi kpi-warning is-active" href="?missing=0&amp;per_page=24&amp;sort_by=name&amp;sort_order=asc&amp;status=update" aria-current="true"`,
		`class="kpi kpi-info" href="?missing=0&amp;per_page=24&amp;sort_by=name&amp;sort_order=asc&amp;status=dlc"`,
		`<span class="kpi-name help-name">Con actualización</span><span class="form-text">`} {
		if !strings.Contains(out, want) {
			t.Errorf("the cards of the status miss %q", want)
		}
	}
	if strings.Contains(out, "library-overview") || strings.Contains(out, "filter-pills") {
		t.Error("the overview and the pills of the status are gone")
	}
	// the panel of filters is in the toolbar, after the search
	if search, panel := strings.Index(out, `class="toolbar-search"`), strings.Index(out, `class="dropdown filter-panel"`); search < 0 || panel < search || !strings.Contains(out, `class="toolbar has-filters"`) {
		t.Errorf("the panel of filters is not in the toolbar: %d, %d", search, panel)
	}
}

// Sizes have the decimal separator of the language, in the pages and in web.js, which lists the
// same languages.
func TestSizesHaveTheDecimalSeparatorOfTheLanguage(t *testing.T) {
	const size = 3 * 1024 * 1024 * 1024 / 2
	for lang, want := range map[string]string{"en": "1.5 GB", "es": "1,5 GB", "de": "1,5 GB", "fr": "1,5 GB", "ja": "1.5 GB", "zh": "1.5 GB"} {
		if got := i18n.FormatSizeIn(lang, size); got != want {
			t.Errorf("%s: %q, want %q", lang, got, want)
		}
		if got := i18nFuncs(lang)["formatSize"].(func(int64) string)(size); got != want {
			t.Errorf("%s template: %q, want %q", lang, got, want)
		}
	}
	if got := i18n.FormatSizeIn("es", 512); got != "512 B" {
		t.Errorf("bytes: %q", got)
	}

	data, err := os.ReadFile("../resources/js/08-compress.js")
	if err != nil {
		t.Fatal(err)
	}
	list := regexp.MustCompile(`DECIMAL_COMMA_LANGUAGES = \[([^\]]*)\]`).FindStringSubmatch(string(data))
	if list == nil {
		t.Fatal("web.js lists no languages with a decimal comma")
	}
	languages := regexp.MustCompile(`'([a-z]+)'`).FindAllStringSubmatch(list[1], -1)
	if len(languages) != len(i18n.DecimalSeparators) {
		t.Errorf("web.js lists %d languages with a decimal comma, the server %d", len(languages), len(i18n.DecimalSeparators))
	}
	for _, lang := range languages {
		if i18n.DecimalSeparators[lang[1]] != "," {
			t.Errorf("web.js writes a decimal comma in %s, the server does not", lang[1])
		}
	}
}
