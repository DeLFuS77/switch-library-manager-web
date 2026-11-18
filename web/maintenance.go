package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
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
	// what is left to do as a figure, big on the card of the step, and what it counts; Count is
	// the figure as a number, for the figures that count up when the page opens
	Figure      string
	FigureLabel string
	Count       int
	// more about what is left, under the figure
	Note string
	// the parts of the tool of the step, as shortcuts on its card
	Shortcuts []StepShortcut
}

// StepShortcut is a part of the tool of a step, opened from the card of the step: an action, a
// tab or a group of its page.
type StepShortcut struct {
	Icon  string
	Label string
	Href  string
	// a number or a size next to it
	Badge string
	// done right there instead of opening a page: "sync" or "verify"
	Action string
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
	// the page asked again by itself while steps are worked out: it does not come in again
	Again bool
}

// DoneCount is how many steps are done, for the ring at the top of the page.
func (d MaintenancePageData) DoneCount() int {
	done := 0
	for _, step := range d.Steps {
		if step.Status == STEP_DONE {
			done++
		}
	}
	return done
}

// NextStep is the first step left to do, or nil when none is.
func (d MaintenancePageData) NextStep() *MaintenanceStep {
	for i := range d.Steps {
		if d.Steps[i].Next {
			return &d.Steps[i]
		}
	}
	return nil
}

// AllDone tells whether every step is done.
func (d MaintenancePageData) AllDone() bool {
	return len(d.Steps) > 0 && d.DoneCount() == len(d.Steps)
}

// DonePercent is the share of the steps that are done.
func (d MaintenancePageData) DonePercent() int {
	if len(d.Steps) == 0 {
		return 0
	}
	return d.DoneCount() * 100 / len(d.Steps)
}

// stepOfPage is the step a tool page is, for the line that points to the tune-up.
var stepOfPage = map[string]int{"organize": 3, "space": 4, "compress": 5}

