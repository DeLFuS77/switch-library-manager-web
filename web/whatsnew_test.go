package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A new installation tells nothing; an update tells once what is new, in the language of the
// interface, and in English for a language without highlights.
func TestWhatsNewAfterAnUpdate(t *testing.T) {
	web := newTestWeb(t)
	highlights, _ := json.Marshal(map[string]map[string][]string{settings.SLM_WEB_VERSION: {"en": {"New thing."}, "es": {"Cosa nueva."}}})
	web.embedFS = fstest.MapFS{"resources/whatsnew.json": {Data: highlights}}

	web.recordVersion()
	if notice := web.whatsNew("es"); notice != nil {
		t.Fatalf("a new installation: %+v", notice)
	}

	record, _ := json.Marshal(versionRecord{Version: "0.9.0"})
	os.WriteFile(filepath.Join(web.dataFolder, VERSION_FILENAME), record, 0600)
	web.recordVersion()
	notice := web.whatsNew("es")
	if notice == nil || notice.Version != settings.SLM_WEB_VERSION || !reflect.DeepEqual(notice.Items, []string{"Cosa nueva."}) {
		t.Fatalf("after an update: %+v", notice)
	}
	if notice := web.whatsNew("de"); notice == nil || notice.Items[0] != "New thing." {
		t.Fatalf("another language: %+v", notice)
	}
	// restarted with the same version: still the same notice, until it is closed in the page
	web.recordVersion()
	if web.whatsNew("es") == nil {
		t.Fatal("a restart forgets the update")
	}
}

// An installation from before the version was kept, with a library, counts as updated; a
// version without highlights tells only that the app was updated.
func TestWhatsNewForAnOldInstallation(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = fstest.MapFS{"resources/whatsnew.json": {Data: []byte("{}")}}
	os.WriteFile(filepath.Join(web.dataFolder, "slm.db"), nil, 0600)
	web.recordVersion()
	notice := web.whatsNew("en")
	if notice == nil || len(notice.Items) != 0 || notice.Url == "" {
		t.Fatalf("without highlights: %+v", notice)
	}
}
