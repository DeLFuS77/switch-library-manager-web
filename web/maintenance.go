package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The steps to put a library in order, in the order they depend on each other: a check
// before a cleanup (a duplicate is only deleted for a copy found sound), organizing before
// a cleanup (a copy already where a file goes is a duplicate), compressing last (nothing is
// compressed that is then deleted).
const (
	STEP_SYNC     = "sync"
	STEP_VERIFY   = "verify"
	STEP_ORGANIZE = "organize"
	STEP_SPACE    = "space"
	STEP_COMPRESS = "compress"
)

// the states of a step
const (
	STEP_DONE      = "done"
	STEP_TODO      = "todo"
	STEP_PREPARING = "preparing"
)

// MaintenanceStep is a step of the tune-up of the library and what is left to do in it.
type MaintenanceStep struct {
	Id     string
	Number int
	Icon   string
	Title  string
	// why the step comes here, and what is left to do, both translated
	Why    string
	Status string
	Detail string
	// where the step is done
	Href   string
	Action string
	// the first step left to do
	Next bool
}

// MaintenancePageData is the tune-up page.
type MaintenancePageData struct {
	GlobalPageData
	Steps []MaintenanceStep
	// the automations take care of new files in the same order
	AutomationsOn bool
	// when the library was last looked at, and whether a step is still being worked out
	LastReview time.Time
	Preparing  bool
}

// stepOfPage is the step a tool page is, for the line that points to the tune-up.
var stepOfPage = map[string]int{"organize": 3, "space": 4, "compress": 5}

