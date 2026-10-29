package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestServiceWorkerIsServedWithTheVersion(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleResources()

	recorder := httptest.NewRecorder()
	http.DefaultServeMux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, "slm-static-"+settings.SLM_WEB_VERSION) || strings.Contains(body, "__VERSION__") {
		t.Fatalf("got %d %q", recorder.Code, body[:min(len(body), 200)])
	}
	if !isPublicPath("/sw.js") {
		t.Fatal("the service worker must load without a login")
	}
}

func TestManifestIsInstallable(t *testing.T) {
	data, err := os.ReadFile("../resources/static/site.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	manifest := struct {
		Name     string `json:"name"`
		StartUrl string `json:"start_url"`
		Display  string `json:"display"`
		Icons    []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}{}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name == "" || manifest.StartUrl == "" || manifest.Display != "standalone" {
		t.Fatalf("manifest %+v", manifest)
	}
	sizes := map[string]bool{}
	for _, icon := range manifest.Icons {
		if _, err := os.Stat("../" + strings.TrimPrefix(icon.Src, "/")); err != nil {
			t.Errorf("missing icon %s", icon.Src)
		}
		sizes[icon.Sizes+" "+icon.Purpose] = true
	}
	if !sizes["192x192 any"] || !sizes["512x512 any"] || !sizes["512x512 maskable"] {
		t.Fatalf("icons %v", sizes)
	}
}

// The page shown when the server cannot be reached speaks every language of the interface, and
// the service worker keeps the styles, the script and the picture it needs with it.
func TestOfflinePageWorksOffline(t *testing.T) {
	page, err := os.ReadFile("../resources/static/offline.html")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := os.ReadFile("../resources/static/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range supportedLanguages {
		if lang != DEFAULT_LANGUAGE && !strings.Contains(string(page), `lang="`+lang+`"`) {
			t.Errorf("the offline page has no text in %s", lang)
		}
	}
	kept := regexp.MustCompile(`OFFLINE_FILES = \[([^\]]*)\]`).FindStringSubmatch(string(worker))
	if kept == nil {
		t.Fatal("the service worker keeps no files for the offline page")
	}
	for _, match := range regexp.MustCompile(`(?:<link rel="stylesheet" href|<script src|<img src)="([^"]+)"`).FindAllStringSubmatch(string(page), -1) {
		if !strings.Contains(kept[1], "'"+match[1]+"'") {
			t.Errorf("the service worker does not keep %s for the offline page", match[1])
		}
	}
}
