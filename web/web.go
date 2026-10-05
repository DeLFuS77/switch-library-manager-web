package web

import (
	"embed"
	"io/fs"
	"errors"
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// WebState holds the databases shared by all requests. They are only ever replaced as a
// whole, so handlers take a snapshot with get() and never see a half built state.
type WebState struct {
	mutex           sync.RWMutex
	switchDB        *db.SwitchTitlesDB
	localDB         *db.LocalSwitchFilesDB
	isSynchronizing bool
	progress        SyncProgress
	// changes every time the databases are replaced
	version uint64
}

// Version identifies the current databases, so results computed from them can be cached.
func (s *WebState) Version() uint64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.version
}

// SyncProgress describes the running synchronization. Total is 0 when the number of
// steps is unknown.
type SyncProgress struct {
	Synchronizing bool   `json:"synchronizing"`
	Current       int    `json:"current"`
	Total         int    `json:"total"`
	Message       string `json:"message"`
}

func (s *WebState) setProgress(current int, total int, message string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if total < 0 {
		// unknown total: keep the last known position
		current, total = s.progress.Current, s.progress.Total
	}
	s.progress = SyncProgress{Current: current, Total: total, Message: message}
}

func (s *WebState) getProgress() SyncProgress {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	progress := s.progress
	progress.Synchronizing = s.isSynchronizing
	return progress
}

func (s *WebState) get() (*db.SwitchTitlesDB, *db.LocalSwitchFilesDB) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.switchDB, s.localDB
}

func (s *WebState) set(switchDB *db.SwitchTitlesDB, localDB *db.LocalSwitchFilesDB) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.switchDB = switchDB
	s.localDB = localDB
	s.version++
}

// startSync marks a synchronization as running. It returns false if one is already running.
func (s *WebState) startSync() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.isSynchronizing {
		return false
	}
	s.isSynchronizing = true
	s.progress = SyncProgress{Message: "Starting..."}
	return true
}

func (s *WebState) endSync() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.isSynchronizing = false
}

func (s *WebState) IsSynchronizing() bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.isSynchronizing
}

type Web struct {
	state          WebState
	languages      titleLanguages
	auto           autoCompressor
	updates        updateChecker
	verify         *verifyStore
	verifyOnce     sync.Once
	compressor     compressor
	cache          derivedCache
	auth           *Auth
	tasks          *TaskLog
	tasksOnce      sync.Once
	// the task that receives the progress of the running synchronization, 0 if none
	currentTask    atomic.Int64
	router         *mux.Router
	// templates and static files: embedded in the binary, read from disk in tests
	embedFS        fs.FS
	appSettings    *settings.AppSettings
	dataFolder     string
	localDbManager *db.LocalSwitchDBManager
	sugarLogger    *zap.SugaredLogger
}

type TitleItem struct {
	ImageUrl         string
	Id               string
	LatestUpdate     int
	LatestUpdateDate time.Time
	LocalUpdate      int
	MissingDLC       []string
	MissingDLCItems  []db.TitleAttributes
	Name             string
	Region           string
	ReleaseDate      time.Time
	Type             string
	Version          string
	// library status, shown on the cards
	UpdateAvailable bool
	MissingDlcCount int
	// the name in the titles database, also matched by the search
	OriginalName string
	// the game is in the titles database
	Known bool
	// DLC of the game in the library, of the DLC not ignored
	DlcOwned int
	DlcTotal int
	// the firmware the installed game needs, when the console is too old for it
	RequiredFirmware string
	FirmwareTooNew   bool
}

// DlcPercent is the share of the DLC of a game in the library.
func (t TitleItem) DlcPercent() int {
	if t.DlcTotal == 0 {
		return 0
	}
	return t.DlcOwned * 100 / t.DlcTotal
}

type GlobalPageData struct {
	// set by render for the user of the request
	Auth                AuthInfo
	// a newer version of the app, if one was released
	Update              *AppUpdate
	// a made-up library is shown (SLM_DEMO=true)
	Demo                bool
	IsKeysFileAvailable bool
	IsSynchronizing     bool
	HasLibrary          bool
	Page                string
	SlmVersion          string
	Version             string
	Counts              NavCounts
}