// maintenanceSteps tells the state of every step; what takes long to compute is prepared in
// the background, and the step says so meanwhile.
func (web *Web) maintenanceSteps(lang string) []MaintenanceStep {
	t := func(text string, args ...any) string { return translatef(lang, text, args...) }
	appSettings := settings.ReadSettings(web.dataFolder)
	steps := []MaintenanceStep{
		{Id: STEP_SYNC, Icon: "bi-arrow-repeat", Title: t("Synchronize"), Href: "/tasks.html",
			Why: t("The app learns which files you have and the data of the games is up to date.")},
		{Id: STEP_VERIFY, Icon: "bi-shield-check", Title: t("Check the files"), Href: "/issues.html",
			Why: t("It tells the sound files from the damaged ones. Without it, the cleanup keeps duplicates whose other copy is not checked.")},
		{Id: STEP_ORGANIZE, Icon: "bi-folder-symlink", Title: t("Organize"), Href: "/organize.html",
			Why: t("Moves and renames the files. A copy already where a file goes stays as a duplicate, for the next step.")},
		{Id: STEP_SPACE, Icon: "bi-hdd", Title: t("Free space"), Href: "/space.html",
			Why: t("With the files checked and in order, old updates and duplicates are deleted safely.")},
		{Id: STEP_COMPRESS, Icon: "bi-file-zip", Title: t("Compress"), Href: "/compress.html",
			Why: t("Last, on what is left: nothing is compressed that is then deleted.")},
	}
	_, localDB := web.state.get()

	for i := range steps {
		step := &steps[i]
		step.Number = i + 1
		switch step.Id {
		case STEP_SYNC:
			switch {
			case web.state.IsSynchronizing():
				step.Status, step.Detail = STEP_PREPARING, t("Synchronizing now.")
			case localDB == nil || appSettings.LastSyncTime.IsZero():
				step.Status, step.Detail, step.Action = STEP_TODO, t("The library was never synchronized."), "sync"
			case time.Since(appSettings.LastSyncTime) > 7*24*time.Hour:
				step.Status, step.Detail, step.Action = STEP_TODO, t("Last synchronized %v.", formatDateTime(lang, appSettings.LastSyncTime)), "sync"
			default:
				step.Status, step.Detail = STEP_DONE, t("Last synchronized %v.", formatDateTime(lang, appSettings.LastSyncTime))
			}
		case STEP_VERIFY:
			unchecked, damaged := web.uncheckedFiles()
			switch {
			case localDB == nil:
				step.Status, step.Detail = STEP_PREPARING, t("Waiting for the library.")
			case web.verifying():
				step.Status, step.Detail = STEP_PREPARING, t("A check is running.")
			case unchecked > 0:
				step.Status, step.Detail, step.Action = STEP_TODO, t("%v files were never checked.", formatNumber(lang, unchecked)), "verify"
			case damaged > 0:
				step.Status, step.Detail = STEP_DONE, t("Everything checked; %v damaged files are on the Issues page.", formatNumber(lang, damaged))
			default:
				step.Status, step.Detail = STEP_DONE, t("Every file is checked.")
			}
		case STEP_ORGANIZE:
			changes, ready := web.organizeChanges()
			updating := false
			if !ready && web.lastOrganize.Load() > 0 {
				// the last count, while it is counted again
				changes, ready, updating = int(web.lastOrganize.Load()-1), true, true
			}
			defer func(step *MaintenanceStep) {
				if updating {
					step.Detail += " " + t("(updating...)")
				}
			}(step)
			switch {
			case localDB == nil || !ready:
				step.Status, step.Detail = STEP_PREPARING, t("Looking at what would change...")
			case changes > 0:
				step.Status, step.Detail = STEP_TODO, t("%v changes are waiting.", formatNumber(lang, changes))
				if changes == 1 {
					step.Detail = t("1 change is waiting.")
				}
			default:
				step.Status, step.Detail = STEP_DONE, t("Every file is in its place.")
			}
		case STEP_SPACE:
			ready := web.spaceReady()
			last := web.lastSpaceContent()
			switch {
			case localDB == nil || (!ready && last == nil):
				step.Status, step.Detail = STEP_PREPARING, t("Looking at what can be freed...")
			default:
				data := *last
				if ready {
					data = web.spaceContent()
				}
				if data.Reclaimable > 0 {
					step.Status, step.Detail = STEP_TODO, t("%v can be freed now.", formatSize(data.Reclaimable))
				} else {
					step.Status, step.Detail = STEP_DONE, t("Nothing to free now.")
				}
				if data.Unverified > 0 {
					step.Detail += " " + t("%v more once their other copy is checked.", formatNumber(lang, data.Unverified))
				}
				if !ready {
					step.Detail += " " + t("(updating...)")
				}
			}
		case STEP_COMPRESS:
			candidates := web.uncompressedCandidates()
			var size int64
			for _, candidate := range candidates {
				size += candidate.Size
			}
			switch {
			case localDB == nil:
				step.Status, step.Detail = STEP_PREPARING, t("Waiting for the library.")
			case !settings.IsKeysFileAvailable():
				step.Status, step.Detail = STEP_DONE, t("Compressing needs your prod.keys.")
			case len(candidates) > 0:
				step.Status, step.Detail = STEP_TODO, t("%v files are not compressed (%v).", formatNumber(lang, len(candidates)), formatSize(size))
			default:
				step.Status, step.Detail = STEP_DONE, t("Every file is compressed.")
			}
		}
	}
	for i := range steps {
		if steps[i].Status == STEP_TODO {
			steps[i].Next = true
			break
		}
	}
	return steps
}

// tuneUpWaiting tells whether a step of the tune-up worth doing is waiting, from what is
// known already (nothing is computed for it): files never checked, changes of Organize,
// space that can be freed.
func (web *Web) tuneUpWaiting() bool {
	if unchecked, _ := web.uncheckedFiles(); unchecked > 0 {
		return true
	}
	if value, ok := web.derivedCached("organizeChanges"); ok && value.(int) > 0 {
		return true
	}
	if value, ok := web.derivedCached(web.spaceKey()); ok && value.(SpacePageData).Reclaimable > 0 {
		return true
	}
	return false
}

