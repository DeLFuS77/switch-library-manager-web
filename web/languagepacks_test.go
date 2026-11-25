package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

const (
	packFrench   = "0100000000011003"
	packGerman   = "0100000000011004"
	packSpanish  = "0100000000011005"
	packUnknown  = "0100000000011006"
	packItalian  = "0100000000011007"
	packInBundle = "0100000000011008"
)

// languagePacksWeb has a game with language packs in the library: French and German, one file
// each; Spanish, the language chosen; one whose language is not known; and an Italian one in the
// same file as another DLC.
func languagePacksWeb(t *testing.T) (*Web, map[string]string) {
	t.Helper()
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	switchDB, localDB := testDatabases(t)
	folder := t.TempDir()
	paths := map[string]string{}
	add := func(id string, name string, file string) {
		path := filepath.Join(folder, file)
		os.WriteFile(path, []byte("pack"), 0o644)
		paths[id] = path
		switchDB.TitlesMap["0100000000010"].Dlc[id] = db.TitleAttributes{Id: id, Name: name}
		localDB.TitlesMap["0100000000010"].Dlc[id] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: file, BaseFolder: folder, Size: 4},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: id}}
	}
	add(packFrench, "Game: French Language Pack", "French ["+packFrench+"].nsp")
	add(packGerman, "Game Language Pack (DE)", "German ["+packGerman+"].nsp")
	add(packSpanish, "Game In-Game Commentary - Spanish", "Spanish ["+packSpanish+"].nsp")
	add(packUnknown, "Game: Multi Language Pack", "Multi ["+packUnknown+"].nsp")
	add(packItalian, "Game: Italian Language Pack", "Bundle.nsp")
	add(packInBundle, "Game: Costume", "Bundle.nsp")
	web.state.set(switchDB, localDB)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.DLCLanguages = []string{"es", "en"} })
	web.invalidateDerived()
	web.HandleSpace()
	return web, paths
}

func languageGroup(web *Web) SpaceGroup {
	for _, group := range web.spaceGroups() {
		if group.Id == SPACE_LANGUAGES {
			return group
		}
	}
	return SpaceGroup{}
}

func languageFile(group SpaceGroup, path string) (SpaceFile, bool) {
	for _, file := range group.Files {
		if file.Path == path {
			return file, true
		}
	}
	return SpaceFile{}, false
}

// The language packs of the library in the languages not chosen are offered on Space; the ones
// whose language is not known, and a file that holds something else too, stay (#200).
func TestLanguagePacksOfferedOnSpace(t *testing.T) {
	web, paths := languagePacksWeb(t)
	group := languageGroup(web)
	for id, want := range map[string]bool{packFrench: true, packGerman: true} {
		file, ok := languageFile(group, paths[id])
		if !ok || file.Deletable != want || !file.KeepableDLC || file.DlcId != id {
			t.Errorf("%s: %+v", id, file)
		}
	}
	if file, _ := languageFile(group, paths[packFrench]); file.Language != "French" {
		t.Errorf("language %q", file.Language)
	}
	if _, ok := languageFile(group, paths[packSpanish]); ok {
		t.Error("a pack in a language chosen is not offered")
	}
	if file, ok := languageFile(group, paths[packUnknown]); !ok || file.Deletable || file.Note != NOTE_LANGUAGE_UNKNOWN || file.KeepableDLC {
		t.Errorf("a pack whose language is not known stays: %+v", file)
	}
	if file, ok := languageFile(group, paths[packItalian]); !ok || file.Deletable || file.Note != process.REASON_IN_USE {
		t.Errorf("a file with another DLC in it stays: %+v", file)
	}
	for _, file := range group.Files {
		if strings.Contains(file.Name, "Owned DLC") || strings.Contains(file.Name, "Costume") {
			t.Errorf("a DLC that is not a language pack is never offered: %+v", file)
		}
	}
	if count, size := web.languagePacksSummary(); count != 2 || size != 8 {
		t.Errorf("Settings: %d packs, %d bytes", count, size)
	}

	// none chosen: nothing is offered
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.DLCLanguages = nil })
	web.invalidateDerived()
	if group := languageGroup(web); len(group.Files) != 0 {
		t.Fatalf("no language chosen: %+v", group.Files)
	}
}