// NavCounts are shown next to the navigation links, so pending work is visible everywhere.
type NavCounts struct {
	Updates int
	Dlc     int
	Issues  int
	// failed tasks that were not dismissed
	FailedTasks int
}

type TitleItemsPageData struct {
	GlobalPageData
	TitleItems []TitleItem
	Filter     *TitleItemFilter
	Pagination pagination.Pagination
}

// LibraryFacets are the numbers shown on the status filters of the library, counted
// before the status filter is applied, and the file formats found in the library.
type LibraryFacets struct {
	All      int
	Update   int
	Dlc      int
	Complete int
	Formats  []string
}

type LibraryPageData struct {
	TitleItemsPageData
	Facets LibraryFacets
	Setup  SetupStatus
	// the overview at the top of the library
	Stats Statistics
}

var funcMap = template.FuncMap {
	"add": func(a, b int) int {
		return a + b
	},
	"eq": func(a, b interface{}) bool {
		return a == b
	},
	"gt": func(a, b int) bool {
		return a > b
	},
	"lt": func(a, b int) bool {
		return a < b
	},
	"mkRange": func(min, max int) []int {
		a := make([]int, max - min + 1)
		for i := range a {
			a[i] = min + i
		}
		return a
	},
	"mkSlice": func(args ...interface{}) []interface{} {
		return args
	},
	"neq": func(a, b interface{}) bool {
		return a != b
	},
	"pageUrl": func(filter *TitleItemFilter, page int) template.URL {
		values := filter.query("page", strconv.Itoa(page))
		values.Set("page", strconv.Itoa(page))
		// url.Values.Encode escapes every value, so the result is safe to use as is
		return template.URL("?" + values.Encode())
	},
	// filterUrl links to the current list with some filter values replaced: filterUrl .Filter "status" "dlc"
	"filterUrl": func(filter *TitleItemFilter, replace ...string) template.URL {
		return template.URL("?" + filter.query(replace...).Encode())
	},
	"formatSize": formatSize,
	"thumb":      thumbUrl,
	"mod": func(a, b int) int {
		if b == 0 {
			return 0
		}
		return a % b
	},
	"conic":      conicGradient,
	// the first letter of a name, for avatars
	"initial": func(name string) string {
		for _, r := range name {
			return strings.ToUpper(string(r))
		}
		return "?"
	},
	"join": strings.Join,
	"issueIcon": issueIcon,
	// dict builds named parameters for a sub-template: dict "Key" value "Key2" value2
	"dict": func(values ...any) map[string]any {
		result := map[string]any{}
		for i := 0; i+1 < len(values); i += 2 {
			if key, ok := values[i].(string); ok {
				result[key] = values[i+1]
			}
		}
		return result
	},
	"intervalLabel": func(hours int) string {
		switch {
		case hours == 0:
			return "Disabled"
		case hours%24 == 0 && hours > 24:
			return fmt.Sprintf("Every %d days", hours/24)
		case hours == 24:
			return "Every day"
		default:
			return fmt.Sprintf("Every %d hours", hours)
		}
	},
	"subtract": func(a, b int) int {
		return a - b
	},
	"toLower": strings.ToLower,
}

func (web *Web) globalPageData(page string) GlobalPageData {
	_, localDB := web.state.get()
	return GlobalPageData {
		IsKeysFileAvailable: settings.IsKeysFileAvailable() || isDemoMode(),
		Demo: isDemoMode(),
		IsSynchronizing: web.state.IsSynchronizing(),
		HasLibrary: localDB != nil && len(localDB.TitlesMap) > 0,
		Page: page,
		SlmVersion: settings.SLM_VERSION,
		Version: settings.SLM_WEB_VERSION,
		Counts: web.navCounts(),
		Update: web.availableUpdate(),
	}
}

// navCounts counts the missing updates and DLC (respecting the ignore lists) and the issues.
func (web *Web) navCounts() NavCounts {
	counts := NavCounts{FailedTasks: web.taskLog().FailedCount()}
	switchDB, localDB := web.state.get()
	if localDB == nil {
		return counts
	}
	counts.Issues = len(web.getIssues())
	if switchDB == nil {
		return counts
	}
	counts.Updates = len(web.missingUpdates())
	for _, title := range web.missingDLC() {
		counts.Dlc += len(title.MissingDLCItems)
	}
	return counts
}

