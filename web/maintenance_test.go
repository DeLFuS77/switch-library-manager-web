package web

import (
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"net/http"
	"net/http/httptest"
	"os"
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
	page := recorder.Body.String()
	if recorder.Code != http.StatusOK || strings.Count(page, `<article class="tune-tile `) != 5 {
		t.Fatalf("the page: %d", recorder.Code)
	}
	// the step to do now on top, the reason of each step in the help of its name (on touch too),
	// and the other tools
	for _, want := range []string{`class="surface tune-hero`, `data-hover-help="touch"`, `class="tune-tile-title help-name"`, `href="/sd.html"`, "Other tools"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %s", want)
		}
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

// "Review again" scans the library, after which the steps are worked out again.
func TestMaintenanceReview(t *testing.T) {
	web, _ := spaceWeb(t)
	web.HandleMaintenance()
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/maintenance/review", nil))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("review: %d", recorder.Code)
	}
	for deadline := time.Now().Add(10 * time.Second); web.state.IsSynchronizing() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if task := lastTask(web.taskLog().Snapshot(), tasklog.TASK_SCAN); task == nil {
		t.Fatal("the library was scanned")
	}
}

// Each step tells what is left as a figure and opens the parts of its tool: the files never
// checked count, and Organize opens its actions and its options.
func TestMaintenanceFiguresAndShortcuts(t *testing.T) {
	web, _ := spaceWeb(t)
	steps := web.maintenanceSteps("en")
	verify := steps[1]
	if verify.Id != STEP_VERIFY || verify.Status != STEP_TODO || verify.Count <= 0 || verify.Figure == "" || verify.FigureLabel != "never checked" {
		t.Fatalf("the files never checked as a figure: %+v", verify)
	}
	if len(verify.Shortcuts) == 0 || verify.Shortcuts[0].Action != "verify" {
		t.Fatalf("the check of the new files from the card: %+v", verify.Shortcuts)
	}
	organize := steps[2]
	hrefs := []string{}
	for _, shortcut := range organize.Shortcuts {
		hrefs = append(hrefs, shortcut.Href)
	}
	for _, want := range []string{"/organize.html#new", "/organize.html#organize", "/organize.html#cleanup", "/organize.html#options"} {
		if !strings.Contains(strings.Join(hrefs, " "), want) {
			t.Errorf("Organize opens %s: %v", want, hrefs)
		}
	}
	for _, step := range steps {
		for _, shortcut := range step.Shortcuts {
			if shortcut.Label == "" || shortcut.Href == "" || shortcut.Icon == "" {
				t.Errorf("a shortcut with its name, icon and address: %+v", shortcut)
			}
		}
	}
	// the groups of Space as shortcuts, only the ones with files, each with what it frees
	shortcuts := spaceShortcuts(func(text string, args ...any) string { return text }, "en", SpacePageData{
		Groups:  []SpaceGroup{{Id: "old_updates", Files: []SpaceFile{{}}, DeletableSize: 2048}, {Id: "duplicates"}, {Id: "compressed", Files: []SpaceFile{{}}}},
		Regions: []RegionGroup{{}},
	})
	if len(shortcuts) != 3 || shortcuts[0].Href != "/space.html#old_updates" || shortcuts[0].Badge == "" || shortcuts[1].Label != "Compressed originals" || shortcuts[1].Badge != "" || shortcuts[2].Href != "/space.html#regions" {
		t.Fatalf("the shortcuts of Space: %+v", shortcuts)
	}
	for deadline := time.Now().Add(10 * time.Second); web.organizePreparing.Load() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
}

// The step to do now is the first one left; with all of them done the tune-up says so.
func TestMaintenanceNextStep(t *testing.T) {
	data := MaintenancePageData{Steps: []MaintenanceStep{{Id: STEP_SYNC, Status: STEP_DONE}, {Id: STEP_VERIFY, Status: STEP_TODO, Next: true}, {Id: STEP_ORGANIZE, Status: STEP_TODO}}}
	if next := data.NextStep(); next == nil || next.Id != STEP_VERIFY || data.AllDone() {
		t.Fatalf("the next step: %+v", next)
	}
	data.Steps[1].Status, data.Steps[1].Next, data.Steps[2].Status = STEP_DONE, false, STEP_DONE
	if data.NextStep() != nil || !data.AllDone() {
		t.Fatal("every step done")
	}
}

// A tool page that is a step shows the line of the five steps, its own marked; the others none.
func TestTuneUpCrumbs(t *testing.T) {
	links := tuneUpStepsOf("space")
	if len(links) != 5 || !links[3].Current || links[2].Current || links[2].Href != "/organize.html" {
		t.Fatalf("the steps of Space: %+v", links)
	}
	if tuneUpStepsOf("index") != nil {
		t.Fatal("no steps on a page that is not one")
	}
	set, err := parseTemplates(os.DirFS(".."), "resources/layout.html", "resources/partials/*.html", "resources/pages/disclaimer.html")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := set["en"].ExecuteTemplate(&out, "tuneUpCrumbs", "organize"); err != nil {
		t.Fatal(err)
	}
	crumbs := out.String()
	if strings.Count(crumbs, `class="tune-crumbs-link`) != 5 || !strings.Contains(crumbs, `href="/maintenance.html"`) || strings.Count(crumbs, `aria-current="step"`) != 1 || !strings.Contains(crumbs, "Step 3 of 5 of the tune-up") {
		t.Fatalf("the line of Organize: %s", crumbs)
	}
	out.Reset()
	if err := set["en"].ExecuteTemplate(&out, "tuneUpCrumbs", "index"); err != nil || strings.TrimSpace(out.String()) != "" {
		t.Fatalf("no line on the Library: %q", out.String())
	}
}
