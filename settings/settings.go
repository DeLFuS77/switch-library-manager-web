package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// The settings are read by many goroutines: the current settings are never changed in
// place, a change saves a new copy (see UpdateSettings).
var (
	settingsInstance atomic.Pointer[AppSettings]
	loadMutex        sync.Mutex
)

const (
	SETTINGS_FILENAME      = "settings.json"
	TITLE_JSON_FILENAME    = "titles.json"
	VERSIONS_JSON_FILENAME = "versions.json"
	SLM_VERSION            = "1.4.0"
	SLM_WEB_VERSION        = "1.25.2"
	REPOSITORY_OWNER       = "DeLFuS77"
	// titles.json and versions.json are generated from blawar/titledb by the
	// "Update title data" workflow of this repository
	DEFAULT_TITLES_JSON_URL   = "https://github.com/DeLFuS77/switch-library-manager-web/releases/download/data/titles.json"
	DEFAULT_VERSIONS_JSON_URL = "https://raw.githubusercontent.com/blawar/titledb/master/versions.json"
	// covers of other stores for titles without one in the US store
	DEFAULT_COVERS_JSON_URL = "https://github.com/DeLFuS77/switch-library-manager-web/releases/download/data/titles.covers.json"
	// %s is replaced by the interface language, e.g. titles.es.json
	DEFAULT_LOCALIZED_TITLES_JSON_URL = "https://github.com/DeLFuS77/switch-library-manager-web/releases/download/data/titles.%s.json"
	DEFAULT_TITLES_ETAG               = "W/\"a5b02845cf6bd61:0\""
	DEFAULT_VERSIONS_ETAG             = "W/\"2ef50d1cb6bd61:0\""
)

// Mirrors tried in order when the configured URL fails.
var (
	FALLBACK_TITLES_JSON_URLS = []string{
		"https://github.com/trembon/switch-library-manager/releases/download/data/titles.json",
	}
	FALLBACK_VERSIONS_JSON_URLS = []string{
		"https://github.com/DeLFuS77/switch-library-manager-web/releases/download/data/versions.json",
	}
)

const (
	DEFAULT_FOLDER_NAME_TEMPLATE = "{TITLE_NAME}"
	DEFAULT_FILE_NAME_TEMPLATE   = "{TITLE_NAME} ({DLC_NAME})[{TITLE_ID}][v{VERSION}]"
)

const (
	TEMPLATE_TITLE_ID    = "TITLE_ID"
	TEMPLATE_TITLE_NAME  = "TITLE_NAME"
	TEMPLATE_DLC_NAME    = "DLC_NAME"
	TEMPLATE_VERSION     = "VERSION"
	TEMPLATE_REGION      = "REGION"
	TEMPLATE_VERSION_TXT = "VERSION_TXT"
	TEMPLATE_TYPE        = "TYPE"
)

type OrganizeOptions struct {
	CreateFolderPerGame        bool   `json:"create_folder_per_game"`
	DlcFolder                  string `json:"dlc_folder"`
	UpdatesFolder              string `json:"updates_folder"`
	ProcessWhenMissingBaseGame bool   `json:"process_when_missing_base_game"`
	DeleteDuplicateFiles       bool   `json:"delete_duplicate_files"`
	RenameFiles                bool   `json:"rename_files"`
	DeleteEmptyFolders         bool   `json:"delete_empty_folders"`
	DeleteOldUpdateFiles       bool   `json:"delete_old_update_files"`
	FolderNameTemplate         string `json:"folder_name_template"`
	SwitchSafeFileNames        bool   `json:"switch_safe_file_names"`
	FileNameTemplate           string `json:"file_name_template"`
}

// NotificationOptions configures the messages sent when updates or DLC become available.
type NotificationOptions struct {
	DiscordWebhookUrl string `json:"discord_webhook_url"`
	TelegramBotToken  string `json:"telegram_bot_token"`
	TelegramChatId    string `json:"telegram_chat_id"`
	WebhookUrl        string `json:"webhook_url"`
	NotifyUpdates     bool   `json:"notify_updates"`
	NotifyDlc         bool   `json:"notify_dlc"`
	// wished games that are released or get new DLC
	NotifyWishlist bool `json:"notify_wishlist"`
	// games and DLC that appear in the folders
	NotifyNewGames bool `json:"notify_new_games"`
}

