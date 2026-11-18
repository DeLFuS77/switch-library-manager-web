package web

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// unreadableWeb is a library with a file that needs newer keys and a file cut short, or with
// none when clean.
func unreadableWeb(t *testing.T, clean bool) *Web {
	t.Helper()
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	web.localDbManager = manager
	dir := t.TempDir()
	library := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}
	if !clean {
		newer := db.ExtendedFileInfo{FileName: "Newer.nsp", BaseFolder: dir, Size: 100}
		cut := db.ExtendedFileInfo{FileName: "Cut.nsp", BaseFolder: dir, Size: 50}
		for _, file := range []db.ExtendedFileInfo{newer, cut} {
			os.WriteFile(filepath.Join(dir, file.FileName), []byte("x"), 0o644)
		}
		library.Skipped[newer] = db.SkippedFile{ReasonText: "failed to read Newer.nsp: prod.keys has no key_area_key_application_14. The title needs keys from a newer firmware: update prod.keys, or add [TitleID][vVersion] to the file name"}
		library.Skipped[cut] = db.SkippedFile{ReasonText: "failed to read Cut.nsp: unexpected EOF", CutShort: true}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, library)
	web.HandleOrganize()
	t.Cleanup(func() {
		for deadline := 0; web.state.IsSynchronizing() && deadline < 200; deadline++ {
			waitShort()
		}
	})
	return web
}

func TestOrganizeTellsTheFilesItCannotRead(t *testing.T) {
	web := unreadableWeb(t, false)
	counts := web.unreadableCounts()
	if len(counts) != 2 || counts[0].Kind != ISSUE_KEYS || counts[0].Count != 1 || counts[1].Kind != ISSUE_TRUNCATED || counts[1].Count != 1 {
		t.Fatalf("the keys first, then the file cut short: %+v", counts)
	}

	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/organize.html", nil))
	body := page.Body.String()
	upload := `href="/settings.html#library">Upload newer keys<`
	for _, want := range []string{"organize-unreadable", "/issues.html?kind_of_issue=keys", "/issues.html?kind_of_issue=truncated", upload} {
		if !strings.Contains(body, want) {
			t.Fatalf("the notice has %q: %d", want, page.Code)
		}
	}
	if strings.Count(body, upload) != 1 {
		t.Fatalf("only the keys too old have the link to upload newer ones")
	}
}

func TestOrganizeWithoutFilesItCannotRead(t *testing.T) {
	web := unreadableWeb(t, true)
	if counts := web.unreadableCounts(); len(counts) != 0 {
		t.Fatalf("no notice: %+v", counts)
	}
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/organize.html", nil))
	if body := page.Body.String(); strings.Contains(body, "organize-unreadable") || page.Code != 200 {
		t.Fatalf("the page without the notice: %d", page.Code)
	}
}
