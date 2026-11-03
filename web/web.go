package web

import (
	"embed"
	"errors"
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/launch"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"slices"
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
	// the message in the language of the request
	Text string `json:"text,omitempty"`
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
	covers         coverLoader
	background     backgroundWork
	coverFallbacks map[string]coverFallback
	wish           *userdata.Wishlist
	wishOnce       sync.Once
	activity       *activity.Log
	activityOnce   sync.Once
	hist           *libraryHistory
	historyOnce    sync.Once
	// the copies chosen of the games of several regions
	regionChoice     *regionChoices
	regionChoiceOnce sync.Once
	// the names, covers and descriptions changed by hand (see overrides.go)
	overrides     *titleOverrides
	overridesOnce sync.Once
	// how long the games take to beat, from IGDB (see igdb.go)
	timeToBeat     *timeToBeatStore
	timeToBeatOnce sync.Once
	igdbSeriesOnce sync.Once
	igdbSeries     *igdbCollectionStore
	seriesFile     seriesFile
	cheatsFile     cheatsFile
	// the games being uploaded from the browser (see upload.go)
	uploads uploadStore
	// one check of the notifications at a time: two would send the same items
	notifying sync.Mutex
	// every cover the Tasks page found for a file, by its name: kept while a scan rebuilds the
	// library, and for a file compressed since, see fileCovers
	knownCovers sync.Map
	// results of switchfs.HoldsSameContents, see holdsSameContents
	sameContents      sync.Map
	sameContentsCount atomic.Int64
	// the titles a file holds, by its path, size and time: read once for the duplicates
	titlesHeld        sync.Map
	timeToBeatRunning atomic.Bool
	// the files waiting for the automations (see automation.go)
	automationQueue *automationQueue
	automationOnce  sync.Once
	// files that could not be compressed (see uncompressible.go)
	uncompressibleStore *uncompressibleStore
	uncompressibleOnce  sync.Once
	// the files checked for their title key (see titlekeys.go)
	titleKeysStore *titleKeyStore
	titleKeysOnce  sync.Once
	ratiosStore    *ratioStore
	ratiosOnce     sync.Once
	// the save vault: verified passwords and what was read from the backups (see vault.go)
	vault           vaultAuth
	vaultFiles      vaultIndex
	coll            *userdata.Collections
	collectionsOnce sync.Once
	fav             *userdata.Favorites
	favoritesOnce   sync.Once
	// how the games go for each user (see play.go)
	playStore *userdata.PlayStore
	playOnce  sync.Once
	// the filters of the Library kept under a name (see smartcollections.go)
	smartCollectionsStore *smartCollectionStore
	smartCollectionsOnce  sync.Once
	// the history and the lists prepared after a scan (tests wait for them)
	afterScanWork sync.WaitGroup
	// scans and the work after them that are still running, for the tests to wait for them
	backgroundWork atomic.Int32
	// a scan was started at least once
	backgroundStarted atomic.Bool
	// the fingerprint of the folders the watcher asked to scan (see saveWatchFingerprint)
	watchFingerprint atomic.Uint64
	// the scans of the library that ended well, for the watcher to know one followed a task
	// on the files
	scansDone atomic.Uint64
	// the last scan the app asked for after changing the files itself (see noteAppScan)
	appScan atomic.Pointer[appScanRequest]
	// when the scan running or last run started, and a rescan asked while it ran (the files
	// changed meanwhile, e.g. a compression ended), done once it ends
	scanStarted   atomic.Int64
	rescanPending atomic.Bool
	// a measure of the server is running
	measuring atomic.Bool
	// the app was asked to stop: no task starts any more (see Stop)
	stopping atomic.Bool
	// the server of the pages, closed before an update starts the new program
	server *http.Server
	// the content of the Space page is being prepared
	spacePreparing atomic.Bool
	// the changes of Organize are being counted for the tune-up
	organizePreparing atomic.Bool
	// the last content of the Space page and the last count of the changes of Organize
	// (-1: none yet), shown while newer ones are worked out
	lastSpace    atomic.Pointer[SpacePageData]
	lastOrganize atomic.Int64
	// the volumes of the library short of free space
	disks         diskWatch
	fallbackMutex sync.Mutex
	thumbs        *thumbnails
	thumbsOnce    sync.Once
	store         *db.TitleStore
	storeOnce     sync.Once
	remote        *remoteCovers
	remoteOnce    sync.Once
	languages     titleLanguages
	auto          autoCompressor
	updates       updateChecker
	verify        *verifyStore
	verifyOnce    sync.Once
	// the files deleted that wait in the quarantine
	quarantineItems *quarantineStore
	quarantineOnce  sync.Once
	compressor      compressor
	// files are being organized or deleted by Organize: file tasks wait
	organizing atomic.Bool
	// the files deleted so far by the cleanup running, for the page to take them off its list
	removing removedSoFar
	// where the files moved by Organize went, for the lists made before
	moves     movedFiles
	cache     derivedCache
	auth      *Auth
	tasks     *tasklog.Log
	tasksOnce sync.Once
	// the tasks on files waiting for their turn
	tasksQueue *taskQueue
	queueOnce  sync.Once
	// what IGDB told of the games titles.json does not list
	igdbTitlesStore *igdbTitleStore
	igdbTitlesOnce  sync.Once
	// the task that receives the progress of the running synchronization, 0 if none
	currentTask atomic.Int64
	router      *mux.Router
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
	// the game is on the wishlist (Missing Games)
	Wished bool
	// the game is a demo (library)
	Demo bool
	// library: how long the game takes to beat, in seconds (IGDB); 0 when not known
	TimeToBeat int
	// library: the score of the game on IGDB, of the critics or else of the users; 0 when none
	Score int
	// library: the size of the game with its updates and DLC, and when it was found in the folders
	Size  int64
	Added time.Time
	// library: the user's collections of the game, and the card can be selected
	Collections []string
	Selectable  bool
	// library: the game is a favorite, or it is not in the library (shown with the others)
	Favorite bool
	// how the game goes for the user who looks (set on the shown page only)
	Play    userdata.PlayEntry
	Missing bool
	// library: there are cheats for the version of the game the library has, or only for
	// other versions
	Cheats      bool
	CheatsOther bool
	// other regions of the game in the library, when each game counts once, and their flags;
	// the regions it was released in that the library has no copy of
	OtherRegions int
	OtherFlags   []string
	MissingFlags []string
	// regions left out of the cover, to keep its flags few, shown on hover
	HiddenRegions []string
	// games without their base game: the updates and DLC the library has of them
	OrphanUpdates int
	OrphanDlc     int
	// from the titles database: genres, number of players and language codes
	Genres    []string
	Players   int
	Languages []string
	// computed once when a list is sorted (see sorted), for sorting and searching
	sortKey     string
	searchKey   string
	searchWords []string
	// shown as a game of the library on the page of a series
	InLibrary bool
}