type AppSettings struct {
	VersionsJsonUrl        string            `json:"versions_json_url"`
	VersionsEtag           string            `json:"versions_etag"`
	TitlesJsonUrl          string            `json:"titles_json_url"`
	TitlesEtag             string            `json:"titles_etag"`
	LocalizedTitlesJsonUrl string            `json:"localized_titles_json_url"`
	LocalizedTitlesEtags   map[string]string `json:"localized_titles_etags"`
	Prodkeys               string            `json:"prod_keys"`
	Folder                 string            `json:"folder"`
	ScanFolders            []string          `json:"scan_folders"`
	Port                   int               `json:"port"`
	Debug                  bool              `json:"debug"`
	OrganizeOptions        OrganizeOptions   `json:"organize_options"`
	IgnoreDLCTitleIds      []string          `json:"ignore_dlc_title_ids"`
	IgnoreUpdateTitleIds   []string          `json:"ignore_update_title_ids"`
	IgnoreDLCUpdates       bool              `json:"ignore_dlc_updates"`
	IgnoreFileTypes        []string          `json:"ignore_file_types"`
	HideDemoGames          bool              `json:"hide_demo_games"`
	WatchFolders           bool              `json:"watch_folders"`
	// hours of heavy background work, e.g. "1-7"; empty for any time
	BackgroundHours string `json:"background_hours"`
	// how often the folders are checked for changes notifications do not report
	WatchIntervalMinutes int `json:"watch_interval_minutes"`
	// automatic compression: "" (off), "new" (when files appear) or "night"
	AutoCompress      string `json:"auto_compress"`
	AutoCompressLevel string `json:"auto_compress_level"`
	// keep the originals of automatic compressions
	AutoCompressKeep bool `json:"auto_compress_keep"`
	// ask GitHub once a day whether a newer version of the app was released
	CheckForUpdates bool `json:"check_for_updates"`
	// firmware of the user's console, e.g. 18.1.0, to warn about files that need a newer one
	ConsoleFirmware string `json:"console_firmware"`
	// days between scheduled verifications of the files; 0 disables them
	VerifyIntervalDays int `json:"verify_interval_days"`
	// how many files a verification checks at the same time: low, normal (empty) or fast
	VerifySpeed string `json:"verify_speed,omitempty"`
	// files read at the same time while scanning; 0 picks a default
	ScanWorkers       int                 `json:"scan_workers"`
	SyncIntervalHours int                 `json:"sync_interval_hours"`
	Language          string              `json:"language"`
	Notifications     NotificationOptions `json:"notifications"`
	LastSyncTime      time.Time           `json:"last_sync_time"`
}

func ReadSettings(dataFolder string) *AppSettings {
	if current := settingsInstance.Load(); current != nil {
		return current
	}
	loadMutex.Lock()
	defer loadMutex.Unlock()
	if current := settingsInstance.Load(); current != nil {
		return current
	}
	// defaults for keys missing from settings files written by older versions
	loaded := &AppSettings{Debug: false, ScanFolders: []string{}, WatchFolders: true, CheckForUpdates: true,
		Notifications:   NotificationOptions{NotifyUpdates: true, NotifyDlc: true, NotifyWishlist: true, NotifyNewGames: true},
		OrganizeOptions: OrganizeOptions{SwitchSafeFileNames: true}, Prodkeys: "", IgnoreDLCTitleIds: []string{"01007F600B135007"}}
	if _, err := os.Stat(filepath.Join(dataFolder, SETTINGS_FILENAME)); err == nil {
		file, err := os.Open(filepath.Join(dataFolder, SETTINGS_FILENAME))
		if err != nil {
			zap.S().Warnf("Missing or corrupted config file, creating a new one")
			return saveDefaultSettings(dataFolder)
		} else {
			err = json.NewDecoder(file).Decode(loaded)
			file.Close()
			if err != nil {
				zap.S().Warnf("Corrupted config file, creating a new one - %v", err)
				return saveDefaultSettings(dataFolder)
			}
			verified := verifySettings(dataFolder, loaded)
			settingsInstance.Store(verified)
			return verified
		}
	} else {
		return saveDefaultSettings(dataFolder)
	}
}

