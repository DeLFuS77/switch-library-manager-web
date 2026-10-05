package web

import (
	"net/http"
	"net/http/httptest"
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

func TestDemoTitlePagesExist(t *testing.T) {
	web := newTestWeb(t)
	web.loadDemo()
	if _, ok := web.getTitleDetail(demoTitleId(0), "en"); !ok {
		t.Fatal("the title page of a demo game must exist")
	}
}
