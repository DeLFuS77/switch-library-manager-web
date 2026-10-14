package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

	// not while a task changes the files
	web.organizing.Store(true)
	busy := httptest.NewRecorder()
	web.router.ServeHTTP(busy, httptest.NewRequest(http.MethodPost, "/settings/measure", nil))
	web.organizing.Store(false)
	if busy.Code != http.StatusConflict {
		t.Fatalf("a measure while a task works on the files: %d", busy.Code)
	}
}