// DlcPercent is the share of the DLC of a game in the library.
func (t TitleItem) DlcPercent() int {
	if t.DlcTotal == 0 {
		return 0
	}
	return t.DlcOwned * 100 / t.DlcTotal
}

type GlobalPageData struct {
	// Set by render for the user of the request
	Auth AuthInfo
	// a newer version of the app, if one was released
	Update *AppUpdate
	// a made-up library is shown (SLM_DEMO=true)
	Demo                bool
	IsKeysFileAvailable bool
	IsSynchronizing     bool
	HasLibrary          bool
	Page                string
	SlmVersion          string
	Version             string
	Counts              NavCounts
	// some games have their time to beat (IGDB): the library can be sorted by it
	TimesToBeat bool
	// some games have a score (IGDB): the library can be sorted and filtered by it
	Scores bool
	// the setup wizard opens by itself (first use), for an administrator
	WizardAuto bool
	// the save vault is on: its page is in the menu
	VaultEnabled bool
	// the volumes of the library short of free space
	LowDisks []LowDisk
}

// NavCounts are shown next to the navigation links, so pending work is visible everywhere.
type NavCounts struct {
	Updates int
	Dlc     int
	Issues  int
	// failed tasks that were not dismissed
	FailedTasks int
	// tasks running now, e.g. covers downloaded in the background
	RunningTasks int
	// games on the wishlist
	Wished int
	// games of the titles database that are not in the library, as the Missing Games page counts them
	Missing int
	// a step of the tune-up is waiting: files never checked, changes of Organize, space to free
	TuneUp bool
	// the issues by kind, for the panel of the header
	IssueKinds []IssueKindCount
}

type TitleItemsPageData struct {
	GlobalPageData
	// games on the wishlist (Missing Games)
	WishedCount int
	TitleItems  []TitleItem
	Filter      *TitleItemFilter
	Pagination  pagination.Pagination
	// missing games like the ones the user likes, on the first page without filters
	Recommendations []Recommendation
	// the views of the library: your games, the ones you do not have, or all of them
	Views LibraryViews
}

// LibraryViews are the numbers of the three views of the Library, and the one shown: "mine",
// "missing" (the page that was Missing Games) or "all" (both, the missing ones greyed out).
type LibraryViews struct {
	Current string
	Mine    int
	Missing int
}

// All is every game, those of the library and those it does not have.
func (v LibraryViews) All() int {
	return v.Mine + v.Missing
}

// LibraryFacets are the numbers shown on the status filters of the library, counted
// before the status filter is applied, and the file formats found in the library.
type LibraryFacets struct {
	All      int
	Update   int
	Dlc      int
	Complete int
	Formats  []string
	// counted before the kind and extra filters; the regions found in the library
	Games     int
	Demos     int
	NoCover   int
	Unknown   int
	Recent    int
	Favorites int
	// games with cheats for the version the library has, and for other versions only
	Cheats      int
	CheatsOther int
	// games that need a newer firmware than the console has
	FirmwareTooNew int
	// the games of the user by how they go, and those without a state ("none")
	Play map[string]int
	// games not in the library, shown in the All view
	Missing int
	// genres and languages of the games, the most frequent first, and the games by players
	Genres       []NamedCount
	Languages    []NamedCount
	SinglePlayer int
	TwoPlayers   int
	FourPlayers  int
	// the games by their score: of 70, 80 and 90 or more
	Score70 int
	Score80 int
	Score90 int
	// the collections with their number of games, before the collection filter
	Collections []userdata.CollectionCount
	Regions     []string
	// the settings hide the demos unless the kind filter asks for them
	DemosHidden bool
}