// Keep this DLC keeps a language pack: Space no longer deletes it, until Stop keeping.
func TestKeepThisDLC(t *testing.T) {
	web, paths := languagePacksWeb(t)
	if code := postForm(web, "/dlc/keep", url.Values{"id": {packGerman}}).Code; code != http.StatusOK {
		t.Fatalf("keep: %d", code)
	}
	if file, _ := languageFile(languageGroup(web), paths[packGerman]); file.Deletable || !file.KeptDLC || file.Note != NOTE_DLC_KEPT {
		t.Fatalf("kept: %+v", file)
	}
	// a kept file is not deleted, even when the request names it
	if code := postForm(web, "/space/clean", url.Values{"path": {paths[packGerman]}}).Code; code != http.StatusBadRequest {
		t.Fatalf("clean a kept pack: %d", code)
	}
	if _, err := os.Stat(paths[packGerman]); err != nil {
		t.Fatal("the kept pack was deleted")
	}
	if code := postForm(web, "/dlc/keep", url.Values{"id": {packGerman}, "keep": {"false"}}).Code; code != http.StatusOK {
		t.Fatalf("stop keeping: %d", code)
	}
	if file, _ := languageFile(languageGroup(web), paths[packGerman]); !file.Deletable || file.KeptDLC {
		t.Fatalf("let go: %+v", file)
	}
	for _, id := range []string{"0100000000099001", "not an id"} {
		if code := postForm(web, "/dlc/keep", url.Values{"id": {id}}).Code; code != http.StatusBadRequest {
			t.Errorf("%q: %d", id, code)
		}
	}
	keep := httptest.NewRequest(http.MethodPost, "/dlc/keep", nil)
	if roleAllows(ROLE_VIEWER, keep) || roleAllows(ROLE_EDITOR, keep) {
		t.Fatal("only an administrator keeps a DLC")
	}
}

// Only the packs selected are deleted, and no cleanup of Organize or of the automations takes them.
func TestLanguagePacksDeletedWhenSelected(t *testing.T) {
	web, paths := languagePacksWeb(t)
	_, localDB := web.state.get()
	for _, op := range process.DeleteOldUpdates(t.TempDir(), localDB, settings.ReadSettings(web.dataFolder).OrganizeOptions, true, nil) {
		if op.From == paths[packFrench] || op.From == paths[packGerman] {
			t.Fatalf("the cleanup takes a language pack: %+v", op)
		}
	}
	if code := postForm(web, "/space/clean", url.Values{"path": {paths[packFrench]}}).Code; code != http.StatusOK {
		t.Fatalf("clean: %d", code)
	}
	if _, err := os.Stat(paths[packFrench]); !os.IsNotExist(err) {
		t.Error("the pack selected was not deleted")
	}
	for _, id := range []string{packGerman, packUnknown, packItalian, packSpanish} {
		if _, err := os.Stat(paths[id]); err != nil {
			t.Errorf("%s was deleted without being selected", id)
		}
	}
	if quarantineReason(SPACE_LANGUAGES) != QUARANTINE_LANGUAGE {
		t.Error("quarantine reason")
	}
}

// The page lists the group with none of its files selected.
func TestLanguagePacksNotSelectedOnThePage(t *testing.T) {
	web, paths := languagePacksWeb(t)
	web.spaceContent()
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/space.html", nil))
	page := recorder.Body.String()
	if !strings.Contains(page, `id="space-languages"`) || !strings.Contains(page, "data-dlc-keep=\""+packFrench+"\"") {
		t.Fatalf("the group is not on the page")
	}
	for _, line := range strings.Split(page, "\n") {
		if strings.Contains(line, `value="`+paths[packFrench]+`"`) && (strings.Contains(line, " checked") || !strings.Contains(line, "data-by-hand")) {
			t.Fatalf("a language pack is selected at first: %s", line)
		}
	}
}

func TestLanguagePackTextsAreTranslated(t *testing.T) {
	for _, text := range []string{"Language packs you do not use", "Language pack you do not use",
		"Language packs and commentary in the languages you did not tick in Settings, What is shown. None is selected: select the ones to delete.",
		"Select all of this group", "Keep this DLC", NOTE_DLC_KEPT, NOTE_LANGUAGE_UNKNOWN,
		"You have %v language packs in other languages (%v).", "See them in Space"} {
		for _, lang := range i18n.SupportedLanguages {
			if _, ok := i18n.Translations[lang][text]; !ok && lang != i18n.DEFAULT_LANGUAGE {
				t.Errorf("no %s translation for %q", lang, text)
			}
		}
	}
}
