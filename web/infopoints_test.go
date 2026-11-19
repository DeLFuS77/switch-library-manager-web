package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The long explanations of the pages are in the ⓘ of their titles: the introduction of every
// section of Settings, what each action of Organize and each tab of Compress does, and the
// regions, the cheats and the versions of the page of a game. Warnings, statuses and safety notes
// stay in sight.
func TestInfoPoints(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join("..", "resources", "pages", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	settings := read("settings.html")
	for _, section := range []string{"compression", "automation", "vault", "netinstall", "notifications", "quarantine", "mobile", "portal"} {
		intro := regexp.MustCompile(`<h2 class="section-title help-name" id="` + section + `Title">.*</h2>\s*<p class="form-text">`)
		if !intro.MatchString(settings) {
			t.Errorf("the introduction of %s in the ⓘ of its title", section)
		}
	}
	// what a console must do now, and the note of the password of JKSV, stay in sight
	for _, keep := range []string{`<p class="small text-body-secondary mb-2">{{t "Each console signs in with its own key`, `<div class="small text-body-secondary mb-2">{{t "Copy it into the installer now.`} {
		if !strings.Contains(settings, keep) {
			t.Errorf("in sight: %s", keep)
		}
	}
	organize := read("organize.html")
	if strings.Count(organize, `<h3 class="h6 fw-semibold mb-1 help-name">`) != 3 || !strings.Contains(organize, `<h2 class="section-title help-name" id="undoRunsTitle">`) {
		t.Error("what each action of Organize does, in its ⓘ")
	}
	// each of these is in a part of the page whose help goes into the ⓘ (on the page of a game, also
	// what installers and emulators take each file)
	for name, want := range map[string]int{"organize.html": 3, "compress.html": 7, "title.html": 9} {
		if got := strings.Count(read(name), "data-hover-help"); got != want {
			t.Errorf("%s: %d parts with the help in the ⓘ, not %d", name, got, want)
		}
	}
	if !strings.Contains(organize, `(t "Every action shows a preview first, nothing changes until you confirm it.")`) {
		t.Error("the preview first, in sight")
	}
	compress := read("compress.html")
	if strings.Count(compress, `class="section-title help-name"`) != 7 {
		t.Errorf("the levels and the six tabs of Compress in their ⓘ: %d", strings.Count(compress, `class="section-title help-name"`))
	}
	// the steps to install an optimized game and the keys note are warnings and safety: in sight
	for _, keep := range []string{`<ul class="small text-body-secondary mb-3 ps-3">`, `{{t "Compression uses your own prod.keys`} {
		if !strings.Contains(compress, keep) {
			t.Errorf("in sight: %s", keep)
		}
	}
	title := read("title.html")
	for _, help := range []string{`<p class="form-text">{{t "You have this game from several regions:`, `<p class="form-text">{{t "For the version you have (v%v, build %v).`, `<p class="form-text">{{t "The titles database only has the date of each update.`} {
		if !strings.Contains(title, help) {
			t.Errorf("in the ⓘ of its title: %s", help)
		}
	}
	if !strings.Contains(title, `<p class="text-body-secondary mb-0">{{t "There are cheats for other versions of the game`) {
		t.Error("the cheats for other versions, a status, in sight")
	}
}