type LibraryPageData struct {
	TitleItemsPageData
	Facets LibraryFacets
	Setup  SetupStatus
	// the overview at the top of the library
	Stats Statistics
	// what is new, once after an update
	WhatsNew *WhatsNew
	// the folders games can be uploaded to
	UploadFolders []string
	// the smart collections, and whether the filters shown can become one
	SmartCollections []SmartCollectionView
	SmartSavable     bool
}

var funcMap = template.FuncMap{
	// the program has the zstd library in C: several threads on each NSZ
	"nativeZstd": func() bool { return switchfs.NativeZstd },
	// mul multiplies, e.g. megabytes into bytes for formatSize
	"mul": func(a, b int) int64 {
		return int64(a) * int64(b)
	},
	"add": func(a, b int) int {
		return a + b
	},
	"fileBase":   fileBase,
	"fileDir":    fileDir,
	"shortPaths": shortPaths,
	"eq":         templateEq,
	// any integers: a size (int64) against 0 stopped the page halfway with "wrong type"
	"gt": func(a, b any) (bool, error) {
		x, y, err := templateInts(a, b)
		return x > y, err
	},
	"lt": func(a, b any) (bool, error) {
		x, y, err := templateInts(a, b)
		return x < y, err
	},
	"mkRange": func(min, max int) []int {
		a := make([]int, max-min+1)
		for i := range a {
			a[i] = min + i
		}
		return a
	},
	"mkSlice": func(args ...interface{}) []interface{} {
		return args
	},
	"neq": func(a, b interface{}) bool {
		return !templateEq(a, b)
	},
	"pageUrl": func(filter *TitleItemFilter, page int) template.URL {
		values := filter.query("page", strconv.Itoa(page))
		values.Set("page", strconv.Itoa(page))
		// url.Values.Encode escapes every value, so the result is safe to use as is
		return template.URL("?" + values.Encode())
	},
	// filterUrl links to the current list with some filter values replaced: filterUrl .Filter "status" "dlc"
	"playStates":    userdata.PlayStates,
	"roleName":      func(role string) string { return roleNames[role] },
	"roleChoices":   RoleChoices,
	"cheatTargets":  func() []cheatTarget { return cheatTargets },
	"playStateName": func(state string) string { return userdata.PlayStateLooks[state].Name },
	"filterUrl": func(filter *TitleItemFilter, replace ...string) template.URL {
		return template.URL("?" + filter.query(replace...).Encode())
	},
	// filterHidden keeps the filter in a form that changes a part of it, as hidden fields
	// besides the fields of the form: filterHidden .Filter "per_page"
	"filterHidden": func(filter *TitleItemFilter, own ...string) template.HTML {
		return filter.hiddenFields(own...)
	},
	"formatSize": i18n.FormatSize,
	// versions as v0, v65536
	"versionList": func(versions []int) string {
		texts := make([]string, len(versions))
		for i, version := range versions {
			texts[i] = "v" + strconv.Itoa(version)
		}
		return strings.Join(texts, ", ")
	},
	"hours":           hoursText,
	"scoreLevel":      scoreLevel,
	"thumb":           thumbUrl,
	"fullImage":       fullImageUrl,
	"maintenanceStep": maintenanceStepOf,
	"formatBytes":     func(size int) string { return i18n.FormatSize(int64(size)) },
	"asset":           func(path string) string { return path + "?v=" + assetVersion },
	// the app runs in a container: its log is the log of the container, not a window
	"inContainer": runningInContainer,
	"mod": func(a, b int) int {
		if b == 0 {
			return 0
		}
		return a % b
	},
	"conic": conicGradient,
	// the first letter of a name, for avatars
	"initial": func(name string) string {
		for _, r := range name {
			return strings.ToUpper(string(r))
		}
		return "?"
	},
	"join":      strings.Join,
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
	// the zones of the stores to prefer, and the ones chosen for each select (see regionZones)
	"regionZones":      func() []RegionZone { return regionZones },
	"preferredChoices": preferredChoices,
	// percentOf64 is part of total in percent, for bars of sizes
	"percentOf64": func(part int64, total int64) int64 {
		if total <= 0 {
			return 0
		}
		return min(part*100/total, 100)
	},
	// sdTerabytes shows a size in GB as terabytes: 1500 is 1.5
	"sdTerabytes": func(gigabytes int) string {
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", float64(gigabytes)/1000), "0"), ".")
	},
	// the change of format of a file in a task on files, e.g. "NSP → NSZ", or ""
	"formatChange": tasklog.FormatChange,
	// percentOf is part of total in percent, for bars
	"percentOf": func(part int, total int) int {
		if total <= 0 {
			return 0
		}
		return part * 100 / total
	},
	// the English name of a language of a game, for t
	"gameLanguage":  gameLanguageName,
	"languageFlags": languageFlags,
	"showRegion":    showRegion,
	// inList reports whether a list of strings contains a value
	"inList": func(value string, list []string) bool {
		for _, item := range list {
			if item == value {
				return true
			}
		}
		return false
	},
}

