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

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
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
	SLM_WEB_VERSION        = "1.73.0"
	REPOSITORY_OWNER       = "DeLFuS77"
	// the release where the "Update title data" workflow of this repository publishes the files
	// it generates; next to each one it publishes the same file compressed (see CompressedFirst)
	DATA_RELEASE_URL = "https://github.com/DeLFuS77/switch-library-manager-web/releases/download/data/"
	// the name of a file compressed with zstd is the one of the file with this after it, and a
	// download whose URL ends with it is decompressed: titles.json.zst
	COMPRESSED_SUFFIX = ".zst"
	// titles.json is generated from blawar/titledb, and versions.json joins the versions of
	// blawar/titledb with the ones of other sources
	DEFAULT_TITLES_JSON_URL   = DATA_RELEASE_URL + "titles.json"
	DEFAULT_VERSIONS_JSON_URL = DATA_RELEASE_URL + "versions.json"
	// the versions of blawar/titledb alone: the default before versions.json of the data release
	BLAWAR_VERSIONS_JSON_URL = "https://raw.githubusercontent.com/blawar/titledb/master/versions.json"
	// covers of other stores for titles without one in the US store
	DEFAULT_COVERS_JSON_URL = DATA_RELEASE_URL + "titles.covers.json"
	// the series of Switch games, from Wikidata
	DEFAULT_SERIES_JSON_URL = DATA_RELEASE_URL + "titles.series.json"
	// the games of the Nintendo Switch 2 alone, from the title list of switchbrew
	DEFAULT_SWITCH2_JSON_URL = DATA_RELEASE_URL + "titles.switch2.json"
	// the cheats of switch-cheats-db: the build ID of every version of every game, the cheats
	// of the last release as the SD card holds them, and the name of that release
	DEFAULT_CHEATS_VERSIONS_URL = "https://raw.githubusercontent.com/HamletDuFromage/switch-cheats-db/master/versions.json"
	DEFAULT_CHEATS_ZIP_URL      = "https://github.com/HamletDuFromage/switch-cheats-db/releases/latest/download/contents_complete.zip"
	DEFAULT_CHEATS_RELEASE_URL  = "https://github.com/HamletDuFromage/switch-cheats-db/releases/latest/download/VERSION"
	// %s is replaced by the interface language, e.g. titles.es.json
	DEFAULT_LOCALIZED_TITLES_JSON_URL = DATA_RELEASE_URL + "titles.%s.json"
	DEFAULT_TITLES_ETAG               = "W/\"a5b02845cf6bd61:0\""
	DEFAULT_VERSIONS_ETAG             = "W/\"2ef50d1cb6bd61:0\""
)