func intToTime(value int) (time.Time, error) {
	if value <= 0 {
		return time.Time{}, nil
	}

	return strToTime("20060102", strconv.Itoa(value))
}

func strToTime(layout, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}

	return time.Parse(layout, value)
}

func CreateWeb(router *mux.Router, embedFS embed.FS, appSettings *settings.AppSettings, dataFolder string, sugarLogger *zap.SugaredLogger) *Web {
	return &Web{router: router, embedFS: embedFS, appSettings: appSettings, dataFolder: dataFolder, sugarLogger: sugarLogger}
}

func (web *Web) Start() {
	localDbManager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		web.sugarLogger.Error("Failed to create local files db\n", err)
		return
	}

	_, err = settings.InitSwitchKeys(web.dataFolder)
	if err != nil {
		// running without keys is supported, files are then identified by their name
		web.sugarLogger.Warnf("prod.keys not loaded, files are identified by their file name only. Put prod.keys in %s to read their metadata (%s)", web.dataFolder, err)
	}

	web.localDbManager = localDbManager
	defer localDbManager.Close()

	if isDemoMode() {
		web.loadDemo()
	} else {
		web.startInBackground()
	}

	web.auth, err = newAuth(web.dataFolder)
	if err != nil {
		web.sugarLogger.Error(err)
		log.Fatal(err)
	}

	// Run http server
	web.router.Use(sameOriginOnly)
	web.HandleResources()
	web.HandleImages()
	web.HandleIndex()
	web.HandleMissing()
	web.HandleUpdates()
	web.HandleDLC()
	web.HandleIssues()
	web.HandleSettings()
	web.HandleSynchronize()
	web.HandleOrganize()
	web.HandleApi()
	web.HandleTitle()
	web.HandleIgnore()
	web.HandleExport()
	web.HandleStatistics()
	web.HandleNotifications()
	web.HandleArchive()
	web.HandleApiDocs()
	web.HandleTasks()
	web.HandleUsers()
	web.HandleCompress()
	web.HandleVerify()
	web.HandleBackup()
	if !isDemoMode() {
		web.StartScheduler()
		web.StartFolderWatcher()
		web.StartUpdateChecker()
	}

	web.router.Handle("/", http.RedirectHandler("/index.html", http.StatusMovedPermanently))

	http.Handle("/", web.router)

	handler := web.auth.middleware(http.DefaultServeMux)
	if isDemoMode() {
		handler = web.demoReadOnly(handler)
	}
	if web.auth.Enabled() {
		web.sugarLogger.Info("[Authentication enabled]")
	}

	web.sugarLogger.Info("[SLM started]")

	if err := http.ListenAndServe(fmt.Sprint(":", web.appSettings.Port), withSecurityHeaders(withHealthCheck(handler))); err != nil {
		web.sugarLogger.Error(fmt.Errorf("running http server failed: %w", err))
		log.Fatal(err)
	}
}

// startInBackground loads the saved titles database and the library while the web server
// already answers: on a small device, reading titles.json takes many seconds.
func (web *Web) startInBackground() {
	if !web.state.startSync() {
		return
	}
	go func() {
		web.UpdateProgress(-1, -1, "Loading titles database...")
		switchDB := web.loadSavedTitles()
		if switchDB == nil {
			web.state.endSync()
			// first start (or titles cache lost): fetch the titles database right away
			web.Synchronize(TRIGGER_STARTUP)
			return
		}

		// files that could not be read with the previous keys may be readable now
		rescan := web.localDbManager.KeysChanged(settings.KeysFingerprint())
		if rescan {
			web.sugarLogger.Info("The keys changed since the last scan, the library is scanned again")
		}
		// the pages show the titles while the library is scanned
		web.state.set(switchDB, nil)

		taskId := web.startTask(TASK_SCAN, TRIGGER_STARTUP)
		var failure *TaskNote
		localDB, err := web.buildLocalDB(switchDB, rescan)
		if err != nil {
			web.sugarLogger.Error(err)
			failure = &TaskNote{Text: NOTE_SCAN_FAILED, Detail: err.Error()}
		} else {
			web.taskLog().SetResult(taskId, len(localDB.TitlesMap), localDB.NumFiles)
			web.state.set(switchDB, localDB)
		}
		web.state.endSync()
		web.finishTask(taskId, failure)
		if failure == nil && settings.ReadSettings(web.dataFolder).AutoCompress == AUTO_COMPRESS_NEW {
			web.autoCompress(TRIGGER_STARTUP)
		}
	}()
}

