package web

import (
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestIgnoreAFileTypeFromTheIssues(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	// ignoring a type scans the library again
	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	web.localDbManager = manager
	web.HandleIssues()
	issues := []Issue{{File: "/roms/a/1.sfx", Reason: "file type is not supported"}, {File: "/roms/a/2.SFX", Reason: "file type is not supported"},
		{File: "/roms/b.cue", Reason: "file type is not supported"}, {File: "/roms/c.nsp", Reason: "base file is missing"}}
	types := unsupportedTypes(issues, 5)
	if len(types) != 2 || types[0].Name != ".sfx" || types[0].Count != 2 {
		t.Fatalf("the types of the files that are not games, most files first: %+v", types)
	}

	original := settings.ReadSettings(web.dataFolder).IgnoreFileTypes
	defer settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.IgnoreFileTypes = original })
	for _, bad := range []string{".nsp", "XCI", "", "a/b", "..", "a b"} {
		if response := postForm(web, "/issues/ignore-type", url.Values{"type": {bad}}); response.Code != http.StatusBadRequest {
			t.Errorf("%q must be refused: %d", bad, response.Code)
		}
	}
	if response := postForm(web, "/issues/ignore-type", url.Values{"type": {"SFX"}}); response.Code != http.StatusAccepted {
		t.Fatalf("ignore: %d", response.Code)
	}
	postForm(web, "/issues/ignore-type", url.Values{"type": {".sfx"}})
	// the scan that follows ends before the test does
	for deadline := 0; web.state.IsSynchronizing() && deadline < 200; deadline++ {
		waitShort()
	}
	count := 0
	for _, known := range settings.ReadSettings(web.dataFolder).IgnoreFileTypes {
		if known == "sfx" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("a type is ignored once: %v", settings.ReadSettings(web.dataFolder).IgnoreFileTypes)
	}
}