func (web *Web) globalPageData(page string) GlobalPageData {
	_, localDB := web.state.get()
	return GlobalPageData{
		IsKeysFileAvailable: settings.IsKeysFileAvailable() || isDemoMode(),
		Demo:                isDemoMode(),
		IsSynchronizing:     web.state.IsSynchronizing(),
		HasLibrary:          localDB != nil && len(localDB.TitlesMap) > 0,
		Page:                page,
		SlmVersion:          settings.SLM_VERSION,
		Version:             settings.SLM_WEB_VERSION,
		Counts:              web.navCounts(),
		Update:              web.availableUpdate(),
		TimesToBeat:         web.timesToBeat().hasKnown(),
		Scores:              web.timesToBeat().hasScores(),
		WizardAuto:          web.wizardAuto(),
		VaultEnabled:        vaultEnabled(settings.ReadSettings(web.dataFolder)) || isDemoMode(),
		LowDisks:            web.lowDisks(),
	}
}

// navCounts counts the missing updates and DLC (respecting the ignore lists) and the issues.
func (web *Web) navCounts() NavCounts {
	counts := NavCounts{FailedTasks: web.taskLog().FailedCount(), RunningTasks: web.taskLog().RunningCount(), Wished: web.wishes().Count()}
	switchDB, localDB := web.state.get()
	if localDB == nil {
		return counts
	}
	counts.Issues = len(web.getIssues())
	counts.IssueKinds = web.issueKindCounts()
	if switchDB == nil {
		return counts
	}
	counts.TuneUp = web.tuneUpWaiting()
	counts.Missing = web.missingGamesCount()
	// as the Updates page lists them (the copies kept of games of several regions depend little
	// on the language)
	counts.Updates = len(web.shownMissingUpdates(i18n.DEFAULT_LANGUAGE))
	for _, title := range web.missingDLC() {
		counts.Dlc += len(title.MissingDLCItems)
	}
	return counts
}

// templateEq compares two values of a template; numbers of different types are equal when
// they have the same value, so a size (int64) can be compared with 0.
func templateEq(a, b interface{}) bool {
	if x, ok := templateInt(a); ok {
		if y, ok := templateInt(b); ok {
			return x == y
		}
	}
	return a == b
}

func templateInt(value interface{}) (int64, bool) {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(v.Uint()), true
	}
	return 0, false
}

func intToTime(value int) (time.Time, error) {
	if value <= 0 {
		return time.Time{}, nil
	}
	// some titles only have the year, or the year and the month, of their release
	switch text := strconv.Itoa(value); len(text) {
	case 4:
		return strToTime("2006", text)
	case 6:
		return strToTime("200601", text)
	default:
		return strToTime("20060102", text)
	}
}

func strToTime(layout, value string) (time.Time, error) {
	// the versions database writes "-" for a date it does not know
	if value = strings.TrimSpace(value); value == "" || value == "-" {
		return time.Time{}, nil
	}

	return time.Parse(layout, value)
}

func CreateWeb(router *mux.Router, embedFS embed.FS, appSettings *settings.AppSettings, dataFolder string, sugarLogger *zap.SugaredLogger) *Web {
	return &Web{router: router, embedFS: embedFS, appSettings: appSettings, dataFolder: dataFolder, sugarLogger: sugarLogger}
}

