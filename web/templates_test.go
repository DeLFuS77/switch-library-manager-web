package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
		for _, lang := range supportedLanguages {
			if set[lang] == nil || set[lang].Lookup("main") == nil {
				t.Fatalf("%s: missing template for %s", name, lang)
			}
		}
	}
}

// Every translation key must be used, and every translated text must be non-empty.
func TestTranslationsAreUsed(t *testing.T) {
	var sources strings.Builder
	for _, pattern := range []string{"../resources/*.html", "../resources/*/*.html", "../resources/web.js", "*.go", "../process/*.go"} {
		files, _ := filepath.Glob(pattern)
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") || strings.HasSuffix(file, "translations.go") {
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
	for lang, texts := range translations {
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
			if _, ok := translations["es"][match[1]]; !ok && !untranslated[match[1]] {
				t.Errorf("%s: no Spanish translation for %q", filepath.Base(file), match[1])
			}
		}
	}
	for _, text := range jsTexts {
		if _, ok := translations["es"][text]; !ok {
			t.Errorf("web.js: no Spanish translation for %q", text)
		}
	}
}

func TestLanguageFromHeader(t *testing.T) {
	for header, want := range map[string]string{
		"":                        "en",
		"es-ES,es;q=0.9,en;q=0.8": "es",
		"en-US,en;q=0.9,es;q=0.8": "en",
		"de-DE,de;q=0.9,es;q=0.7": "es",
		"fr-FR,fr;q=0.9":          "en",
		"ES":                      "es",
	} {
		if got := languageFromHeader(header); got != want {
			t.Errorf("languageFromHeader(%q) = %q, want %q", header, got, want)
		}
	}
	if translatef("es", "%v DLC missing", 3) != "Faltan 3 DLC" || translate("xx", "Library") != "Library" {
		t.Fatal("unexpected translation")
	}
}
