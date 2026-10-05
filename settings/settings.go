package settings

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

var (
	settingsInstance *AppSettings
)

const (
	SETTINGS_FILENAME      = "settings.json"
	TITLE_JSON_FILENAME    = "titles.json"
	VERSIONS_JSON_FILENAME = "versions.json"
	SLM_VERSION            = "1.4.0"
	SLM_WEB_VERSION        = "1.7.0"
	// titles.json and versions.json are generated from blawar/titledb by the
	// "Update title data" workflow of this repository
	DEFAULT_TITLES_JSON_URL   = "https://github.com/SiscuPrats/switch-library-manager-web/releases/download/data/titles.json"
	DEFAULT_VERSIONS_JSON_URL = "https://raw.githubusercontent.com/blawar/titledb/master/versions.json"
	// %s is replaced by the interface language, e.g. titles.es.json
	DEFAULT_LOCALIZED_TITLES_JSON_URL = "https://github.com/SiscuPrats/switch-library-manager-web/releases/download/data/titles.%s.json"
	DEFAULT_TITLES_ETAG               = "W/\"a5b02845cf6bd61:0\""
	DEFAULT_VERSIONS_ETAG             = "W/\"2ef50d1cb6bd61:0\""
)

// Mirrors tried in order when the configured URL fails.
var (
	FALLBACK_TITLES_JSON_URLS = []string{
		"https://github.com/trembon/switch-library-manager/releases/download/data/titles.json",
	}
	FALLBACK_VERSIONS_JSON_URLS = []string{
		"https://github.com/SiscuPrats/switch-library-manager-web/releases/download/data/versions.json",
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
}

type AppSettings struct {
	VersionsJsonUrl        string              `json:"versions_json_url"`
	VersionsEtag           string              `json:"versions_etag"`
	TitlesJsonUrl          string              `json:"titles_json_url"`
	TitlesEtag             string              `json:"titles_etag"`
	LocalizedTitlesJsonUrl string              `json:"localized_titles_json_url"`
	LocalizedTitlesEtags   map[string]string   `json:"localized_titles_etags"`
	Prodkeys               string              `json:"prod_keys"`
	Folder                 string              `json:"folder"`
	ScanFolders            []string            `json:"scan_folders"`
	Port                   int                 `json:"port"`
	Debug                  bool                `json:"debug"`
	OrganizeOptions        OrganizeOptions     `json:"organize_options"`
	IgnoreDLCTitleIds      []string            `json:"ignore_dlc_title_ids"`
	IgnoreUpdateTitleIds   []string            `json:"ignore_update_title_ids"`
	IgnoreDLCUpdates       bool                `json:"ignore_dlc_updates"`
	IgnoreFileTypes        []string            `json:"ignore_file_types"`
	HideDemoGames          bool                `json:"hide_demo_games"`
	WatchFolders           bool                `json:"watch_folders"`
	SyncIntervalHours      int                 `json:"sync_interval_hours"`
	Language               string              `json:"language"`
	Notifications          NotificationOptions `json:"notifications"`
	LastSyncTime           time.Time           `json:"last_sync_time"`
}

func ReadSettingsAsJSON(dataFolder string) string {
	if _, err := os.Stat(filepath.Join(dataFolder, SETTINGS_FILENAME)); err != nil {
		saveDefaultSettings(dataFolder)
	}
	file, err := os.Open(filepath.Join(dataFolder, SETTINGS_FILENAME))
	if err != nil {
		return ""
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	return string(bytes)
}

func ReadSettings(dataFolder string) *AppSettings {
	if settingsInstance != nil {
		return settingsInstance
	}
	// defaults for keys missing from settings files written by older versions
	settingsInstance = &AppSettings{Debug: false, ScanFolders: []string{}, WatchFolders: true,
		Notifications:   NotificationOptions{NotifyUpdates: true, NotifyDlc: true},
		OrganizeOptions: OrganizeOptions{SwitchSafeFileNames: true}, Prodkeys: "", IgnoreDLCTitleIds: []string{"01007F600B135007"}}
	if _, err := os.Stat(filepath.Join(dataFolder, SETTINGS_FILENAME)); err == nil {
		file, err := os.Open(filepath.Join(dataFolder, SETTINGS_FILENAME))
		if err != nil {
			zap.S().Warnf("Missing or corrupted config file, creating a new one")
			return saveDefaultSettings(dataFolder)
		} else {
			err = json.NewDecoder(file).Decode(settingsInstance)
			file.Close()
			if err != nil {
				zap.S().Warnf("Corrupted config file, creating a new one - %v", err)
				return saveDefaultSettings(dataFolder)
			}
			return verifySettings(dataFolder, settingsInstance)
		}
	} else {
		return saveDefaultSettings(dataFolder)
	}
}

// verifySettings fills in values missing from settings files written by older versions.
// Ported from https://github.com/trembon/switch-library-manager
func verifySettings(dataFolder string, settings *AppSettings) *AppSettings {
	if settings.TitlesJsonUrl == "" {
		settings.TitlesJsonUrl = DEFAULT_TITLES_JSON_URL
	}
	if settings.VersionsJsonUrl == "" {
		settings.VersionsJsonUrl = DEFAULT_VERSIONS_JSON_URL
	}
	if settings.LocalizedTitlesJsonUrl == "" {
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
	settingsInstance = &AppSettings{
		TitlesJsonUrl:          DEFAULT_TITLES_JSON_URL,
		TitlesEtag:             DEFAULT_TITLES_ETAG,
		VersionsJsonUrl:        DEFAULT_VERSIONS_JSON_URL,
		LocalizedTitlesJsonUrl: DEFAULT_LOCALIZED_TITLES_JSON_URL,
		VersionsEtag:           DEFAULT_VERSIONS_ETAG,
		Prodkeys:               "", // empty: look for prod.keys in the data folder, then ~/.switch
		Folder:                 "/mnt/roms",
		ScanFolders:            []string{},
		IgnoreDLCTitleIds:      []string{},
		IgnoreUpdateTitleIds:   []string{},
		IgnoreFileTypes:        []string{},
		WatchFolders:           true,
		Notifications:          NotificationOptions{NotifyUpdates: true, NotifyDlc: true},
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
	return SaveSettings(settingsInstance, dataFolder)
}

var updateMutex sync.Mutex

// UpdateSettings applies change to the current settings and saves them. Concurrent
// updates are serialized so one request cannot overwrite the change of another.
func UpdateSettings(dataFolder string, change func(settings *AppSettings)) *AppSettings {
	updateMutex.Lock()
	defer updateMutex.Unlock()
	appSettings := ReadSettings(dataFolder)
	change(appSettings)
	return SaveSettings(appSettings, dataFolder)
}

func SaveSettings(settings *AppSettings, dataFolder string) *AppSettings {
	file, _ := json.MarshalIndent(settings, "", " ")
	if err := os.WriteFile(filepath.Join(dataFolder, SETTINGS_FILENAME), file, 0644); err != nil {
		zap.S().Errorf("Failed to save settings - %v", err)
	}
	settingsInstance = settings
	return settings
}