// Mirrors tried in order when the configured URL fails.
var (
	FALLBACK_TITLES_JSON_URLS = []string{
		"https://github.com/trembon/switch-library-manager/releases/download/data/titles.json",
	}
	FALLBACK_VERSIONS_JSON_URLS = []string{
		BLAWAR_VERSIONS_JSON_URL,
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
	// the titles of each kind a file holds (1G+1U+2D) and its size, as Switch Library Manager
	// writes them
	TEMPLATE_PACKAGE_CONTENTS = "PACKAGE_CONTENTS"
	TEMPLATE_SIZE_GB          = "SIZE_GB"
	TEMPLATE_SIZE_MB          = "SIZE_MB"
	// the number of a DLC among the ones of its game, from its title ID: 1, 2...
	TEMPLATE_DLC_NUMBER = "DLC_NUMBER"
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
	// each update and DLC goes to a folder named with its own ID, and the game folder has its
	// ID too: Game [ID] › Update [ID of the update], DLC › name [ID of the DLC]
	FoldersWithIds bool `json:"folders_with_ids,omitempty"`
	// with FoldersWithIds, every DLC goes to the DLC folder instead of a folder of its own:
	// paths stay shorter for the programs of Windows that read no more than 260 characters
	DlcInOneFolder bool `json:"dlc_in_one_folder,omitempty"`
	// which copy of a duplicate the library keeps: KEEP_FIRST_FOUND (empty), KEEP_NSP, KEEP_XCI
	// or KEEP_AUTO; the other one is the duplicate
	KeepDuplicate string `json:"keep_duplicate"`
	// old updates with cheats when the newer one has none are deleted too
	DeleteUpdatesWithCheats bool `json:"delete_updates_with_cheats,omitempty"`
	// the copies of duplicates chosen one by one to be kept, by path: they win over KeepDuplicate
	KeepFiles []string `json:"keep_files,omitempty"`
	// a file whose name has its title ID in brackets keeps its name: it is only moved
	KeepNamesWithId bool `json:"keep_names_with_id,omitempty"`
	// the subfolder of the game file, inside the game folder as the ones of updates and DLC
	BaseFolder string `json:"base_folder,omitempty"`
	// a game that has a folder of its own keeps it: its files go there, and it is not renamed
	KeepGameFolders bool `json:"keep_game_folders,omitempty"`
	// the NFO, SFV and proof files of a game stay where they are when it moves, instead of going
	// with it
	LeaveExtraFiles bool `json:"leave_extra_files,omitempty"`
	// the folders the scan leaves out (AppSettings.ExcludeFolders), given when organizing: a file
	// moved into one would leave the library; never saved with these options
	ExcludeFolders []string `json:"-"`
}

// The copies of a duplicate the library can keep.
const (
	KEEP_FIRST_FOUND = ""
	// the NSP or NSZ over the XCI or XCZ, and the other way round
	KEEP_NSP = "nsp"
	KEEP_XCI = "xci"
	// a file with several titles (it stays anyway), otherwise the smaller file
	KEEP_AUTO = "auto"
)

// KeepDuplicateChoices are the valid values of KeepDuplicate.
var KeepDuplicateChoices = []string{KEEP_FIRST_FOUND, KEEP_NSP, KEEP_XCI, KEEP_AUTO}

// the security of the connection to the SMTP server of the notifications
const (
	// encrypted after the greeting, usually on port 587: the default
	SMTP_STARTTLS = "starttls"
	// encrypted from the start, usually on port 465
	SMTP_TLS = "tls"
	// not encrypted, for a server of the local network that takes mail without a password
	SMTP_NONE = "none"
)

// SmtpSecurityChoices are the valid values of NotificationOptions.SmtpSecurity.
var SmtpSecurityChoices = []string{SMTP_STARTTLS, SMTP_TLS, SMTP_NONE}

// NotificationOptions configures the messages sent when updates or DLC become available.
type NotificationOptions struct {
	DiscordWebhookUrl string `json:"discord_webhook_url"`
	TelegramBotToken  string `json:"telegram_bot_token"`
	TelegramChatId    string `json:"telegram_chat_id"`
	WebhookUrl        string `json:"webhook_url"`
	// ntfy: the address of the topic, with its server, and an access token for a topic that is
	// protected
	NtfyUrl   string `json:"ntfy_url,omitempty"`
	NtfyToken string `json:"ntfy_token,omitempty"`
	// Gotify: the address of the server and the token of an application
	GotifyUrl   string `json:"gotify_url,omitempty"`
	GotifyToken string `json:"gotify_token,omitempty"`
	// Pushover: the key of the user (or of a group) and the token of an application
	PushoverUser  string `json:"pushover_user,omitempty"`
	PushoverToken string `json:"pushover_token,omitempty"`
	// email through an SMTP server; the port 0 is the usual one of the security
	SmtpHost     string `json:"smtp_host,omitempty"`
	SmtpPort     int    `json:"smtp_port,omitempty"`
	SmtpSecurity string `json:"smtp_security,omitempty"`
	SmtpUser     string `json:"smtp_user,omitempty"`
	SmtpPassword string `json:"smtp_password,omitempty"`
	// the address the mail comes from, the user when empty
	SmtpFrom string `json:"smtp_from,omitempty"`
	// the addresses the mail goes to, separated by commas
	SmtpTo        string `json:"smtp_to,omitempty"`
	NotifyUpdates bool   `json:"notify_updates"`
	NotifyDlc     bool   `json:"notify_dlc"`
	// wished games that are released or get new DLC
	NotifyWishlist bool `json:"notify_wishlist"`
	// games of the library that get cheats for the version they have
	NotifyCheats bool `json:"notify_cheats"`
	// games and DLC that appear in the folders
	NotifyNewGames bool `json:"notify_new_games"`
	// tasks that fail (a compression, a check, a synchronization...)
	NotifyTaskFailures bool `json:"notify_task_failures"`
	// no message when the queue of tasks is done: one is sent by default, also to the settings
	// saved before it existed
	QueueDoneQuiet bool `json:"queue_done_quiet,omitempty"`
	// the bot of Telegram answers in its chat: whether a game is in the library, what is new...
	TelegramCommands bool `json:"telegram_commands,omitempty"`
}

// VaultOptions are the save vault: JKSV uploads the backups of the saves over WebDAV, with this
// user and password (the hash only is kept). Off by default.
type VaultOptions struct {
	Enabled      bool   `json:"enabled"`
	User         string `json:"user,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	// backups kept per game, the newest; 0 keeps them all
	Keep int `json:"keep,omitempty"`
	// a message for every new backup
	Notify bool `json:"notify,omitempty"`
}

// QuarantineOptions: the files the app deletes from the library (duplicates, old updates, the
// copies of other regions) go first to a hidden folder of the same library folder, from where
// they can be put back until they expire.
type QuarantineOptions struct {
	// how long they are kept: "" (off: deleted at once), "48h", "7d", "30d" or "never" (until
	// emptied)
	Keep string `json:"keep,omitempty"`
	// the GB it holds at most, the oldest deleted first; 0 for no limit
	LimitGB int `json:"limit_gb"`
	// the oldest are deleted when a task needs room on their disk
	MakeRoom bool `json:"make_room"`
	// also the originals deleted after compressing, decompressing, converting, splitting or
	// making a pack, already checked then
	Originals bool `json:"originals,omitempty"`
}

// PortalOptions let the people a sign-in portal in front of the app already let in (Authelia,
// Authentik, Cloudflare Access) open it without the password of the app. Off by default; the
// environment variables of the same options win over them (see web/proxyauth.go).
type PortalOptions struct {
	// believe the user a proxy of SLM_TRUSTED_PROXIES names in a header
	Header       bool   `json:"header,omitempty"`
	UserHeader   string `json:"user_header,omitempty"`
	EmailHeader  string `json:"email_header,omitempty"`
	GroupsHeader string `json:"groups_header,omitempty"`
	// believe the signed token of Cloudflare Access: the team and the audience (AUD) of the
	// application
	Cloudflare     bool   `json:"cloudflare,omitempty"`
	CloudflareTeam string `json:"cloudflare_team,omitempty"`
	CloudflareAud  string `json:"cloudflare_aud,omitempty"`
	// a button of the login page signs in with OpenID Connect (PocketID, Authentik, Keycloak):
	// the address of the provider, the client of the app in it, the name on the button and the
	// scopes asked for
	Oidc             bool   `json:"oidc,omitempty"`
	OidcIssuer       string `json:"oidc_issuer,omitempty"`
	OidcClientId     string `json:"oidc_client_id,omitempty"`
	OidcClientSecret string `json:"oidc_client_secret,omitempty"`
	OidcButton       string `json:"oidc_button,omitempty"`
	OidcScopes       string `json:"oidc_scopes,omitempty"`
	// how a person is matched with a user: "" by name or email, "name" or "email"
	MatchBy string `json:"match_by,omitempty"`
	// the role of the user made for a person without one: "" makes none, guest or viewer
	NewUsers string `json:"new_users,omitempty"`
	// the members of this group are administrators
	AdminGroup string `json:"admin_group,omitempty"`
	// where Log out sends the people signed in by the portal
	LogoutUrl string `json:"logout_url,omitempty"`
}

// AutomationOptions are the steps run on the files that appear in the library, in this order:
// check, compress, delete old updates, organize, and a message with what was done.
type AutomationOptions struct {
	Enabled        bool `json:"enabled"`
	Verify         bool `json:"verify"`
	Compress       bool `json:"compress"`
	CleanupUpdates bool `json:"cleanup_updates"`
	Organize       bool `json:"organize"`
	// organize the new files only, into the folders of their games, and leave the rest as it is
	OrganizeNewOnly bool `json:"organize_new_only,omitempty"`
	Notify          bool `json:"notify"`
	// wait for the background hours instead of running at once
	BackgroundHoursOnly bool `json:"background_hours_only"`
}

// Any reports whether the automation is on with at least one step.
func (o AutomationOptions) Any() bool {
	return o.Enabled && (o.Verify || o.Compress || o.CleanupUpdates || o.Organize || o.Notify)
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
	// the folders below the library folders the scan leaves out, with everything in them, by
	// the start, the end or a part of their names (see FolderSkip); none by default
	ExcludeFolders []string `json:"exclude_folders,omitempty"`
	// the cheats of the games are not downloaded nor shown
	CheatsDisabled bool `json:"cheats_disabled,omitempty"`
	// a game the library has from several regions shows and counts once, by the copy the
	// Space page suggests to keep
	CountRegionsOnce bool `json:"count_regions_once,omitempty"`
	// the user has a Nintendo Switch 2: its own games are missing too, and counted with the others
	HasSwitch2   bool `json:"has_switch2,omitempty"`
	WatchFolders bool `json:"watch_folders"`
	// the country whose copy is the main one of a game of several regions, unless one is chosen
	// on the page of the game; empty for the copy suggested
	PreferredRegion string `json:"preferred_region,omitempty"`
	// hours of heavy background work, e.g. "1-7"; empty for any time
	BackgroundHours string `json:"background_hours"`
	// how often the folders are checked for changes notifications do not report
	WatchIntervalMinutes int `json:"watch_interval_minutes"`
	// automatic compression: "" (off), "new" (when files appear) or "night"
	AutoCompress      string `json:"auto_compress"`
	AutoCompressLevel string `json:"auto_compress_level"`
	// keep the originals of automatic compressions
	AutoCompressKeep bool `json:"auto_compress_keep"`
	// the threads that compress each file (0: half of the processors, at most 4) and the
	// files compressed, decompressed or converted at the same time (0: one)
	CompressThreads     int `json:"compress_threads,omitempty"`
	CompressFilesAtOnce int `json:"compress_files_at_once,omitempty"`
	// the app runs below the normal priority of the processor, so the rest of the server stays
	// responsive while it compresses
	LowPriority bool `json:"low_priority,omitempty"`
	// ask GitHub once a day whether a newer version of the app was released
	CheckForUpdates bool `json:"check_for_updates"`
	// a new version is installed by itself at night, where the app can update itself
	AutoInstallUpdates bool `json:"auto_install_updates,omitempty"`
	// the setup wizard was finished or turned off, or put off until then
	WizardDone  bool      `json:"wizard_done,omitempty"`
	WizardLater time.Time `json:"wizard_later,omitempty"`
	// what happens by itself when new files appear (all off by default)
	Automation AutomationOptions `json:"automation"`
	// the save vault for JKSV (off by default)
	Vault VaultOptions `json:"vault"`
	// where deleted files wait before they go (off by default)
	Quarantine QuarantineOptions `json:"quarantine"`
	// the Twitch application of the user, for the times to beat of IGDB (optional)
	IgdbClientId     string `json:"igdb_client_id,omitempty"`
	IgdbClientSecret string `json:"igdb_client_secret,omitempty"`
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
	// signing in through a portal in front of the app (off by default)
	Portal PortalOptions `json:"portal,omitzero"`
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
	loaded := settingsDefaults()
	path := filepath.Join(dataFolder, SETTINGS_FILENAME)
	if _, err := os.Stat(path); err == nil {
		file, err := os.Open(path)
		if err != nil {
			// e.g. a file of another user after PUID changed: never written over, it holds the
			// folders, the tokens and every choice; the defaults are used meanwhile
			zap.S().Errorf("The settings file cannot be opened, the defaults are used until it can - %v", err)
			settingsInstance.Store(loaded)
			return loaded
		} else {
			err = json.NewDecoder(file).Decode(loaded)
			file.Close()
			if err != nil {
				// kept aside, so what it holds can be recovered by hand
				aside := filepath.Join(dataFolder, SETTINGS_FILENAME+".corrupt-"+time.Now().Format("20060102-150405"))
				os.Rename(path, aside)
				// the settings saved before the last ones are tried before the defaults
				if restored := settingsDefaults(); decodeSettings(path+SETTINGS_BACKUP_SUFFIX, restored) == nil {
					zap.S().Errorf("The settings file could not be read, it was kept as %s and the copy saved before it is used - %v", filepath.Base(aside), err)
					return SaveSettings(verifySettings(dataFolder, restored), dataFolder)
				}
				zap.S().Errorf("The settings file could not be read, it was kept as %s and a new one is created - %v", filepath.Base(aside), err)
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

// settingsDefaults are the settings a settings file is read into: the defaults of the keys
// missing from the files written by older versions.
func settingsDefaults() *AppSettings {
	return &AppSettings{Debug: false, ScanFolders: []string{}, WatchFolders: true, CheckForUpdates: true,
		Notifications:   NotificationOptions{NotifyUpdates: true, NotifyDlc: true, NotifyWishlist: true, NotifyNewGames: true, NotifyCheats: true, NotifyTaskFailures: true},
		OrganizeOptions: OrganizeOptions{SwitchSafeFileNames: true}, Quarantine: QuarantineOptions{LimitGB: 100, MakeRoom: true}, Prodkeys: "", IgnoreDLCTitleIds: []string{"01007F600B135007"}}
}

// decodeSettings reads the settings file at path into settings.
func decodeSettings(path string, settings *AppSettings) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewDecoder(file).Decode(settings)
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
	// the versions of blawar/titledb were the default: settings saved with it get the versions
	// of the data release, which has them and the ones of other sources; a URL of one's own stays
	if settings.VersionsJsonUrl == BLAWAR_VERSIONS_JSON_URL {
		settings.VersionsJsonUrl = DEFAULT_VERSIONS_JSON_URL
	}

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

// ExcludedFolder tells a folder left out of the scan by its name, in any case: by its start, its
// end or a part of it (see FolderSkip).
func ExcludedFolder(patterns []string, name string) bool {
	name = strings.ToLower(name)
	for _, pattern := range patterns {
		kind, text := FolderSkip(pattern)
		text = strings.ToLower(text)
		switch {
		case text == "":
		case kind == SKIP_STARTING && strings.HasPrefix(name, text),
			kind == SKIP_ENDING && strings.HasSuffix(name, text),
			kind == SKIP_CONTAINING && strings.Contains(name, text):
			return true
		}
	}
	return false
}

// the kinds of folders to skip: by the start, the end or a part of their name
const (
	SKIP_STARTING   = "starting"
	SKIP_ENDING     = "ending"
	SKIP_CONTAINING = "containing"
)

// FolderSkip tells the kind and the text of a line of ExcludeFolders: "_*" skips the names that
// start with "_", "*_old" the ones that end with "_old", and "*build*", or "build", the ones that
// contain it. Only the * at the ends count: the rest is the text as it is.
func FolderSkip(line string) (kind string, text string) {
	pattern := FolderPattern(line)
	starts, ends := strings.HasPrefix(pattern, "*"), strings.HasSuffix(pattern, "*")
	text = SkipText(pattern)
	switch {
	case ends && !starts:
		return SKIP_STARTING, text
	case starts && !ends:
		return SKIP_ENDING, text
	}
	return SKIP_CONTAINING, text
}

// SkipPattern is the line of ExcludeFolders that skips the folders of a kind with a text; "" for
// a text without anything to look for.
func SkipPattern(kind string, text string) string {
	text = SkipText(text)
	switch {
	case text == "":
		return ""
	case kind == SKIP_STARTING:
		return text + "*"
	case kind == SKIP_ENDING:
		return "*" + text
	}
	return "*" + text + "*"
}

// SkipText is a text of a list of folders to skip as it is kept: without the spaces, the
// separators and the * around it.
func SkipText(line string) string {
	return strings.TrimSpace(strings.Trim(FolderPattern(line), "*"))
}

// SkipFolders lists the texts of the folders skipped by one kind, for the lists of the settings.
func (s AppSettings) SkipFolders(kind string) []string {
	texts := []string{}
	for _, pattern := range s.ExcludeFolders {
		if skip, text := FolderSkip(pattern); skip == kind && text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}

// SKIP_MARKER is a file that, put in a folder below the library folders, leaves the folder out
// of the scan with everything in it, whatever its name.
const SKIP_MARKER = ".slmignore"

// IsSkipMarker tells the name of SKIP_MARKER, in any case.
func IsSkipMarker(name string) bool {
	return strings.EqualFold(name, SKIP_MARKER)
}

// MarkedToSkip tells a folder with SKIP_MARKER in it.
func MarkedToSkip(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, SKIP_MARKER))
	return err == nil && !info.IsDir()
}

// FolderPattern is a line of ExcludeFolders as it is kept: without the spaces and the
// separators around it, which a folder name never has ("cache/").
func FolderPattern(line string) string {
	return strings.Trim(strings.TrimSpace(line), `/\`)
}

// ValidFolderPattern tells a text of the folders to skip that can be part of a folder name: a
// path has separators no name has.
func ValidFolderPattern(line string) bool {
	text := SkipText(line)
	return text != "" && !strings.ContainsAny(text, `/\`)
}

// TitlesJsonUrls returns the configured titles.json URL followed by the fallback mirrors, the
// files of the data release compressed first (see CompressedFirst).
func (s *AppSettings) TitlesJsonUrls() []string {
	return CompressedFirst(withFallbacks(s.TitlesJsonUrl, FALLBACK_TITLES_JSON_URLS)...)
}

// VersionsJsonUrls returns the configured versions.json URL followed by the fallback mirrors, the
// files of the data release compressed first (see CompressedFirst).
func (s *AppSettings) VersionsJsonUrls() []string {
	return CompressedFirst(withFallbacks(s.VersionsJsonUrl, FALLBACK_VERSIONS_JSON_URLS)...)
}

// CompressedFirst puts before each file of the data release the same file compressed, a quarter
// of its size or less; the file as it is comes next, for when the compressed one cannot be
// downloaded. The files of other places are left as they are.
func CompressedFirst(urls ...string) []string {
	result := make([]string, 0, 2*len(urls))
	for _, url := range urls {
		if strings.HasPrefix(url, DATA_RELEASE_URL) && strings.HasSuffix(url, ".json") {
			result = append(result, url+COMPRESSED_SUFFIX)
		}
		result = append(result, url)
	}
	return result
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
		Notifications:          NotificationOptions{NotifyUpdates: true, NotifyDlc: true, NotifyWishlist: true, NotifyNewGames: true, NotifyCheats: true, NotifyTaskFailures: true},
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
		Quarantine: QuarantineOptions{LimitGB: 100, MakeRoom: true},
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
// updates are serialized so one request cannot overwrite the change of another. A change that
// cannot be saved is not made, and the settings as they are are given (see TryUpdateSettings).
func UpdateSettings(dataFolder string, change func(settings *AppSettings)) *AppSettings {
	appSettings, err := TryUpdateSettings(dataFolder, change)
	if err != nil {
		zap.S().Errorf("Failed to save settings - %v", err)
	}
	return appSettings
}

// TryUpdateSettings is UpdateSettings that tells why a change could not be saved: the settings
// then stay as they were, in memory as on the disk, and the page that asked for it can say so
// instead of "Saved", which the next start would prove wrong.
func TryUpdateSettings(dataFolder string, change func(settings *AppSettings)) (*AppSettings, error) {
	updateMutex.Lock()
	defer updateMutex.Unlock()
	// a copy: other goroutines may be reading the current settings
	current := ReadSettings(dataFolder)
	appSettings := *current
	appSettings.LocalizedTitlesEtags = maps.Clone(appSettings.LocalizedTitlesEtags)
	change(&appSettings)
	if err := TrySaveSettings(&appSettings, dataFolder); err != nil {
		return current, err
	}
	return &appSettings, nil
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

// SETTINGS_BACKUP_SUFFIX names the copy of the settings saved before the last ones: tried when
// the settings file cannot be read, before the defaults.
const SETTINGS_BACKUP_SUFFIX = ".bak"

// SaveSettings saves the settings and makes them the current ones. Settings that cannot be
// saved are not: the current ones stay, and are given (see TrySaveSettings).
func SaveSettings(settings *AppSettings, dataFolder string) *AppSettings {
	if err := TrySaveSettings(settings, dataFolder); err != nil {
		zap.S().Errorf("Failed to save settings - %v", err)
		if current := settingsInstance.Load(); current != nil {
			return current
		}
		// none to keep (the defaults, in a data folder that cannot be written): used unsaved
		use(settings)
	}
	return settings
}

// TrySaveSettings saves the settings and, once they are on the disk, makes them the current
// ones; when they cannot be saved the current ones stay, and the error tells why.
func TrySaveSettings(settings *AppSettings, dataFolder string) error {
	file, err := json.MarshalIndent(settings, "", " ")
	if err != nil {
		return err
	}
	path := filepath.Join(dataFolder, SETTINGS_FILENAME)
	// the settings saved before, while they can be read: the last good ones if these get lost
	if previous, err := os.ReadFile(path); err == nil && json.Valid(previous) {
		if err := writeDurably(path+SETTINGS_BACKUP_SUFFIX, previous); err != nil {
			zap.S().Warnf("Failed to keep a copy of the settings - %v", err)
		}
	}
	if err := writeDurably(path, file); err != nil {
		return err
	}
	use(settings)
	return nil
}

// use makes settings the current ones.
func use(settings *AppSettings) {
	previous := settingsInstance.Swap(settings)
	if previous == nil || changesLists(previous, settings) {
		version.Add(1)
	}
}

// writeDurably writes a file aside and puts it in place at once, once its bytes are on the
// disk: a full disk or a power cut while it is written must not leave it empty or half written,
// which would lose every setting. The settings hold notification tokens: only the app reads them.
func writeDurably(path string, data []byte) error {
	temporary := path + ".tmp"
	err := func() error {
		file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		if _, err := file.Write(data); err != nil {
			file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		return os.Rename(temporary, path)
	}()
	if err != nil {
		os.Remove(temporary)
		return err
	}
	// a file written by an older version keeps its permissions otherwise
	os.Chmod(path, 0600)
	durable.SyncFolder(filepath.Dir(path))
	return nil
}
