package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// Times every page with a library the size of a big real collection: the first request
// (caches empty) and the next ones. Run with SLM_PAGE_TIMES=1.
func TestPageTimesWithBigLibrary(t *testing.T) {
	if os.Getenv("SLM_PAGE_TIMES") == "" {
		t.Skip("set SLM_PAGE_TIMES=1")
	}
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	switchDB, localDB := largeDatabases(12000, 26000)
	for i := 0; i < 13600; i++ {
		localDB.Skipped[db.ExtendedFileInfo{FileName: fmt.Sprintf("Game %d [0100%09X000][v0](2).nsz", i, i), BaseFolder: "/roms", Size: 1 << 30}] =
			db.SkippedFile{ReasonCode: db.REASON_DUPLICATE, ReasonText: fmt.Sprintf("duplicate base file (/roms/Game %d [0100%09X000][v0].nsz)", i, i)}
	}
	web.state.set(switchDB, localDB)
	web.HandleIndex()
	web.HandleUpdates()
	web.HandleDLC()
	web.HandleMissing()
	web.HandleIssues()
	web.HandleStatistics()
	web.HandleTitle()
	web.HandleSpace()
	web.HandleCompress()

	pages := []string{"/index.html", "/updates.html", "/dlc.html", "/missing.html", "/statistics.html", "/issues.html",
		"/space.html", "/compress.html", "/title/0100000000005000.html", "/index.html?page=200", "/index.html?q=Game+77"}
	for _, page := range pages {
		times := []time.Duration{}
		size := 0
		for run := 0; run < 3; run++ {
			recorder := httptest.NewRecorder()
			start := time.Now()
			web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, page, nil))
			times = append(times, time.Since(start))
			if recorder.Code != http.StatusOK {
				t.Fatalf("%s: %d", page, recorder.Code)
			}
			size = recorder.Body.Len()
		}
		t.Logf("%-32s first %8v  then %8v  %7d KB", page, times[0].Round(time.Millisecond), times[2].Round(time.Millisecond), size/1024)
	}
}