// verifySettings fills in values missing from settings files written by older versions.
// Ported from https://github.com/trembon/switch-library-manager
// CONTAINER_GAMES_FOLDER is where the Docker image mounts the games.
const CONTAINER_GAMES_FOLDER = "/mnt/roms"

// defaultGamesFolder is the folder of the games of the container, or none on a computer: the
// user then chooses it in Settings.
func defaultGamesFolder() string {
	if info, err := os.Stat(CONTAINER_GAMES_FOLDER); err == nil && info.IsDir() {
		return CONTAINER_GAMES_FOLDER
	}
	return ""
}

func verifySettings(dataFolder string, settings *AppSettings) *AppSettings {
	// settings written on a computer by an older version have the folder of the container,
	// which does not exist there
	if settings.Folder == CONTAINER_GAMES_FOLDER && defaultGamesFolder() == "" {
		settings.Folder = ""
	}
	// the repository moved to another account: follow it
	settings.TitlesJsonUrl = movedRepositoryUrl(settings.TitlesJsonUrl)
	settings.VersionsJsonUrl = movedRepositoryUrl(settings.VersionsJsonUrl)
	settings.LocalizedTitlesJsonUrl = movedRepositoryUrl(settings.LocalizedTitlesJsonUrl)

	// the databases are only downloaded over https (a restored backup could name any address)
	if !strings.HasPrefix(settings.TitlesJsonUrl, "https://") {
		settings.TitlesJsonUrl = DEFAULT_TITLES_JSON_URL
	}
	if !strings.HasPrefix(settings.VersionsJsonUrl, "https://") {
		settings.VersionsJsonUrl = DEFAULT_VERSIONS_JSON_URL
	}
	if !strings.HasPrefix(settings.LocalizedTitlesJsonUrl, "https://") {
		settings.LocalizedTitlesJsonUrl = DEFAULT_LOCALIZED_TITLES_JSON_URL
	}
	if settings.OrganizeOptions.FolderNameTemplate == "" {
		settings.OrganizeOptions.FolderNameTemplate = DEFAULT_FOLDER_NAME_TEMPLATE
	}
	if settings.OrganizeOptions.FileNameTemplate == "" {
		settings.OrganizeOptions.FileNameTemplate = DEFAULT_FILE_NAME_TEMPLATE
	}

	// without a local copy the etag would prevent downloading the file again
	if _, err := os.Stat(filepath.Join(dataFolder, TITLE_JSON_FILENAME)); err != nil {
		settings.TitlesEtag = DEFAULT_TITLES_ETAG
	}
	if _, err := os.Stat(filepath.Join(dataFolder, VERSIONS_JSON_FILENAME)); err != nil {
		settings.VersionsEtag = DEFAULT_VERSIONS_ETAG
	}

	return settings
}

// sha256 of the account that owned the repository before it moved
var previousOwnerHash = "6bd7cb1f52a2bba2c6cb01657035d346617bc6c980b92b0ae1f3d016288a927c"

// movedRepositoryUrl points URLs of this repository under its previous account to the
// current one, so settings saved by older versions keep working.
func movedRepositoryUrl(url string) string {
	const prefix = "https://github.com/"
	if !strings.HasPrefix(url, prefix) {
		return url
	}
	rest := url[len(prefix):]
	slash := strings.Index(rest, "/")
	if slash <= 0 || !strings.HasPrefix(rest[slash:], "/switch-library-manager-web/") {
		return url
	}
	owner := sha256.Sum256([]byte(strings.ToLower(rest[:slash])))
	if hex.EncodeToString(owner[:]) != previousOwnerHash {
		return url
	}
	return prefix + REPOSITORY_OWNER + rest[slash:]
}

// TitlesJsonUrls returns the configured titles.json URL followed by the fallback mirrors.
func (s *AppSettings) TitlesJsonUrls() []string {
	return withFallbacks(s.TitlesJsonUrl, FALLBACK_TITLES_JSON_URLS)
}

// VersionsJsonUrls returns the configured versions.json URL followed by the fallback mirrors.
func (s *AppSettings) VersionsJsonUrls() []string {
	return withFallbacks(s.VersionsJsonUrl, FALLBACK_VERSIONS_JSON_URLS)
}

