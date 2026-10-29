package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

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

// A game the measure cannot compress, however big, is passed over for the next ones.
func TestMeasurableGames(t *testing.T) {
	_, game := compressWeb(t)
	broken := filepath.Join(filepath.Dir(game), "Broken [0100000000020000][v0].nsp")
	if err := os.WriteFile(broken, bytes.Repeat([]byte("not a game "), 1<<16), 0o644); err != nil {
		t.Fatal(err)
	}
	games := []CompressCandidate{{Path: broken, Size: 1 << 20}, {Path: game, Size: 1 << 10}}
	got := measurableGames(context.Background(), games, switchfs.LevelBalanced, maxFilesAtOnce)
	if len(got) != 1 || got[0].Path != game {
		t.Fatalf("the games measured: %+v", got)
	}
	if len(measurableGames(context.Background(), games[:1], switchfs.LevelBalanced, maxFilesAtOnce)) != 0 {
		t.Fatal("a broken game alone is measured")
	}
}