// uncheckedFiles counts the files of the library never checked (or found modified by an
// older check), and the ones found damaged, from the results alone: nothing is read.
func (web *Web) uncheckedFiles() (unchecked int, damaged int) {
	store := web.verifications()
	// asked by the header of every page: counted again when the results change, as they are
	// saved while a check runs (every file would count it again before it is shown)
	version := store.Version()
	if web.verifying() {
		version = store.SavedVersion()
	}
	counts := web.derived(fmt.Sprintf("unchecked:%d", version), func() any {
		files := web.derived("gameFiles", func() any { return web.libraryFiles(".nsp", ".nsz", ".xci", ".xcz") }).([]CompressCandidate)
		store.mutex.Lock()
		defer store.mutex.Unlock()
		counts := [2]int{}
		for _, candidate := range files {
			record, ok := store.records[candidate.Path]
			switch {
			case !ok || record.stale():
				counts[0]++
			case !record.OK:
				counts[1]++
			}
		}
		return counts
	}).([2]int)
	return counts[0], counts[1]
}

// verifying tells whether a check of the files is running.
func (web *Web) verifying() bool {
	for _, task := range web.taskLog().Snapshot() {
		if task.Running() && task.Kind == TASK_VERIFY {
			return true
		}
	}
	return false
}

// organizeChanges counts the changes Organize would make, prepared in the background: the
// preview looks at every file, which takes a while on a NAS.
func (web *Web) organizeChanges() (int, bool) {
	const name = "organizeChanges"
	if value, ok := web.derivedCached(name); ok {
		return value.(int), true
	}
	if web.organizePreparing.CompareAndSwap(false, true) {
		go func() {
			defer web.organizePreparing.Store(false)
			web.derived(name, func() any {
				operations, err := web.organize(ORGANIZE_ACTION_ORGANIZE, true)
				if err != nil {
					return 0
				}
				changes := 0
				for _, op := range operations {
					if op.Error == "" && op.Kind != process.OP_SKIP && op.Kind != process.OP_CLEANUP {
						changes++
					}
				}
				// kept even if the library changed meanwhile, to show while it is counted again
				web.lastOrganize.Store(int64(changes) + 1)
				return changes
			})
		}()
	}
	return 0, false
}

func (web *Web) HandleMaintenance() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/maintenance.html")
	web.router.HandleFunc("/maintenance.html", func(w http.ResponseWriter, r *http.Request) {
		automation := settings.ReadSettings(web.dataFolder).Automation
		data := MaintenancePageData{
			GlobalPageData: web.globalPageData("maintenance"),
			Steps:          web.maintenanceSteps(web.requestLanguage(r)),
			AutomationsOn:  automation.Enabled,
		}
		// the page asks again by itself while the quick figures are worked out; not for the
		// hours a check of the files may run
		for _, step := range data.Steps {
			if step.Status == STEP_PREPARING && (step.Id == STEP_ORGANIZE || step.Id == STEP_SPACE) {
				data.Preparing = true
			}
		}
		tasks := web.taskLog().Snapshot()
		for _, kind := range []string{TASK_SCAN, TASK_SYNC} {
			if task := lastTask(tasks, kind); task != nil && task.Finished.After(data.LastReview) {
				data.LastReview = task.Finished
			}
		}
		web.render(w, r, templates, data)
	}).Methods("GET")

	// look at the library again: a scan (quick, only new or changed files are read) after
	// which every step is worked out again
	web.router.HandleFunc("/maintenance/review", func(w http.ResponseWriter, r *http.Request) {
		web.Rescan(TRIGGER_MANUAL)
		writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
	}).Methods("POST")
}

// maintenanceStepOf is the number of the step a tool page is, or 0.
func maintenanceStepOf(page string) string {
	if number, ok := stepOfPage[page]; ok {
		return fmt.Sprint(number)
	}
	return ""
}
