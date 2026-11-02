package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The folders to leave out are written in three lists, by the start, the end or a part of their
// names, one per line, and kept as the lines they are matched by; a path, which no folder name
// has, is refused.
func TestSettingsSaveTheFoldersToLeaveOut(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleSettings()
	current := settings.ReadSettings(web.dataFolder)
	form := url.Values{"scan_folders": {t.TempDir()}, "skip_starting": {" _ \r\n\r\n#temp/"}, "skip_ending": {"*_old\r\n(copy)"},
		"skip_containing": {"build\r\n*"}, "sync_interval_hours": {strconv.Itoa(current.SyncIntervalHours)},
		"quarantine_limit": {strconv.Itoa(current.Quarantine.LimitGB)}}
	response := postForm(web, "/settings.html", form)
	if response.Code != http.StatusOK {
		t.Fatalf("saved: %d %s", response.Code, response.Body.String())
	}
	saved := settings.ReadSettings(web.dataFolder)
	if got := saved.ExcludeFolders; !reflect.DeepEqual(got, []string{"_*", "#temp*", "*_old", "*(copy)", "*build*"}) {
		t.Fatalf("saved as %#v", got)
	}
	if !settings.ExcludedFolder(saved.ExcludeFolders, "_Tests") || !settings.ExcludedFolder(saved.ExcludeFolders, "Zelda (Copy)") ||
		settings.ExcludedFolder(saved.ExcludeFolders, "Zelda") {
		t.Fatal("the folders of the lists are skipped, and only those")
	}

	// the lists show what was saved
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/settings.html", nil))
	for _, want := range []string{"name=\"skip_starting\" rows=\"3\" spellcheck=\"false\" placeholder=\"e.g. _\">\n_\n#temp</textarea>",
		">\n_old\n(copy)</textarea>", ">\nbuild</textarea>"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("the settings page misses %q", want)
		}
	}

	form.Set("skip_ending", "_old\n/mnt/roms/cache")
	refused := postForm(web, "/settings.html", form)
	var answer ErrorResponse
	json.Unmarshal(refused.Body.Bytes(), &answer)
	if refused.Code != http.StatusBadRequest || len(answer.FieldErrors) != 1 || answer.FieldErrors[0].Field != "skip_ending" {
		t.Fatalf("a path: %d %s", refused.Code, refused.Body.String())
	}

	for _, field := range []string{"skip_starting", "skip_ending", "skip_containing"} {
		form.Del(field)
	}
	if response := postForm(web, "/settings.html", form); response.Code != http.StatusOK || len(settings.ReadSettings(web.dataFolder).ExcludeFolders) != 0 {
		t.Fatalf("emptied: %d %v", response.Code, settings.ReadSettings(web.dataFolder).ExcludeFolders)
	}
}