func (web *Web) Start() {
	// before the library database is opened: an installation that had one was updated
	web.recordVersion()
	if web.appSettings.LowPriority {
		web.applyPriority(true)
	}
	// the app answers at once: while it gets ready, every page says it is starting and opens
	// by itself once it is ready (opening the local database and preparing the pages can
	// take a while on a NAS)
	listener, err := listenLimited(fmt.Sprint(":", web.appSettings.Port))
	// started by an update: the program before leaves the port in a moment
	for wait := 0; err != nil && launch.PortInUse(err) && restarted() && wait < 60; wait++ {
		time.Sleep(500 * time.Millisecond)
		listener, err = listenLimited(fmt.Sprint(":", web.appSettings.Port))
	}
	if err != nil {
		// started twice with a double click: the app is already open, show it instead
		if launch.PortInUse(err) {
			url := fmt.Sprintf("http://localhost:%d/", web.appSettings.Port)
			web.sugarLogger.Warnf("Port %d is already in use: the app may be running already. Open %s, or set another \"port\" in settings.json.", web.appSettings.Port, url)
			if launch.ShouldOpenBrowser() {
				launch.OpenUrl(url)
			}
			os.Exit(1)
		}
		web.sugarLogger.Error(fmt.Errorf("running http server failed: %w", err))
		log.Fatal(err)
	}
	front := &switchHandler{}
	icon, _ := fs.ReadFile(web.embedFS, "resources/static/icon.svg")
	front.set(withSecurityHeaders(startingHandler(icon)))
	// timeouts so slow or idle connections cannot pile up; no write timeout, because game
	// downloads and the live task events can take long
	server := &http.Server{
		Handler:           front,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	web.server = server
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	if launch.ShouldOpenBrowser() && !isDemoMode() {
		go launch.OpenBrowserWhenReady(web.appSettings.Port, web.sugarLogger)
	}

	// the names the games give themselves, for names the titles database has only in another script
	lookup := web.libraryNacp
	fileNacp.Store(&lookup)
	setStartingStep("Opening the local database...")
	localDbManager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		web.sugarLogger.Error("Failed to create local files db\n", err)
		return
	}

	setStartingStep("Loading the keys...")
	_, err = settings.InitSwitchKeys(web.dataFolder)
	if err != nil {
		// running without keys is supported, files are then identified by their name
		web.sugarLogger.Warnf("prod.keys not loaded, files are identified by their file name only. Put prod.keys in %s to read their metadata (%s)", web.dataFolder, err)
	}

	web.localDbManager = localDbManager
	localDbManager.CheckedPartly = web.checkedPartly
	defer localDbManager.Close()

	if isDemoMode() {
		web.loadDemo()
	} else {
		web.startInBackground()
	}

	setStartingStep("Checking the users...")
	web.auth, err = newAuth(web.dataFolder)
	if err != nil {
		web.sugarLogger.Error(err)
		log.Fatal(err)
	}
	web.auth.onLogin = web.recordLogin
	web.auth.onKey = web.recordKeyAttempt
	web.auth.onPortal = web.recordPortal
	web.auth.portalSettings = func() settings.PortalOptions { return settings.ReadSettings(web.dataFolder).Portal }
	web.logPortalProblems()

	// Run http server
	setStartingStep("Preparing the pages...")
	web.router.Use(sameOriginOnly)
	web.HandleResources()
	web.HandleImages()
	web.HandleIndex()
	web.HandleMissing()
	web.HandleUpdates()
	web.HandleDLC()
	web.HandleIssues()
	web.HandleSettings()
	web.HandleSelfUpdate()
	web.HandleKeysUpload()
	web.HandleUploads()
	web.HandleSynchronize()
	web.HandleOrganize()
	web.HandleApi()
	web.HandleTitle()
	web.HandleIgnore()
	web.HandleExport()
	web.HandleStatistics()
	web.HandleNotifications()
	web.HandleArchive()
	web.HandleCheats()
	web.HandleApiDocs()
	web.HandleTasks()
	web.HandleUsers()
	web.HandleActivity()
	web.HandleCompress()
	web.HandleMeasure()
	web.HandleVerify()
	web.HandleBackup()
	web.HandleQuarantine()
	web.HandleSpace()
	web.HandleSdCard()
	web.HandleOrphans()
	web.HandleSagas()
	web.HandleUpcoming()
	web.HandleQuickSearch()
	web.HandleYear()
	web.HandleWizard()
	web.HandleSaves()
	web.HandleAutoBackups()
	web.HandleDiagnostics()
	web.HandleDisclaimer()
	web.HandleMaintenance()
	web.HandleCovers()
	web.HandleUpdateGuide()
	web.HandleWishlist()
	web.HandleCollections()
	web.HandlePlay()
	web.HandleSmartCollections()
	web.HandleFavorites()
	if !isDemoMode() {
		web.StartScheduler()
		web.startTelegramBot()
		web.StartFolderWatcher()
		web.StartUpdateChecker()
	}

	web.router.Handle("/", http.RedirectHandler("/index.html", http.StatusMovedPermanently))

	http.Handle("/", web.router)

	handler := web.auth.middleware(web.withActivityLog(limitHeavy(http.DefaultServeMux)))
	if isDemoMode() {
		handler = web.demoReadOnly(handler)
	}
	if err := web.auth.envPasswordWeak; err != nil {
		web.sugarLogger.Warnf("[Weak password] SLM_AUTH_PASSWORD still works, but it is weak: %s Change it to one of at least %d characters that is not common.", err.Error(), minPasswordLength)
	}
	if web.auth.Enabled() {
		web.sugarLogger.Info("[Authentication enabled]")
	} else if web.auth.remoteWithoutLogin {
		web.sugarLogger.Warn("[Authentication disabled] Anyone who can reach the app is an administrator (" + REMOTE_WITHOUT_LOGIN_ENV + "=true). Create an administrator in Users unless another service protects it.")
	} else {
		web.sugarLogger.Warn("[Authentication disabled] The app only answers on the local network until an administrator is created in Users.")
	}
	// the code that creates the first administrator, while there is none
	web.logSetupCode(true)

	// the app is ready: the pages take the place of the page of the start
	front.set(withSecurityHeaders(withBodyLimit(withCompression(withHealthCheck(web.withVault(handler))))))
	web.sugarLogger.Info("[SLM started]")
	if err := <-served; err != nil {
		// closed by an update, which starts the new program and then stops this one
		if errors.Is(err, http.ErrServerClosed) {
			select {}
		}
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
	web.scanStarted.Store(time.Now().UnixNano())
	go func() {
		defer web.rescanIfPending()
		web.UpdateProgress(-1, -1, "Loading titles database...")
		switchDB := web.loadSavedTitles()
		if switchDB == nil {
			web.state.endSync()
			// first start (or titles cache lost): fetch the titles database right away
			web.Synchronize(tasklog.TRIGGER_STARTUP)
			return
		}

		// files that could not be read with the previous keys may be readable now
		rescan := web.localDbManager.KeysChanged(settings.KeysFingerprint())
		if rescan {
			web.sugarLogger.Info("The keys changed since the last scan, the library is scanned again")
		}
		// the pages show the titles while the library is scanned
		web.state.set(web.withLibraryTitles(switchDB, nil), nil)

		taskId := web.startTask(tasklog.TASK_SCAN, tasklog.TRIGGER_STARTUP)
		failure := func() (failure *tasklog.Note) {
			// a panic: the scan of the start fails, the app goes on with the titles it has
			defer func() {
				if recovered := recover(); recovered != nil {
					failure = web.crashed(tasklog.TASK_SCAN, recovered)
				}
			}()
			localDB, err := web.buildLocalDB(switchDB, rescan)
			if err != nil {
				web.sugarLogger.Error(err)
				return &tasklog.Note{Text: tasklog.NOTE_SCAN_FAILED, Detail: err.Error()}
			}
			web.taskLog().SetResult(taskId, len(localDB.TitlesMap), localDB.NumFiles)
			web.state.set(web.withLibraryTitles(switchDB, localDB), localDB)
			return nil
		}()
		web.state.endSync()
		web.finishTask(taskId, failure)
		if failure == nil {
			web.afterScan()
			// the cheats are not waited for until the next synchronization, which can be hours away
			go web.downloadCheats()
		}
		// the task the app was stopped in the middle of goes first, before the new files
		if failure == nil {
			web.resumeInterrupted()
			// then the tasks that were waiting in the queue when the app stopped
			web.runQueue()
		}
		if failure == nil && settings.ReadSettings(web.dataFolder).AutoCompress == AUTO_COMPRESS_NEW {
			web.autoCompress(tasklog.TRIGGER_STARTUP)
		}
		if failure == nil {
			web.resumeAutomation()
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

	titleFile.Close()
	versionsFile.Close()
	switchDB, err := web.readTitles()
	if err != nil {
		web.sugarLogger.Errorf("Failed to read cached titles, please synchronize - %v", err)
		return nil
	}
	web.applyCoverFallbacks(switchDB)
	switchDB.Localized = web.loadLocalizedTitles(false)
	return switchDB
}

func (web *Web) UpdateProgress(curr int, total int, message string) {
	web.sugarLogger.Debugf("%v (%v/%v)", message, curr, total)
	web.state.setProgress(curr, total, message)
	if id := web.currentTask.Load(); id != 0 {
		key, arg := progressMessage(message)
		web.taskLog().ProgressArg(id, curr, total, key, arg)
	}
}

// the steps of a scan, with what they name apart so the tasks page translates them
var progressSteps = []struct{ prefix, key string }{
	{"Found ", "Looking at the folders: %v"},
	{"Reading ", "Reading the new file %v"},
	{"Scanned ", "Folder looked at: %v"},
}

// progressMessage splits a message of the scan into the sentence to translate and the file
// or folder it names.
func progressMessage(message string) (string, string) {
	for _, step := range progressSteps {
		if strings.HasPrefix(message, step.prefix) {
			return step.key, strings.TrimPrefix(message, step.prefix)
		}
	}
	if message == "Complete" {
		return "Done", ""
	}
	return message, ""
}

// messageText translates the message of a task, with what it names in its place: a message
// that is only the name of a file stays as it is.
func messageText(lang string, message string, arg string) string {
	if arg != "" {
		return i18n.Translatef(lang, message, arg)
	}
	return i18n.Translate(lang, message)
}

// taskLog returns the task history, loading it on first use.
func (web *Web) taskLog() *tasklog.Log {
	web.tasksOnce.Do(func() {
		if web.tasks == nil {
			web.tasks = tasklog.Load(web.dataFolder)
		}
		web.tasks.OnFailed = web.notifyTaskFailed
		// the log tells what each task did, not only what went wrong in it
		web.tasks.OnEnded = web.logTaskEnd
	})
	return web.tasks
}

// logTaskEnd writes a line of a task that ended: what, started by what, how it ended and how
// long it took.
func (web *Web) logTaskEnd(task tasklog.Task) {
	how := task.Status
	if task.Error != nil {
		how += ": " + task.Error.Text
		if task.Error.Detail != "" {
			how += " (" + task.Error.Detail + ")"
		}
	} else if len(task.Warnings) > 0 {
		how += fmt.Sprintf(" (%d warnings)", len(task.Warnings))
	}
	web.sugarLogger.Infof("[Task %d] %s (%s) %s in %s", task.Id, task.Kind, task.Trigger, how, task.Duration())
}

// crashed turns a panic of the work of a task into its failure, logged with where it happened:
// the task ends, the files and the scan are let go, and the app goes on instead of stopping
// with every task it runs.
func (web *Web) crashed(what string, recovered any) *tasklog.Note {
	web.sugarLogger.Errorf("%s stopped on an unexpected error: %v\n%s", what, recovered, debug.Stack())
	return &tasklog.Note{Text: tasklog.NOTE_TASK_CRASHED, Detail: fmt.Sprint(recovered)}
}

// finishRunning ends, with a failure, the running tasks of some kinds: their work stopped on a
// panic where the task is not known.
func (web *Web) finishRunning(failure *tasklog.Note, kinds ...string) {
	for _, task := range web.taskLog().Snapshot() {
		if task.Running() && slices.Contains(kinds, task.Kind) {
			web.finishTask(task.Id, failure)
		}
	}
}

// errUnexpected is the error of the work on one file that panicked: that file fails, the task
// goes on with the others.
var errUnexpected = errors.New("unexpected error")

// safely runs the work on one file, a panic on a strange file turned into its error.
func safely[T any](work func() (T, error)) (result T, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: %v", errUnexpected, recovered)
		}
	}()
	return work()
}

// holdScan keeps scans from running while files are moved or deleted (Organize, a cleanup);
// false while one runs. releaseScan lets them run again.
func (web *Web) holdScan() bool {
	return web.state.startSync()
}

// releaseScan lets scans run again after holdScan. A rescan asked meanwhile was left for the end
// Of a scan, and no scan ended: it is done now, or done by the one asked here (a trigger), else
// it would wait for whatever starts the next scan, and a task waiting in the queue with it.
func (web *Web) releaseScan(trigger string) {
	web.state.endSync()
	if trigger != "" {
		web.Rescan(trigger)
		return
	}
	web.rescanIfPending()
}

// Stop is called when the app is asked to stop (docker stop, Ctrl+C, an update): no task starts
// any more, and what the tasks did and have left is saved now, so they go on where they were
// once the app starts again instead of from up to half a minute before.
func (web *Web) Stop() {
	if !web.stopping.CompareAndSwap(false, true) {
		return
	}
	web.compressor.mutex.Lock()
	flush := web.compressor.flush
	web.compressor.mutex.Unlock()
	if flush != nil {
		flush()
	}
	if web.verify != nil {
		if err := web.verify.save(); err != nil {
			web.sugarLogger.Warnf("Failed to save the verification results: %v", err)
		}
	}
	web.taskLog().Flush()
	web.sugarLogger.Info("[SLM stops]")
}

// startTask records a task that receives the progress reported by UpdateProgress.
func (web *Web) startTask(kind string, trigger string) int64 {
	id := web.taskLog().Start(kind, trigger)
	web.currentTask.Store(id)
	return id
}

func (web *Web) finishTask(id int64, failure *tasklog.Note) {
	web.currentTask.CompareAndSwap(id, 0)
	web.taskLog().Finish(id, failure)
}

// buildSwitchDb downloads the titles and versions databases if they changed. When neither
// changed, the current database is reused instead of processing the files again. When the
// titles database could not be downloaded, the error says why, and the database returned, if
// any, is the one of the saved copy. With no connection to the servers nothing else is
// downloaded, so the scan does not wait for every other file to fail too.
func (web *Web) buildSwitchDb(current *db.SwitchTitlesDB) (*db.SwitchTitlesDB, error) {
	settingsObj := settings.ReadSettings(web.dataFolder)

	web.UpdateProgress(0, 4, "Downloading titles database...")
	filename := filepath.Join(web.dataFolder, settings.TITLE_JSON_FILENAME)
	titles, err := db.LoadAndUpdate(settingsObj.TitlesJsonUrls(), filename, settingsObj.TitlesEtag)

	if err != nil {
		return nil, fmt.Errorf("failed to download switch titles [reason:%w]", err)
	}
	defer titles.File.Close()
	var notDownloaded error
	if titles.Failure != nil {
		notDownloaded = fmt.Errorf("failed to download switch titles, the saved copy is used [reason:%w]", titles.Failure)
	}
	offline := errors.Is(titles.Failure, db.ErrNoConnection)
	if offline && current != nil {
		// the titles the app has are the ones of the saved copy already
		return nil, notDownloaded
	}

	web.UpdateProgress(1, 4, "Downloading versions database...")
	filename = filepath.Join(web.dataFolder, settings.VERSIONS_JSON_FILENAME)
	var versions db.UpdateResult
	if offline {
		versions, err = db.LoadLocal(filename, settingsObj.VersionsEtag)
	} else {
		versions, err = db.LoadAndUpdate(settingsObj.VersionsJsonUrls(), filename, settingsObj.VersionsEtag)
	}

	if err != nil {
		return nil, errors.Join(notDownloaded, fmt.Errorf("failed to download switch updates [reason:%w]", err))
	}
	defer versions.File.Close()

	// the versions of the files are kept once they were read: kept for a file that cannot be
	// read, the server would answer "not changed" and the app would never get it again
	keepVersions := func(titlesEtag string, versionsEtag string) {
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			s.TitlesEtag = titlesEtag
			s.VersionsEtag = versionsEtag
		})
	}

	if current != nil && !titles.Updated && !versions.Updated {
		keepVersions(titles.Etag, versions.Etag)
		web.sugarLogger.Info("The titles database did not change")
		web.UpdateProgress(3, 4, "Scanning library...")
		// a copy, so pages reading the current database are not affected: its map too, as the
		// cover fallbacks replace titles in it
		unchanged := *current
		unchanged.TitlesMap = make(map[string]*db.SwitchTitle, len(current.TitlesMap))
		for id, title := range current.TitlesMap {
			unchanged.TitlesMap[id] = title
		}
		unchanged.Localized = web.loadLocalizedTitles(true)
		web.downloadCoverFallbacks()
		web.downloadSeries()
		// a few megabytes, not waited for: the library shows its cheats once they are here
		go web.downloadCheats()
		web.applyCoverFallbacks(&unchanged)
		return &unchanged, notDownloaded
	}

	web.UpdateProgress(2, 4, "Processing titles and updates...")
	titles.File.Close()
	versions.File.Close()
	switchTitleDB, err := web.readTitles()
	if err != nil {
		// downloaded whole the next time
		keepVersions("", "")
	} else {
		keepVersions(titles.Etag, versions.Etag)
		switchTitleDB.Localized = web.loadLocalizedTitles(!offline)
		if !offline {
			web.downloadCoverFallbacks()
			web.downloadSeries()
			// a few megabytes, not waited for: the library shows its cheats once they are here
			go web.downloadCheats()
		}
		web.applyCoverFallbacks(switchTitleDB)
	}

	web.UpdateProgress(3, 4, "Scanning library...")

	return switchTitleDB, errors.Join(notDownloaded, err)
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
// Names are used instead.
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
	// the language names a file of the data folder and goes in the address of the download
	if lang = i18n.SupportedLanguage(lang); lang == "" {
		return nil, false
	}
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
		file, newEtag, err = db.LoadAndUpdateFile(settings.CompressedFirst(fmt.Sprintf(settingsObj.LocalizedTitlesJsonUrl, lang)), filePath, etag)
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

	file.Close()
	titles, err := web.readLocalizedTitles(lang, filePath)
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
		missing := process.ScanForMissingUpdates(localDB.TitlesMap, switchDB.TitlesMap, toLowerSet(settingsObj.IgnoreUpdateTitleIds), settingsObj.IgnoreDLCUpdates)
		return withoutDemos(missing, switchDB, settingsObj.HideDemoGames)
	}).(map[string]process.IncompleteTitle)
}

