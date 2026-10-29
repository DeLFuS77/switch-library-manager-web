package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDemoLibrary(t *testing.T) {
	web := newTestWeb(t)
	web.loadDemo()
	switchDB, localDB := web.state.get()
	if len(localDB.TitlesMap) == 0 || len(switchDB.TitlesMap) <= len(localDB.TitlesMap) {
		t.Fatalf("%d games in the library, %d in the catalog", len(localDB.TitlesMap), len(switchDB.TitlesMap))
	}
	for key, title := range switchDB.TitlesMap {
		if len(title.Attributes.Id) != 16 || !strings.EqualFold(title.Attributes.Id[:13], key) {
			t.Errorf("title ID %q under %q", title.Attributes.Id, key)
		}
		for id := range title.Dlc {
			if len(id) != 16 || !strings.EqualFold(id[:12], key[:12]) {
				t.Errorf("DLC ID %q of %q", id, key)
			}
		}
	}
	if len(web.missingUpdates()) == 0 || len(web.missingDLC()) == 0 {
		t.Fatal("the demo shows missing updates and DLC")
	}
	if _, err := os.Stat(filepath.Join(web.dataFolder, "img", "demo-"+demoTitleId(0)+".png")); err != nil {
		t.Fatal("covers are written")
	}
}

func TestDemoRefusesChanges(t *testing.T) {
	web := newTestWeb(t)
	handler := web.demoReadOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/sync", nil))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), DEMO_DISABLED) {
		t.Fatalf("got %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if recorder.Code != http.StatusAccepted {
		t.Fatal("pages can be read")
	}
}

// The demo shows nothing of the server it runs on: not the diagnostics, not its folders, and the
// checks of the wizard answer without looking at it, so the wizard can still be gone through.
func TestDemoHidesTheServer(t *testing.T) {
	t.Setenv("SLM_DEMO", "true")
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleWizard()
	web.router.HandleFunc("/diagnostics.html", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(web.dataFolder)) })
	handler := web.demoReadOnly(web.router)
	serve := func(method string, path string, form url.Values) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	folder := t.TempDir()
	os.WriteFile(filepath.Join(folder, "a.nsp"), []byte("x"), 0o644)
	if got := serve(http.MethodGet, "/diagnostics.html", nil); got.Code != http.StatusForbidden || strings.Contains(got.Body.String(), web.dataFolder) || !strings.Contains(got.Body.String(), DEMO_HIDDEN) {
		t.Fatalf("the diagnostics: %d %s", got.Code, got.Body.String())
	}
	if got := serve(http.MethodGet, "/wizard/folders?path="+url.QueryEscape(folder), nil); got.Code != http.StatusOK || strings.Contains(got.Body.String(), "a.nsp") || !strings.Contains(got.Body.String(), DEMO_HIDDEN) || strings.Contains(got.Body.String(), `"places":["`) {
		t.Fatalf("the folders of the server: %d %s", got.Code, got.Body.String())
	}
	for _, kind := range []string{"folder", "keys"} {
		got := serve(http.MethodPost, "/wizard/check", url.Values{"kind": {kind}, "value": {folder}})
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"ok":true`) || !strings.Contains(got.Body.String(), DEMO_HIDDEN) || strings.Contains(got.Body.String(), `"games":1`) {
			t.Fatalf("the check of the %s: %d %s", kind, got.Code, got.Body.String())
		}
	}
	if got := serve(http.MethodPost, "/sync", url.Values{}); got.Code != http.StatusForbidden {
		t.Fatalf("changes stay refused: %d", got.Code)
	}
}

func TestDemoTitlePagesExist(t *testing.T) {
	web := newTestWeb(t)
	web.loadDemo()
	if _, ok := web.getTitleDetail(demoTitleId(0), "en"); !ok {
		t.Fatal("the title page of a demo game must exist")
	}
}