// maintenanceSteps tells the state of every step; what takes long to compute is prepared in
// the background, and the step says so meanwhile.
func (web *Web) maintenanceSteps(lang string) []MaintenanceStep {
	t := func(text string, args ...any) string { return i18n.Translatef(lang, text, args...) }
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
			step.Shortcuts = []StepShortcut{
				{Icon: "bi-arrow-repeat", Label: t("Synchronize"), Href: "/sync", Action: "sync"},
				{Icon: "bi-list-task", Label: t("Tasks"), Href: "/tasks.html"},
			}
			switch {
			case web.state.IsSynchronizing():
				step.Status, step.Detail = STEP_PREPARING, t("Synchronizing now.")
			case localDB == nil || appSettings.LastSyncTime.IsZero():
				step.Status, step.Detail, step.Action = STEP_TODO, t("The library was never synchronized."), "sync"
			case time.Since(appSettings.LastSyncTime) > 7*24*time.Hour:
				step.Status, step.Detail, step.Action = STEP_TODO, t("Last synchronized %v.", i18n.FormatDateTime(lang, appSettings.LastSyncTime)), "sync"
				step.Count = int(time.Since(appSettings.LastSyncTime) / (24 * time.Hour))
				step.Figure, step.FigureLabel = i18n.FormatNumber(lang, step.Count), t("days since the last synchronization")
			default:
				step.Status, step.Detail = STEP_DONE, t("Last synchronized %v.", i18n.FormatDateTime(lang, appSettings.LastSyncTime))
			}
		case STEP_VERIFY:
			unchecked, damaged := web.uncheckedFiles()
			step.Shortcuts = []StepShortcut{{Icon: "bi-shield-check", Label: t("Check new files"), Href: "/issues.html", Action: "verify"}}
			if damaged > 0 {
				step.Shortcuts = append(step.Shortcuts, StepShortcut{Icon: "bi-exclamation-octagon", Label: t("Issues"), Href: "/issues.html?kind=damaged", Badge: i18n.FormatNumber(lang, damaged)})
			} else {
				step.Shortcuts = append(step.Shortcuts, StepShortcut{Icon: "bi-exclamation-triangle", Label: t("Issues"), Href: "/issues.html"})
			}
			switch {
			case localDB == nil:
				step.Status, step.Detail = STEP_PREPARING, t("Waiting for the library.")
			case web.verifying():
				step.Status, step.Detail = STEP_PREPARING, t("A check is running.")
			case unchecked > 0:
				step.Status, step.Detail, step.Action = STEP_TODO, t("%v files were never checked.", i18n.FormatNumber(lang, unchecked)), "verify"
				step.Count, step.Figure, step.FigureLabel = unchecked, i18n.FormatNumber(lang, unchecked), t("never checked")
			case damaged > 0:
				step.Status, step.Detail = STEP_DONE, t("Everything checked; %v damaged files are on the Issues page.", i18n.FormatNumber(lang, damaged))
				if damaged == 1 {
					step.Detail = t("Everything checked; 1 damaged file is on the Issues page.")
				}
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
					step.Note = t("(updating...)")
				}
			}(step)
			step.Shortcuts = []StepShortcut{
				{Icon: "bi-box-arrow-in-down", Label: t("Organize new files"), Href: "/organize.html#new"},
				{Icon: "bi-folder-symlink", Label: t("Organize all files"), Href: "/organize.html#organize"},
				{Icon: "bi-trash3", Label: t("Delete old updates"), Href: "/organize.html#cleanup"},
				{Icon: "bi-sliders", Label: t("Options"), Href: "/organize.html#options"},
			}
			if runs := len(web.undoRows()); runs > 0 {
				step.Shortcuts = append(step.Shortcuts, StepShortcut{Icon: "bi-arrow-counterclockwise", Label: t("Earlier organizes"), Href: "/organize.html#undo", Badge: i18n.FormatNumber(lang, runs)})
			}
			switch {
			case localDB == nil || !ready:
				step.Status, step.Detail = STEP_PREPARING, t("Looking at what would change...")
			case changes > 0:
				step.Status, step.Detail = STEP_TODO, t("%v changes are waiting.", i18n.FormatNumber(lang, changes))
				if changes == 1 {
					step.Detail = t("1 change is waiting.")
				}
				step.Count, step.Figure, step.FigureLabel = changes, i18n.FormatNumber(lang, changes), t("to move or rename")
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
					step.Status, step.Detail = STEP_TODO, t("%v can be freed now.", i18n.FormatSizeIn(lang, data.Reclaimable))
					step.Figure, step.FigureLabel = i18n.FormatSizeIn(lang, data.Reclaimable), t("to free now")
				} else {
					step.Status, step.Detail = STEP_DONE, t("Nothing to free now.")
				}
				notes := []string{}
				if data.Unverified > 0 {
					notes = append(notes, t("%v more once their other copy is checked.", i18n.FormatNumber(lang, data.Unverified)))
				}
				if !ready {
					notes = append(notes, t("(updating...)"))
				}
				for _, note := range notes {
					step.Detail += " " + note
				}
				step.Note = strings.Join(notes, " ")
				step.Shortcuts = spaceShortcuts(t, lang, data)
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
				step.Status, step.Detail = STEP_TODO, t("%v files are not compressed (%v).", i18n.FormatNumber(lang, len(candidates)), i18n.FormatSizeIn(lang, size))
				step.Count, step.Figure, step.FigureLabel = len(candidates), i18n.FormatNumber(lang, len(candidates)), t("to compress, %v", i18n.FormatSizeIn(lang, size))
			default:
				step.Status, step.Detail = STEP_DONE, t("Every file is compressed.")
			}
			if localDB != nil && settings.IsKeysFileAvailable() {
				step.Shortcuts = web.compressShortcuts(t, lang, len(candidates))
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
	counts := web.derivedLatest("unchecked:", fmt.Sprintf("unchecked:%d", version), func() any {
		files := web.derived("gameFiles", func() any { return web.libraryFiles(".nsp", ".nsz", ".xci", ".xcz") }).([]CompressCandidate)
		store.mutex.Lock()
		defer store.mutex.Unlock()
		counts := [2]int{}
		for _, candidate := range files {
			record, ok := store.records[candidate.Path]
			switch {
			case !ok || record.stale(candidate.Path):
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
		if task.Running() && task.Kind == tasklog.TASK_VERIFY {
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
			Again:          r.URL.Query().Has("again"),
		}
		// the page asks again by itself while the quick figures are worked out; not for the
		// hours a check of the files may run
		for _, step := range data.Steps {
			if step.Status == STEP_PREPARING && (step.Id == STEP_ORGANIZE || step.Id == STEP_SPACE) {
				data.Preparing = true
			}
		}
		tasks := web.taskLog().Snapshot()
		for _, kind := range []string{tasklog.TASK_SCAN, tasklog.TASK_SYNC} {
			if task := lastTask(tasks, kind); task != nil && task.Finished.After(data.LastReview) {
				data.LastReview = task.Finished
			}
		}
		web.render(w, r, templates, data)
	}).Methods("GET")

	// look at the library again: a scan (quick, only new or changed files are read) after
	// which every step is worked out again
	web.router.HandleFunc("/maintenance/review", func(w http.ResponseWriter, r *http.Request) {
		web.Rescan(tasklog.TRIGGER_MANUAL)
		writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
	}).Methods("POST")
}

// spaceShortcuts are the groups of the Space page with something in them, each with what can be
// freed in it now, and the games from several regions.
func spaceShortcuts(t func(string, ...any) string, lang string, data SpacePageData) []StepShortcut {
	shortcuts := []StepShortcut{}
	for _, group := range data.Groups {
		if len(group.Files) == 0 {
			continue
		}
		shortcut := StepShortcut{Icon: "bi-files", Label: t("Duplicates"), Href: "/space.html#" + group.Id}
		switch group.Id {
		case "old_updates":
			shortcut.Icon, shortcut.Label = "bi-clock-history", t("Old updates")
		case "compressed":
			shortcut.Icon, shortcut.Label = "bi-file-earmark-zip", t("Compressed originals")
		}
		if group.DeletableSize > 0 {
			shortcut.Badge = i18n.FormatSizeIn(lang, group.DeletableSize)
		}
		shortcuts = append(shortcuts, shortcut)
	}
	if len(data.Regions) > 0 {
		shortcuts = append(shortcuts, StepShortcut{Icon: "bi-globe2", Label: t("Regions"), Href: "/space.html#regions", Badge: i18n.FormatNumber(lang, len(data.Regions))})
	}
	return shortcuts
}

// compressShortcuts are the tabs of the Compress page with files in them, as the page shows them.
func (web *Web) compressShortcuts(t func(string, ...any) string, lang string, uncompressed int) []StepShortcut {
	tabs := []struct {
		icon, label, tab string
		count            int
	}{
		{"bi-file-zip", t("Compress"), "compress", uncompressed},
		{"bi-arrow-left-right", t("Convert"), "convert", len(web.convertCandidates())},
		{"bi-scissors", t("Split"), "split", len(web.splitCandidates())},
		{"bi-box-seam", t("Combine"), "packs", len(web.packCandidates())},
		{"bi-lightning-charge", t("Optimize for install"), "optimize", len(web.optimizeCandidates())},
		{"bi-box-arrow-up", t("Decompress"), "decompress", len(web.decompressCandidates())},
	}
	shortcuts := []StepShortcut{}
	for _, tab := range tabs {
		if tab.count > 0 {
			shortcuts = append(shortcuts, StepShortcut{Icon: tab.icon, Label: tab.label, Href: "/compress.html#" + tab.tab, Badge: i18n.FormatNumber(lang, tab.count)})
		}
	}
	return shortcuts
}

// TuneUpLink is a step of the tune-up in the line of steps of a tool page: its number, its name
// (to translate) and where it is done.
type TuneUpLink struct {
	Number  int
	Title   string
	Icon    string
	Href    string
	Current bool
}

// tuneUpStepsOf are the five steps for the line of steps of a tool page, the step of the page
// marked; none on a page that is not a step. Their state is not worked out here: it costs too
// much to do on every tool page, and the tune-up shows it.
func tuneUpStepsOf(page string) []TuneUpLink {
	number, ok := stepOfPage[page]
	if !ok {
		return nil
	}
	links := []TuneUpLink{
		{Number: 1, Title: "Synchronize", Icon: "bi-arrow-repeat", Href: "/tasks.html"},
		{Number: 2, Title: "Check the files", Icon: "bi-shield-check", Href: "/issues.html"},
		{Number: 3, Title: "Organize", Icon: "bi-folder-symlink", Href: "/organize.html"},
		{Number: 4, Title: "Free space", Icon: "bi-hdd", Href: "/space.html"},
		{Number: 5, Title: "Compress", Icon: "bi-file-zip", Href: "/compress.html"},
	}
	for i := range links {
		links[i].Current = links[i].Number == number
	}
	return links
}

// maintenanceStepOf is the number of the step a tool page is, or 0.
func maintenanceStepOf(page string) string {
	if number, ok := stepOfPage[page]; ok {
		return fmt.Sprint(number)
	}
	return ""
}