func withFallbacks(url string, fallbacks []string) []string {
	urls := []string{url}
	for _, fallback := range fallbacks {
		if fallback != url {
			urls = append(urls, fallback)
		}
	}
	return urls
}

func saveDefaultSettings(dataFolder string) *AppSettings {
	defaults := &AppSettings{
		TitlesJsonUrl:          DEFAULT_TITLES_JSON_URL,
		TitlesEtag:             DEFAULT_TITLES_ETAG,
		VersionsJsonUrl:        DEFAULT_VERSIONS_JSON_URL,
		LocalizedTitlesJsonUrl: DEFAULT_LOCALIZED_TITLES_JSON_URL,
		VersionsEtag:           DEFAULT_VERSIONS_ETAG,
		Prodkeys:               "", // empty: look for prod.keys in the data folder, then ~/.switch
		Folder:                 defaultGamesFolder(),
		ScanFolders:            []string{},
		IgnoreDLCTitleIds:      []string{},
		IgnoreUpdateTitleIds:   []string{},
		IgnoreFileTypes:        []string{},
		WatchFolders:           true,
		CheckForUpdates:        true,
		Notifications:          NotificationOptions{NotifyUpdates: true, NotifyDlc: true, NotifyWishlist: true, NotifyNewGames: true},
		Port:                   3000,
		Debug:                  false,
		OrganizeOptions: OrganizeOptions{
			RenameFiles:          false,
			CreateFolderPerGame:  false,
			FolderNameTemplate:   DEFAULT_FOLDER_NAME_TEMPLATE,
			FileNameTemplate:     DEFAULT_FILE_NAME_TEMPLATE,
			DeleteEmptyFolders:   false,
			SwitchSafeFileNames:  true,
			DeleteOldUpdateFiles: false,
		},
	}
	return SaveSettings(defaults, dataFolder)
}

var updateMutex sync.Mutex

// version changes every time the settings are saved, so results computed from them can
// be cached until then
var version atomic.Uint64

// Version identifies the saved settings.
func Version() uint64 {
	return version.Load()
}

// ReloadSettings reads the settings file again, after it was replaced.
func ReloadSettings(dataFolder string) *AppSettings {
	updateMutex.Lock()
	defer updateMutex.Unlock()
	settingsInstance.Store(nil)
	appSettings := ReadSettings(dataFolder)
	version.Add(1)
	return appSettings
}

// UpdateSettings applies change to the current settings and saves them. Concurrent
// updates are serialized so one request cannot overwrite the change of another.
func UpdateSettings(dataFolder string, change func(settings *AppSettings)) *AppSettings {
	updateMutex.Lock()
	defer updateMutex.Unlock()
	// a copy: other goroutines may be reading the current settings
	appSettings := *ReadSettings(dataFolder)
	appSettings.LocalizedTitlesEtags = maps.Clone(appSettings.LocalizedTitlesEtags)
	change(&appSettings)
	return SaveSettings(&appSettings, dataFolder)
}

// changesLists reports whether a change of the settings can change the lists of the app:
// the time of the last synchronization, the versions of the downloaded files, the organize
// options and the notifications do not.
func changesLists(before *AppSettings, after *AppSettings) bool {
	a, b := *before, *after
	for _, s := range []*AppSettings{&a, &b} {
		s.LastSyncTime = time.Time{}
		s.TitlesEtag, s.VersionsEtag = "", ""
		s.LocalizedTitlesEtags = nil
		s.OrganizeOptions = OrganizeOptions{}
		s.Notifications = NotificationOptions{}
	}
	return !reflect.DeepEqual(a, b)
}

func SaveSettings(settings *AppSettings, dataFolder string) *AppSettings {
	file, _ := json.MarshalIndent(settings, "", " ")
	// the settings hold notification tokens: only the app reads them
	path := filepath.Join(dataFolder, SETTINGS_FILENAME)
	if err := os.WriteFile(path, file, 0600); err != nil {
		zap.S().Errorf("Failed to save settings - %v", err)
	}
	// a file written by an older version keeps its permissions otherwise
	os.Chmod(path, 0600)
	previous := settingsInstance.Swap(settings)
	if previous == nil || changesLists(previous, settings) {
		version.Add(1)
	}
	return settings
}
