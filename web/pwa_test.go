package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