// loadSavedTitles reads the titles database downloaded by an earlier synchronization.
func (web *Web) loadSavedTitles() *db.SwitchTitlesDB {
	titleFile, err := os.Open(filepath.Join(web.dataFolder, settings.TITLE_JSON_FILENAME))
	if err != nil {
		return nil
	}
	defer titleFile.Close()
	versionsFile, err := os.Open(filepath.Join(web.dataFolder, settings.VERSIONS_JSON_FILENAME))
	if err != nil {
		return nil
	}
	defer versionsFile.Close()

	switchDB, err := db.CreateSwitchTitleDB(titleFile, versionsFile)
	if err != nil {
		web.sugarLogger.Errorf("Failed to read cached titles, please synchronize - %v", err)
		return nil
	}
	switchDB.Localized = web.loadLocalizedTitles(false)
	return switchDB
}

func (web *Web) UpdateProgress(curr int, total int, message string) {
	web.sugarLogger.Debugf("%v (%v/%v)", message, curr, total)
	web.state.setProgress(curr, total, message)
	if id := web.currentTask.Load(); id != 0 {
		web.taskLog().Progress(id, curr, total, message)
	}
}

// taskLog returns the task history, loading it on first use.
func (web *Web) taskLog() *TaskLog {
	web.tasksOnce.Do(func() {
		if web.tasks == nil {
			web.tasks = loadTaskLog(web.dataFolder)
		}
	})
	return web.tasks
}

// startTask records a task that receives the progress reported by UpdateProgress.
func (web *Web) startTask(kind string, trigger string) int64 {
	id := web.taskLog().Start(kind, trigger)
	web.currentTask.Store(id)
	return id
}

func (web *Web) finishTask(id int64, failure *TaskNote) {
	web.currentTask.CompareAndSwap(id, 0)
	web.taskLog().Finish(id, failure)
}

// buildSwitchDb downloads the titles and versions databases if they changed. When neither
// changed, the current database is reused instead of processing the files again.
func (web *Web) buildSwitchDb(current *db.SwitchTitlesDB) (*db.SwitchTitlesDB, error) {
	settingsObj := settings.ReadSettings(web.dataFolder)

	web.UpdateProgress(0, 4, "Downloading titles database...")
	filename := filepath.Join(web.dataFolder, settings.TITLE_JSON_FILENAME)
	titles, err := db.LoadAndUpdate(settingsObj.TitlesJsonUrls(), filename, settingsObj.TitlesEtag)

	if err != nil {
		return nil, errors.New("failed to download switch titles [reason:" + err.Error() + "]")
	}
	defer titles.File.Close()

	web.UpdateProgress(1, 4, "Downloading versions database...")
	filename = filepath.Join(web.dataFolder, settings.VERSIONS_JSON_FILENAME)
	versions, err := db.LoadAndUpdate(settingsObj.VersionsJsonUrls(), filename, settingsObj.VersionsEtag)

	if err != nil {
		return nil, errors.New("failed to download switch updates [reason:" + err.Error() + "]")
	}
	defer versions.File.Close()

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.TitlesEtag = titles.Etag
		s.VersionsEtag = versions.Etag
	})

	if current != nil && !titles.Updated && !versions.Updated {
		web.sugarLogger.Info("The titles database did not change")
		web.UpdateProgress(3, 4, "Scanning library...")
		// a copy, so pages reading the current database are not affected
		unchanged := *current
		unchanged.Localized = web.loadLocalizedTitles(true)
		return &unchanged, nil
	}

	web.UpdateProgress(2, 4, "Processing titles and updates...")
	switchTitleDB, err := db.CreateSwitchTitleDB(titles.File, versions.File)
	if err == nil {
		switchTitleDB.Localized = web.loadLocalizedTitles(true)
	}

	web.UpdateProgress(3, 4, "Scanning library...")

	return switchTitleDB, err
}

