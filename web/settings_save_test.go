package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A form whose settings cannot be saved says so, as an error: it said "Saved", the settings in
// memory changed, and the change was gone at the next start.
func TestFormSaysWhenTheSettingsCannotBeSaved(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleOrganize()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.OrganizeOptions.RenameFiles = false })
	// a folder where the file is written aside: it cannot be
	if err := os.MkdirAll(filepath.Join(web.dataFolder, settings.SETTINGS_FILENAME+".tmp", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"rename_files": {"true"}, "file_name_template": {"{TITLE_NAME} [{TITLE_ID}]"}, "folder_name_template": {"{TITLE_NAME}"}}
	response := postForm(web, "/organize.html", form)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "The settings could not be saved") {
		t.Fatalf("an error: %d %s", response.Code, response.Body.String())
	}
	if settings.ReadSettings(web.dataFolder).OrganizeOptions.RenameFiles {
		t.Fatal("the settings stay as they were")
	}

	os.RemoveAll(filepath.Join(web.dataFolder, settings.SETTINGS_FILENAME+".tmp"))
	if response := postForm(web, "/organize.html", form); response.Code != http.StatusOK || !settings.ReadSettings(web.dataFolder).OrganizeOptions.RenameFiles {
		t.Fatalf("saved once it can be: %d %s", response.Code, response.Body.String())
	}
}
