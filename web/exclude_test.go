package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The folders to leave out are saved one per line, as they are matched; a path, which no folder
// name matches, is refused.
func TestSettingsSaveTheFoldersToLeaveOut(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleSettings()
	current := settings.ReadSettings(web.dataFolder)
	form := url.Values{"scan_folders": {t.TempDir()}, "exclude_folders": {" _build \r\n\r\ncache/\r\n*_old*"},
		"sync_interval_hours": {strconv.Itoa(current.SyncIntervalHours)}, "quarantine_limit": {strconv.Itoa(current.Quarantine.LimitGB)}}
	response := postForm(web, "/settings.html", form)
	if response.Code != http.StatusOK {
		t.Fatalf("saved: %d %s", response.Code, response.Body.String())
	}
	if got := settings.ReadSettings(web.dataFolder).ExcludeFolders; !reflect.DeepEqual(got, []string{"_build", "cache", "*_old*"}) {
		t.Fatalf("saved as %#v", got)
	}

	form.Set("exclude_folders", "_build\n/mnt/roms/cache")
	refused := postForm(web, "/settings.html", form)
	var answer ErrorResponse
	json.Unmarshal(refused.Body.Bytes(), &answer)
	if refused.Code != http.StatusBadRequest || len(answer.FieldErrors) != 1 || answer.FieldErrors[0].Field != "exclude_folders" {
		t.Fatalf("a path: %d %s", refused.Code, refused.Body.String())
	}

	form.Del("exclude_folders")
	if response := postForm(web, "/settings.html", form); response.Code != http.StatusOK || len(settings.ReadSettings(web.dataFolder).ExcludeFolders) != 0 {
		t.Fatalf("emptied: %d %v", response.Code, settings.ReadSettings(web.dataFolder).ExcludeFolders)
	}
}