// buildLocalDB scans the configured folders. It does not change the shared state.
func (web *Web) buildLocalDB(switchDB *db.SwitchTitlesDB, ignoreCache bool) (*db.LocalSwitchFilesDB, error) {
	settingsObj := settings.ReadSettings(web.dataFolder)
	if ignoreCache {
		// remember the keys this scan uses
		web.localDbManager.KeysChanged(settings.KeysFingerprint())
	}

	return web.localDbManager.CreateLocalSwitchFilesDB(switchDB, web.dataFolder, scanFolders(settingsObj), web, true, ignoreCache)
}

// loadLocalizedTitles loads the names and descriptions of the title languages. With
// download, newer files are fetched first. Failures only lose the translations: English
// names are used instead.
func (web *Web) loadLocalizedTitles(download bool) map[string]map[string]db.LocalizedTitle {
	result := map[string]map[string]db.LocalizedTitle{}
	for _, lang := range web.titleLanguages() {
		if titles, ok := web.loadTitleLanguage(lang, download); ok {
			result[lang] = titles
		}
	}
	return result
}

// loadTitleLanguage loads titles.<lang>.json, downloading a newer one first if asked.
func (web *Web) loadTitleLanguage(lang string, download bool) (map[string]db.LocalizedTitle, bool) {
	web.languages.fileMutex.Lock()
	defer web.languages.fileMutex.Unlock()
	settingsObj := settings.ReadSettings(web.dataFolder)
	filePath := filepath.Join(web.dataFolder, "titles."+lang+".json")

	var file *os.File
	var err error
	if download {
		etag := settingsObj.LocalizedTitlesEtags[lang]
		if _, statErr := os.Stat(filePath); statErr != nil {
			etag = ""
		}
		var newEtag string
		file, newEtag, err = db.LoadAndUpdateFile([]string{fmt.Sprintf(settingsObj.LocalizedTitlesJsonUrl, lang)}, filePath, etag)
		if err == nil && newEtag != etag {
			settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
				if s.LocalizedTitlesEtags == nil {
					s.LocalizedTitlesEtags = map[string]string{}
				}
				s.LocalizedTitlesEtags[lang] = newEtag
			})
		}
	} else {
		file, err = os.Open(filePath)
	}
	if err != nil {
		if download || !os.IsNotExist(err) {
			web.sugarLogger.Warnf("Titles in %v are not available: %v", lang, err)
		}
		return nil, false
	}

	titles, err := db.LoadLocalizedTitles(file)
	file.Close()
	if err != nil {
		web.sugarLogger.Warnf("Failed to read titles in %v: %v", lang, err)
		return nil, false
	}
	return titles, true
}

// missingUpdates are the missing updates of the library, respecting the ignore lists.
func (web *Web) missingUpdates() map[string]process.IncompleteTitle {
	return web.derived("missingUpdates", func() any {
		switchDB, localDB := web.state.get()
		if switchDB == nil || localDB == nil {
			return map[string]process.IncompleteTitle{}
		}
		settingsObj := settings.ReadSettings(web.dataFolder)
		return process.ScanForMissingUpdates(localDB.TitlesMap, switchDB.TitlesMap, toLowerSet(settingsObj.IgnoreUpdateTitleIds), settingsObj.IgnoreDLCUpdates)
	}).(map[string]process.IncompleteTitle)
}

// missingDLC are the games with missing DLC, respecting the ignore list.
func (web *Web) missingDLC() map[string]process.IncompleteTitle {
	return web.derived("missingDLC", func() any {
		switchDB, localDB := web.state.get()
		if switchDB == nil || localDB == nil {
			return map[string]process.IncompleteTitle{}
		}
		settingsObj := settings.ReadSettings(web.dataFolder)
		return process.ScanForMissingDLC(localDB.TitlesMap, switchDB.TitlesMap, toLowerSet(settingsObj.IgnoreDLCTitleIds))
	}).(map[string]process.IncompleteTitle)
}
