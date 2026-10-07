package web

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestDiagnosticsShowProblemsWithoutSecrets(t *testing.T) {
	web := newTestWeb(t)
	settings.ReadSettings(web.dataFolder)
	library := t.TempDir()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Folder = library
		s.ScanFolders = []string{filepath.Join(library, "missing")}
		s.Notifications.TelegramBotToken = "123456:secret-token"
	})
	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)
	r := httptest.NewRequest("GET", "/diagnostics.html", nil)
	r.RemoteAddr = "192.168.1.10:1234"
	data := web.diagnostics(r, "en")
	if data.Problems == 0 {
		t.Fatal("the missing folder and keys are problems")
	}
	if !strings.Contains(data.Report, "ERROR  "+filepath.Join(library, "missing")+": Not found") {
		t.Fatalf("the missing folder is reported:\n%s", data.Report)
	}
	if strings.Contains(data.Report, "secret-token") {
		t.Fatal("the report must hold no secret")
	}
	if !writable(library) || writable(filepath.Join(library, "missing")) {
		t.Fatal("writable folders")
	}
	os.RemoveAll(library)
}
