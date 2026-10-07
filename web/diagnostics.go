package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The diagnostics page checks at a glance what the app needs to work: the keys, the library
// folders, the titles database, the free space and the protection, with a report to copy
// when asking for help. It shows no key and no password.

const (
	CHECK_OK    = "ok"
	CHECK_WARN  = "warn"
	CHECK_ERROR = "error"
	CHECK_INFO  = "info"
	// less free space than this is a warning
	lowDiskSpace = 5 << 30
)

// Check is one line of the diagnostics.
type Check struct {
	Label  string
	Value  string
	Status string
	// what to do about it, when it is not right
	Hint string
}

// CheckGroup is a part of the diagnostics.
type CheckGroup struct {
	Title  string
	Icon   string
	Checks []Check
}

// DiagnosticsPageData is the diagnostics page.
type DiagnosticsPageData struct {
	GlobalPageData
	Groups []CheckGroup
	// the checks that are not right
	Problems int
	// the whole diagnostics as text, to paste when asking for help
	Report string
}

var appStarted = time.Now()

func (web *Web) diagnostics(r *http.Request, lang string) DiagnosticsPageData {
	t := func(text string, args ...any) string {
		if len(args) > 0 {
			return translatef(lang, text, args...)
		}
		return translate(lang, text)
	}
	appSettings := settings.ReadSettings(web.dataFolder)
	switchDB, localDB := web.state.get()
	groups := []CheckGroup{}

	// the app
	app := CheckGroup{Title: t("App"), Icon: "bi-app-indicator"}
	app.Checks = append(app.Checks,
		Check{Label: t("Version"), Value: settings.SLM_WEB_VERSION, Status: CHECK_INFO},
		Check{Label: t("System"), Value: fmt.Sprintf("%s/%s, %s, %d %s", runtime.GOOS, runtime.GOARCH, runtime.Version(), runtime.NumCPU(), t("cores")), Status: CHECK_INFO},
		Check{Label: t("Running for"), Value: formatDuration(time.Since(appStarted)), Status: CHECK_INFO},
		Check{Label: t("Data folder"), Value: web.dataFolder, Status: CHECK_INFO},
	)
	if update := web.availableUpdate(); update != nil {
		app.Checks = append(app.Checks, Check{Label: t("New version"), Value: update.Version, Status: CHECK_WARN, Hint: t("Version %v is available.", update.Version)})
	}
	groups = append(groups, app)

	// keys
	keys := CheckGroup{Title: t("Keys"), Icon: "bi-key"}
	count, titleKeys, generation := settings.KeysSummary()
	if settings.IsKeysFileAvailable() {
		keys.Checks = append(keys.Checks, Check{Label: "prod.keys", Value: t("%v keys, up to generation %v", count, generation), Status: CHECK_OK})
	} else {
		keys.Checks = append(keys.Checks, Check{Label: "prod.keys", Value: t("Not found"), Status: CHECK_ERROR, Hint: t("Games are recognized by their file names only, and compression is off. Add your own prod.keys, dumped from your console.")})
	}
	keys.Checks = append(keys.Checks, Check{Label: "title.keys", Value: fmt.Sprint(titleKeys), Status: CHECK_INFO})
	groups = append(groups, keys)

	// library folders
	folders := CheckGroup{Title: t("Library folders"), Icon: "bi-folder2-open"}
	// the first folder and the other ones
	libraryFolders := []string{}
	for _, folder := range append([]string{appSettings.Folder}, appSettings.ScanFolders...) {
		if strings.TrimSpace(folder) != "" {
			libraryFolders = append(libraryFolders, folder)
		}
	}
	if len(libraryFolders) == 0 {
		folders.Checks = append(folders.Checks, Check{Label: t("Folders"), Value: t("None"), Status: CHECK_ERROR, Hint: t("Add your games folders in Settings to see the statistics of your library.")})
	}
	for _, folder := range libraryFolders {
		check := Check{Label: folder, Status: CHECK_OK}
		info, err := os.Stat(folder)
		switch {
		case err != nil || !info.IsDir():
			check.Status, check.Value, check.Hint = CHECK_ERROR, t("Not found"), t("Check that the folder exists and, in Docker, that it is mounted in the container.")
		case !writable(folder):
			check.Status, check.Value, check.Hint = CHECK_WARN, t("Read only"), t("The games can be listed and downloaded, but organize, compress and clean up cannot change them.")
		default:
			check.Value = t("Readable and writable")
		}
		if free, err := diskFree(folder); err == nil {
			check.Value += " · " + t("%v free", formatSize(int64(free)))
			if free < lowDiskSpace && check.Status == CHECK_OK {
				check.Status, check.Hint = CHECK_WARN, t("Little free space left.")
			}
		}
		folders.Checks = append(folders.Checks, check)
	}
	if localDB != nil {
		folders.Checks = append(folders.Checks, Check{Label: t("Files found"), Value: t("%v files, %v not recognized", num(lang, localDB.NumFiles), num(lang, len(localDB.Skipped))), Status: CHECK_INFO})
	}
	if scan := lastTask(web.taskLog().Snapshot(), TASK_SCAN); scan != nil {
		check := Check{Label: t("Last scan"), Value: formatDateTime(lang, scan.Started) + " · " + t("took %v", scan.Duration()), Status: CHECK_OK}
		if scan.Status != TASK_SUCCESS {
			check.Status, check.Hint = CHECK_WARN, t("See the details in Tasks.")
		}
		folders.Checks = append(folders.Checks, check)
	}
	groups = append(groups, folders)

	// titles database
	database := CheckGroup{Title: t("Titles database"), Icon: "bi-database"}
	if switchDB == nil {
		database.Checks = append(database.Checks, Check{Label: t("Titles"), Value: t("Not loaded"), Status: CHECK_ERROR, Hint: t("Synchronize to download the titles database.")})
	} else {
		database.Checks = append(database.Checks, Check{Label: t("Titles"), Value: num(lang, len(switchDB.TitlesMap)), Status: CHECK_OK})
	}
	sync := Check{Label: t("Last synchronization"), Value: formatDateTime(lang, appSettings.LastSyncTime), Status: CHECK_OK}
	if appSettings.LastSyncTime.IsZero() {
		sync.Status, sync.Hint = CHECK_WARN, t("Synchronize to download the titles database.")
	} else if time.Since(appSettings.LastSyncTime) > 14*24*time.Hour {
		sync.Status, sync.Hint = CHECK_WARN, t("The titles database is more than two weeks old: new updates and DLC are missing.")
	}
	database.Checks = append(database.Checks, sync)
	if languages := web.titleLanguages(); len(languages) > 0 {
		database.Checks = append(database.Checks, Check{Label: t("Translated names"), Value: strings.Join(languages, ", "), Status: CHECK_INFO})
	}
	groups = append(groups, database)

	// data folder and backups
	data := CheckGroup{Title: t("Data and backups"), Icon: "bi-hdd"}
	dataCheck := Check{Label: t("Data folder"), Value: t("Readable and writable"), Status: CHECK_OK}
	if !writable(web.dataFolder) {
		dataCheck.Status, dataCheck.Value, dataCheck.Hint = CHECK_ERROR, t("Read only"), t("The settings cannot be saved. In Docker, check PUID and PGID.")
	}
	if free, err := diskFree(web.dataFolder); err == nil {
		dataCheck.Value += " · " + t("%v free", formatSize(int64(free)))
		if free < lowDiskSpace/5 && dataCheck.Status == CHECK_OK {
			dataCheck.Status, dataCheck.Hint = CHECK_WARN, t("Little free space left.")
		}
	}
	data.Checks = append(data.Checks, dataCheck)
	if backups := web.autoBackups(); len(backups) > 0 {
		data.Checks = append(data.Checks, Check{Label: t("Automatic copies"), Value: t("%v, the last on %v", len(backups), formatDateTime(lang, backups[0].Time)), Status: CHECK_OK})
	} else {
		data.Checks = append(data.Checks, Check{Label: t("Automatic copies"), Value: t("None yet"), Status: CHECK_INFO})
	}
	groups = append(groups, data)

	// protection
	protection := CheckGroup{Title: t("Protection"), Icon: "bi-shield-lock"}
	switch {
	case web.auth == nil || web.auth.Enabled():
		protection.Checks = append(protection.Checks, Check{Label: t("Login"), Value: t("Required"), Status: CHECK_OK})
	case web.auth.remoteWithoutLogin:
		protection.Checks = append(protection.Checks, Check{Label: t("Login"), Value: t("Off, also outside the local network"), Status: CHECK_WARN, Hint: t("Anyone who can reach the app is an administrator. Create an administrator unless another service protects it.")})
	default:
		protection.Checks = append(protection.Checks, Check{Label: t("Login"), Value: t("Off, local network only"), Status: CHECK_WARN, Hint: t("Anyone on your network can change or delete your library. Create an administrator to protect the app, above all if it can be reached from the internet.")})
	}
	https := Check{Label: "HTTPS", Value: t("Yes"), Status: CHECK_OK}
	if !isHttps(r) {
		https.Value, https.Status = t("No"), CHECK_INFO
		if !isLocalRequest(r) {
			https.Status, https.Hint = CHECK_WARN, t("The app is opened from outside the local network without HTTPS: passwords travel unencrypted. Put it behind a reverse proxy with HTTPS.")
		}
	}
	protection.Checks = append(protection.Checks, https)
	groups = append(groups, protection)

	// work in the background
	work := CheckGroup{Title: t("Tasks"), Icon: "bi-list-task"}
	failed := web.taskLog().FailedCount()
	tasks := Check{Label: t("Failed tasks"), Value: fmt.Sprint(failed), Status: CHECK_OK}
	if failed > 0 {
		tasks.Status, tasks.Hint = CHECK_WARN, t("See the details in Tasks.")
	}
	work.Checks = append(work.Checks, tasks, Check{Label: t("Issues"), Value: num(lang, len(web.getIssues())), Status: CHECK_INFO})
	if last := web.verifications().lastRun(); !last.IsZero() {
		work.Checks = append(work.Checks, Check{Label: t("Last file check"), Value: formatDateTime(lang, last), Status: CHECK_INFO})
	}
	groups = append(groups, work)

	result := DiagnosticsPageData{Groups: groups}
	var report strings.Builder
	fmt.Fprintf(&report, "Switch Library Manager Web %s — %s\n", settings.SLM_WEB_VERSION, time.Now().Format(time.RFC3339))
	for _, group := range groups {
		fmt.Fprintf(&report, "\n[%s]\n", group.Title)
		for _, check := range group.Checks {
			if check.Status == CHECK_WARN || check.Status == CHECK_ERROR {
				result.Problems++
			}
			fmt.Fprintf(&report, "%-6s %s: %s\n", strings.ToUpper(check.Status), check.Label, check.Value)
		}
	}
	result.Report = report.String()
	return result
}

func num(lang string, n int) string {
	return formatNumber(lang, n)
}

// writable reports whether a file can be created in a folder.
func writable(folder string) bool {
	file, err := os.CreateTemp(folder, ".slm-write-test-*")
	if err != nil {
		return false
	}
	name := file.Name()
	file.Close()
	os.Remove(name)
	return filepath.Dir(name) != ""
}

// lastTask returns the newest finished task of a kind.
func lastTask(tasks []Task, kind string) *Task {
	var last *Task
	for i := range tasks {
		task := &tasks[i]
		if task.Kind != kind || task.Running() {
			continue
		}
		if last == nil || task.Started.After(last.Started) {
			last = task
		}
	}
	return last
}

// formatDuration is a duration in days, hours and minutes.
func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func (web *Web) HandleDiagnostics() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/diagnostics.html")
	web.router.HandleFunc("/diagnostics.html", func(w http.ResponseWriter, r *http.Request) {
		data := web.diagnostics(r, web.requestLanguage(r))
		data.GlobalPageData = web.globalPageData("diagnostics")
		web.render(w, r, templates, data)
	}).Methods("GET")
}
