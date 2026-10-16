package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The tune-up shows its five steps in order, each with its state, and marks the first one
// left to do.
func TestMaintenancePage(t *testing.T) {
	web, _ := spaceWeb(t)
	web.HandleMaintenance()
	steps := web.maintenanceSteps("en")
	if len(steps) != 5 || steps[0].Id != STEP_SYNC || steps[4].Id != STEP_COMPRESS {
		t.Fatalf("the steps in order: %+v", steps)
	}
	next := 0
	for _, step := range steps {
		if step.Detail == "" || step.Status == "" {
			t.Fatalf("every step tells its state: %+v", step)
		}
		if step.Next {
			next++
			if step.Status != STEP_TODO {
				t.Fatalf("the next step is one left to do: %+v", step)
			}
		}
	}
	if next > 1 {
		t.Fatal("one next step at most")
	}
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/maintenance.html", nil))
	if recorder.Code != http.StatusOK || strings.Count(recorder.Body.String(), "maintenance-step ") != 5 {
		t.Fatalf("the page: %d", recorder.Code)
	}
	if maintenanceStepOf("space") != "4" || maintenanceStepOf("index") != "" {
		t.Fatal("the step of a tool page")
	}
	// the count of the changes of Organize, prepared in the background, ends before the test
	for deadline := time.Now().Add(10 * time.Second); web.organizePreparing.Load() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
}

// The menu marks the tune-up while a step is waiting, here files never checked.
func TestTuneUpDot(t *testing.T) {
	web, _ := spaceWeb(t)
	if !web.navCounts().TuneUp {
		t.Fatal("files never checked: the tune-up is waiting")
	}
	for _, file := range web.libraryFiles(".nsp", ".nsz", ".xci", ".xcz") {
		web.verifications().set(file.Path, verifyRecord{OK: true, Checker: verifyChecker})
	}
	if unchecked, _ := web.uncheckedFiles(); unchecked != 0 {
		t.Fatalf("every file checked: %d left", unchecked)
	}
}
