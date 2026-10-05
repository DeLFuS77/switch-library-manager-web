package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestNewerVersion(t *testing.T) {
	for _, tt := range []struct {
		version, current string
		newer            bool
	}{
		{"1.12.0", "1.11.0", true},
		{"1.10.0", "1.9.0", true},
		{"v2.0.0", "1.99.9", true},
		{"1.11.0", "1.11.0", false},
		{"1.9.0", "1.10.0", false},
	} {
		if got := newerVersion(tt.version, tt.current); got != tt.newer {
			t.Errorf("newerVersion(%q, %q) = %v", tt.version, tt.current, got)
		}
	}
}

func TestUpdateNotice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "switch-library-manager-web/") {
			t.Error("GitHub needs a user agent")
		}
		w.Write([]byte(`{"tag_name": "99.0.0", "html_url": "https://github.com/x/y/releases/tag/99.0.0", "body": "news"}`))
	}))
	defer server.Close()
	previous := latestReleaseUrl
	latestReleaseUrl = server.URL
	defer func() { latestReleaseUrl = previous }()

	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.checkForUpdate()
	update := web.availableUpdate()
	if update == nil || update.Version != "99.0.0" || update.Notes != "news" {
		t.Fatalf("update: %+v", update)
	}

	web.HandleStatistics()
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/statistics.html", nil))
	if !strings.Contains(page.Body.String(), `data-update-version="99.0.0"`) {
		t.Fatal("administrators see the notice")
	}

	original := settings.ReadSettings(web.dataFolder).CheckForUpdates
	defer settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CheckForUpdates = original })
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CheckForUpdates = false })
	if web.availableUpdate() != nil {
		t.Fatal("the notice can be turned off")
	}
}

func TestOlderReleaseIsNoUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name": "1.0.0", "html_url": "https://evil.example/"}`))
	}))
	defer server.Close()
	previous := latestReleaseUrl
	latestReleaseUrl = server.URL
	defer func() { latestReleaseUrl = previous }()
	web := newTestWeb(t)
	web.checkForUpdate()
	if web.availableUpdate() != nil {
		t.Fatal("an older release is no update")
	}
}
