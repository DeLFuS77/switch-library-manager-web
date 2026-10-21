package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The setting chosen is the fastest tried, unless a lighter one is almost as fast, and no
// trial takes more than three fourths of the processors.
func TestChooseTrial(t *testing.T) {
	trials := []MeasureTrial{{1, 1, 100}, {1, 2, 180}, {2, 1, 190}, {1, 4, 260}, {2, 2, 280}, {3, 1, 200}}
	if threads, files := chooseTrial(trials); threads != 4 || files != 1 {
		t.Fatalf("2x2 is not clearly faster than 1x4: %d threads, %d files", threads, files)
	}
	trials[4].Speed = 330
	if threads, files := chooseTrial(trials); threads != 2 || files != 2 {
		t.Fatalf("2x2 clearly faster: %d threads, %d files", threads, files)
	}
	for _, trial := range measureTrials(2) {
		if trial.Files*trial.Threads > 1 {
			t.Fatalf("a server of two processors: %+v", trial)
		}
	}
	if len(measureTrials(16)) != 7 {
		t.Fatal("every setting on a big server")
	}
}

func TestMeasureServer(t *testing.T) {
	web, _ := compressWeb(t)
	web.HandleMeasure()
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/settings/measure", nil))
	var measure ServerMeasure
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &measure) != nil {
		t.Fatalf("the measure: %d %s", recorder.Code, recorder.Body.String())
	}
	if len(measure.Results) == 0 || measure.Results[0].Speed <= 0 || measure.Threads < 1 || measure.FilesAtOnce < 1 || measure.FilesAtOnce > maxFilesAtOnce {
		t.Fatalf("what was measured: %+v", measure)
	}
	// both levels are measured with the setting chosen, which is saved at once and kept
	if measure.Level("balanced").Speed <= 0 || measure.Level("max").Speed <= 0 || measure.Level("max").MinutesPer10 < 1 || measure.Measured.IsZero() {
		t.Fatalf("the levels: %+v", measure.Levels)
	}
	saved := settings.ReadSettings(web.dataFolder)
	if saved.CompressThreads != measure.Threads || saved.CompressFilesAtOnce != measure.FilesAtOnce {
		t.Fatalf("not saved: %d threads, %d files", saved.CompressThreads, saved.CompressFilesAtOnce)
	}
	if last := web.lastMeasure(); last == nil || last.Best() != measure.Best() || last.Threads != measure.Threads {
		t.Fatalf("not kept: %+v", last)
	}
	// Settings shows the last measure, with what each level costs here
	web.HandleSettings()
	page := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/settings.html", nil)
	request.Header.Set("Accept-Language", "es")
	web.router.ServeHTTP(page, request)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Última medición") || !strings.Contains(page.Body.String(), "min por cada 10 GB") {
		t.Fatalf("the settings page: %d", page.Code)
	} // not while a task changes the files
	web.organizing.Store(true)
	busy := httptest.NewRecorder()
	web.router.ServeHTTP(busy, httptest.NewRequest(http.MethodPost, "/settings/measure", nil))
	web.organizing.Store(false)
	if busy.Code != http.StatusConflict {
		t.Fatalf("a measure while a task works on the files: %d", busy.Code)
	}
}

// The minutes for 10 GB of games at a speed, at least one.
func TestMinutesPer10GB(t *testing.T) {
	for speed, want := range map[int]int{0: 0, 25: 7, 250: 1, 9000: 1, 35: 5} {
		if got := minutesPer10GB(speed); got != want {
			t.Errorf("%d MB/s: %d min, want %d", speed, got, want)
		}
	}
}
