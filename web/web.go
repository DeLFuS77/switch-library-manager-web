package web

import (
	"embed"
	"errors"
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	router         *mux.Router
	embedFS        embed.FS
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
	Name             string
	Region           string
	ReleaseDate      time.Time
	Type             string
	Version          string
}

type GlobalPageData struct {
	IsKeysFileAvailable bool
	IsSynchronizing     bool
	Page                string
	SlmVersion          string
	Version             string
}

type TitleItemsPageData struct {
	GlobalPageData
	TitleItems []TitleItem
	Filter     *TitleItemFilter
	Pagination pagination.Pagination
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
	"formatTime": func(value time.Time) string {
		if value.IsZero() {
			return ""
		}

		return value.Format("2006-01-02")
	},
	"pageUrl": func(filter *TitleItemFilter, page int) template.URL {
		values := url.Values{}
		if filter.Keyword != "" {
			values.Set("q", filter.Keyword)
		}
		values.Set("per_page", strconv.Itoa(filter.PerPage))
		values.Set("sort_by", filter.SortBy)
		values.Set("sort_order", filter.SortOrder)
		values.Set("page", strconv.Itoa(page))
		// url.Values.Encode escapes every value, so the result is safe to use as is
		return template.URL("?" + values.Encode())
	},
	"subtract": func(a, b int) int {
		return a - b
	},
	"toLower": strings.ToLower,
}

func (web *Web) globalPageData(page string) GlobalPageData {
	return GlobalPageData {
		IsKeysFileAvailable: settings.IsKeysFileAvailable(),
		IsSynchronizing: web.state.IsSynchronizing(),
		Page: page,
		SlmVersion: settings.SLM_VERSION,
		Version: settings.SLM_WEB_VERSION,
	}
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
	titleFilePath := filepath.Join(web.dataFolder, settings.TITLE_JSON_FILENAME)
	versionsFilePath := filepath.Join(web.dataFolder, settings.VERSIONS_JSON_FILENAME)

	var switchDB *db.SwitchTitlesDB
	if titleFile, err := os.Open(titleFilePath); err == nil {
		if versionsFile, err := os.Open(versionsFilePath); err == nil {
			if switchDB, err = db.CreateSwitchTitleDB(titleFile, versionsFile); err != nil {
				web.sugarLogger.Errorf("Failed to read cached titles, please synchronize - %v", err)
			}
			versionsFile.Close()
		}
		titleFile.Close()
	}

	localDbManager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		web.sugarLogger.Error("Failed to create local files db\n", err)
		return
	}

	_, err = settings.InitSwitchKeys(web.dataFolder)
	if err != nil {
		web.sugarLogger.Errorf("Failed to initialize switch keys: %s", err)
	}

	web.localDbManager = localDbManager
	defer localDbManager.Close()

	if switchDB != nil {
		localDB, err := web.buildLocalDB(switchDB, false)
		if err != nil {
			web.sugarLogger.Error(err)
		}
		web.state.set(switchDB, localDB)
	} else {
		// first start (or titles cache lost): fetch the titles database right away
		web.Synchronize()
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

	web.router.Handle("/", http.RedirectHandler("/index.html", http.StatusMovedPermanently))

	http.Handle("/", web.router)

	var handler http.Handler = http.DefaultServeMux
	username, password, authEnabled, err := authFromEnv()
	if err != nil {
		web.sugarLogger.Error(err)
		log.Fatal(err)
	}
	if authEnabled {
		handler = basicAuth(username, password, handler)
		web.sugarLogger.Info("[Authentication enabled]")
	}

	web.sugarLogger.Info("[SLM started]")

	if err := http.ListenAndServe(fmt.Sprint(":", web.appSettings.Port), handler); err != nil {
		web.sugarLogger.Error(fmt.Errorf("running http server failed: %w", err))
		log.Fatal(err)
	}
}

func (web *Web) UpdateProgress(curr int, total int, message string) {
	web.sugarLogger.Debugf("%v (%v/%v)", message, curr, total)
	web.state.setProgress(curr, total, message)
}

func (web *Web) buildSwitchDb() (*db.SwitchTitlesDB, error) {
	settingsObj := settings.ReadSettings(web.dataFolder)

	web.UpdateProgress(0, 4, "Downloading titles database...")
	filename := filepath.Join(web.dataFolder, settings.TITLE_JSON_FILENAME)
	titleFile, titlesEtag, err := db.LoadAndUpdateFile(settingsObj.TitlesJsonUrls(), filename, settingsObj.TitlesEtag)

	if err != nil {
		return nil, errors.New("failed to download switch titles [reason:" + err.Error() + "]")
	}
	defer titleFile.Close()

	settingsObj.TitlesEtag = titlesEtag

	web.UpdateProgress(1, 4, "Downloading versions database...")
	filename = filepath.Join(web.dataFolder, settings.VERSIONS_JSON_FILENAME)
	versionsFile, versionsEtag, err := db.LoadAndUpdateFile(settingsObj.VersionsJsonUrls(), filename, settingsObj.VersionsEtag)

	if err != nil {
		return nil, errors.New("failed to download switch updates [reason:" + err.Error() + "]")
	}
	defer versionsFile.Close()

	settingsObj.VersionsEtag = versionsEtag

	settings.SaveSettings(settingsObj, web.dataFolder)

	web.UpdateProgress(2, 4, "Processing titles and updates...")
	switchTitleDB, err := db.CreateSwitchTitleDB(titleFile, versionsFile)

	web.UpdateProgress(3, 4, "Scanning library...")

	return switchTitleDB, err
}

// buildLocalDB scans the configured folders. It does not change the shared state.
func (web *Web) buildLocalDB(switchDB *db.SwitchTitlesDB, ignoreCache bool) (*db.LocalSwitchFilesDB, error) {
	settingsObj := settings.ReadSettings(web.dataFolder)

	scanFolders := []string{}
	for _, folder := range append([]string{settingsObj.Folder}, settingsObj.ScanFolders...) {
		if folder != "" {
			scanFolders = append(scanFolders, folder)
		}
	}

	return web.localDbManager.CreateLocalSwitchFilesDB(switchDB, web.dataFolder, scanFolders, web, true, ignoreCache)
}
