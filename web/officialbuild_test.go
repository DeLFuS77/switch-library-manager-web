package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Every page, the diagnostics too, is told whether the program is an official build: built with
// the tag "official", which only the workflows of this repository use (TRADEMARKS.md).
func TestPagesAreToldTheOfficialBuild(t *testing.T) {
	web := newTestWeb(t)
	for _, page := range []string{"index", "diagnostics"} {
		if got := web.globalPageData(page).OfficialBuild; got != settings.OFFICIAL_BUILD {
			t.Errorf("%s: OfficialBuild %v, the program %v", page, got, settings.OFFICIAL_BUILD)
		}
	}
}

const (
	licenseLink = `href="https://github.com/DeLFuS77/switch-library-manager-web/blob/master/LICENSE"`
	chipOfBuild = `<span class="unofficial-build" data-hover-help><span class="chip chip-warning help-name">`
)

// The Disclaimer page starts with who makes the app, its license and that it has no warranty, for
// every user, with links to the license and the code; the footer of every page shows the license.
// Only a program built without the tag "official" says in the footer that it is an unofficial
// build, with its ⓘ (TRADEMARKS.md). In the official job of the workflow: go test -tags official.
func TestOfficialBuildNoticeInTheFooterAndTheDisclaimer(t *testing.T) {
	web := newTestWeb(t)
	set, err := parseTemplates(os.DirFS(".."), "resources/layout.html", "resources/pages/disclaimer.html")
	if err != nil {
		t.Fatal(err)
	}
	render := func(lang string) string {
		t.Helper()
		var page strings.Builder
		if err := set.execute(&page, lang, web.globalPageData("disclaimer")); err != nil {
			t.Fatal(err)
		}
		return page.String()
	}
	out := render("en")
	notice, disclaimer := strings.Index(out, `class="surface code-license`), strings.Index(out, `class="surface disclaimer"`)
	if notice < 0 || disclaimer < notice {
		t.Fatalf("Code and license is not at the top of the Disclaimer page: %d, %d", notice, disclaimer)
	}
	for _, want := range []string{">Code and license</h2>",
		"Switch Library Manager Web © 2026 DeLFuS77, based on the work of giwty, dtrunk90 and trembon. Free software under the AGPL-3.0, with no warranty. A changed version must say it is one.",
		licenseLink + `><span class="bi bi-file-earmark-text me-1" aria-hidden="true"></span>See the license</a>`,
		`href="https://github.com/DeLFuS77/switch-library-manager-web"><span class="bi bi-github me-1" aria-hidden="true"></span>See the code</a>`} {
		if !strings.Contains(out[notice:disclaimer], want) {
			t.Errorf("Code and license misses %q", want)
		}
	}
	footer := out[strings.Index(out, `<footer`):]
	if !strings.Contains(footer, licenseLink+` title="See the license">AGPL-3.0</a>`) {
		t.Error("the footer does not show the license")
	}
	if got := strings.Contains(footer, chipOfBuild+"Unofficial build</span>"); got == settings.OFFICIAL_BUILD {
		t.Errorf("the footer says it is an unofficial build: %v, an official build: %v", got, settings.OFFICIAL_BUILD)
	}
	if !settings.OFFICIAL_BUILD && !strings.Contains(footer, `<span class="form-text">This program was not built by the workflow of the repository`) {
		t.Error("the chip of an unofficial build has no help")
	}
	// in another language, with the name of the app and the license as they are
	es := render("es")
	for _, want := range []string{">Código y licencia</h2>", "Switch Library Manager Web © 2026 DeLFuS77, basado en el trabajo de giwty, dtrunk90 y trembon.", ">Ver la licencia</a>", ">AGPL-3.0</a>"} {
		if !strings.Contains(es, want) {
			t.Errorf("in Spanish, the page misses %q", want)
		}
	}
	if got := strings.Contains(es, chipOfBuild+"Compilación no oficial</span>"); got == settings.OFFICIAL_BUILD {
		t.Errorf("in Spanish, the footer says it is an unofficial build: %v, an official build: %v", got, settings.OFFICIAL_BUILD)
	}
}

// Diagnostics says it too: the first line of its report, which is pasted when asking for help, and
// its chip beside the state of the checks.
func TestOfficialBuildInDiagnostics(t *testing.T) {
	web := newTestWeb(t)
	data := web.diagnostics(httptest.NewRequest(http.MethodGet, "/diagnostics.html", nil), "en")
	first, _, _ := strings.Cut(data.Report, "\n")
	if got := strings.HasPrefix(first, "Switch Library Manager Web "+settings.SLM_WEB_VERSION+" · unofficial build — "); got == settings.OFFICIAL_BUILD {
		t.Errorf("the report says it is an unofficial build: %q, an official build: %v", first, settings.OFFICIAL_BUILD)
	}
	if settings.OFFICIAL_BUILD && !strings.HasPrefix(first, "Switch Library Manager Web "+settings.SLM_WEB_VERSION+" — ") {
		t.Errorf("the first line of the report: %q", first)
	}
	set, err := parseTemplates(os.DirFS(".."), "resources/layout.html", "resources/pages/diagnostics.html")
	if err != nil {
		t.Fatal(err)
	}
	data.GlobalPageData = web.globalPageData("diagnostics")
	var page strings.Builder
	if err := set.execute(&page, "en", data); err != nil {
		t.Fatal(err)
	}
	out := page.String()
	body := out[:strings.Index(out, `<footer`)]
	if got := strings.Contains(body, chipOfBuild+"Unofficial build</span>"); got == settings.OFFICIAL_BUILD {
		t.Errorf("Diagnostics says it is an unofficial build: %v, an official build: %v", got, settings.OFFICIAL_BUILD)
	}
}