// withoutDemos removes the demos (and their DLC) when the settings hide them.
func withoutDemos(titles map[string]process.IncompleteTitle, switchDB *db.SwitchTitlesDB, hide bool) map[string]process.IncompleteTitle {
	if !hide {
		return titles
	}
	for key, title := range titles {
		prefix, err := db.TitleIDPrefix(title.Attributes.Id)
		if err != nil {
			continue
		}
		base := switchDB.TitlesMap[prefix]
		name := title.Attributes.Name
		if base != nil {
			name = base.Attributes.Name
		}
		if isDemo(base, name) || localDemo(title.Meta) {
			delete(titles, key)
		}
	}
	return titles
}

// missingDLC are the games with missing DLC, respecting the ignore list.
func (web *Web) missingDLC() map[string]process.IncompleteTitle {
	return web.derived("missingDLC", func() any {
		switchDB, localDB := web.state.get()
		if switchDB == nil || localDB == nil {
			return map[string]process.IncompleteTitle{}
		}
		settingsObj := settings.ReadSettings(web.dataFolder)
		missing := process.ScanForMissingDLC(localDB.TitlesMap, switchDB.TitlesMap, toLowerSet(settingsObj.IgnoreDLCTitleIds))
		return withoutDemos(missing, switchDB, settingsObj.HideDemoGames)
	}).(map[string]process.IncompleteTitle)
}

// templateInts reads two integers of any size for the comparisons of the templates.
func templateInts(a, b any) (int64, int64, error) {
	read := func(value any) (int64, error) {
		v := reflect.ValueOf(value)
		switch v.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return v.Int(), nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return int64(v.Uint()), nil
		}
		return 0, fmt.Errorf("not an integer: %v", value)
	}
	x, err := read(a)
	if err != nil {
		return 0, 0, err
	}
	y, err := read(b)
	return x, y, err
}
