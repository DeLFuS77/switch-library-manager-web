package web

import (
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/notify"
	"github.com/dtrunk90/switch-library-manager-web/internal/priority"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

type SettingsPageData struct {
	GlobalPageData
	Settings      *settings.AppSettings
	NextSync      time.Time
	SyncIntervals []int
	Languages     []string
	// games of the library without a cover
	MissingCovers int
	// the verification speeds, with the files checked at once on this computer
	VerifySpeeds []VerifySpeedOption
	// the copies of the configuration made every week
	AutoBackups []AutoBackup
	// what the automations need: the keys (to compress) and a notification channel
	KeysAvailable           bool
	NotificationsConfigured bool
	// the title keys loaded from title.keys
	TitleKeys int
	// the notification channels, with where each configured one sends
	NotifyServices      []notify.ServiceView
	SmtpSecurityChoices []string
	// the last measure of the server, nil when there is none
	LastMeasure *ServerMeasure
	// files waiting for the automations
	AutomationPending int
	// the threads offered to compress each file, the processors of this computer and the
	// threads used when not set
	CompressThreadChoices []int
	Processors            int
	DefaultThreads        int
	FilesAtOnceChoices    []int
	// the app can install a new version by itself (not in a container)
	SelfUpdatable bool
	// the files deleted that wait in the quarantine, and the sizes it can hold
	Quarantine       quarantineSummary
	QuarantineLimits []int
	// the sign-in portal in front of the app
	Portal PortalView
}

type SettingsForm struct {
	Prodkeys    string `in:"form=prod_keys"`
	ScanFolders string `in:"form=scan_folders"`
	// the folders to skip: the starts, the ends and the parts of their names, one per line
	SkipStarting          string `in:"form=skip_starting"`
	SkipEnding            string `in:"form=skip_ending"`
	SkipContaining        string `in:"form=skip_containing"`
	IgnoreDLCTitleIds     string `in:"form=ignore_dlc_title_ids"`
	IgnoreUpdateTitleIds  string `in:"form=ignore_update_title_ids"`
	IgnoreDLCUpdates      bool   `in:"form=ignore_dlc_updates"`
	IgnoreFileTypes       string `in:"form=ignore_file_types"`
	HideDemoGames         bool   `in:"form=hide_demo_games"`
	Cheats                bool   `in:"form=cheats"`
	KeepUpdatesWithCheats bool   `in:"form=keep_updates_with_cheats"`
	CountRegionsOnce      bool   `in:"form=count_regions_once"`
	HasSwitch2            bool   `in:"form=has_switch2"`
	PreferredRegion       string `in:"form=preferred_region"`
	PreferredRegion2      string `in:"form=preferred_region_2"`
	PreferredRegion3      string `in:"form=preferred_region_3"`
	WatchFolders          bool   `in:"form=watch_folders"`
	WatchIntervalMinutes  int    `in:"form=watch_interval_minutes"`
	BackgroundHours       string `in:"form=background_hours"`
	VerifyIntervalDays    int    `in:"form=verify_interval_days"`
	VerifySpeed           string `in:"form=verify_speed"`
	ConsoleFirmware       string `in:"form=console_firmware"`
	CheckForUpdates       bool   `in:"form=check_for_updates"`
	AutoInstallUpdates    bool   `in:"form=auto_install_updates"`
	AutomationEnabled     bool   `in:"form=automation_enabled"`
	AutomationVerify      bool   `in:"form=automation_verify"`
	AutomationCompress    bool   `in:"form=automation_compress"`
	AutomationCleanup     bool   `in:"form=automation_cleanup"`
	AutomationOrganize    bool   `in:"form=automation_organize"`
	AutomationNewOnly     bool   `in:"form=automation_organize_new_only"`
	AutomationNotify      bool   `in:"form=automation_notify"`
	AutomationNight       bool   `in:"form=automation_night"`
	VaultEnabled          bool   `in:"form=vault_enabled"`
	VaultUser             string `in:"form=vault_user"`
	VaultPassword         string `in:"form=vault_password"`
	VaultKeep             int    `in:"form=vault_keep"`
	VaultNotify           bool   `in:"form=vault_notify"`
	QuarantineKeep        string `in:"form=quarantine_keep"`
	QuarantineLimit       int    `in:"form=quarantine_limit"`
	QuarantineMakeRoom    bool   `in:"form=quarantine_make_room"`
	QuarantineOriginals   bool   `in:"form=quarantine_originals"`
	IgdbClientId          string `in:"form=igdb_client_id"`
	IgdbClientSecret      string `in:"form=igdb_client_secret"`
	AutoCompress          string `in:"form=auto_compress"`
	AutoCompressLevel     string `in:"form=auto_compress_level"`
	AutoCompressKeep      bool   `in:"form=auto_compress_keep"`
	CompressThreads       int    `in:"form=compress_threads"`
	CompressFilesAtOnce   int    `in:"form=compress_files_at_once"`
	LowPriority           bool   `in:"form=low_priority"`
	SyncIntervalHours     int    `in:"form=sync_interval_hours"`
	Language              string `in:"form=language"`
	DiscordWebhookUrl     string `in:"form=discord_webhook_url"`
	TelegramBotToken      string `in:"form=telegram_bot_token"`
	TelegramChatId        string `in:"form=telegram_chat_id"`
	WebhookUrl            string `in:"form=webhook_url"`
	NtfyUrl               string `in:"form=ntfy_url"`
	NtfyToken             string `in:"form=ntfy_token"`
	GotifyUrl             string `in:"form=gotify_url"`
	GotifyToken           string `in:"form=gotify_token"`
	PushoverUser          string `in:"form=pushover_user"`
	PushoverToken         string `in:"form=pushover_token"`
	SmtpHost              string `in:"form=smtp_host"`
	SmtpPort              string `in:"form=smtp_port"`
	SmtpSecurity          string `in:"form=smtp_security"`
	SmtpUser              string `in:"form=smtp_user"`
	SmtpPassword          string `in:"form=smtp_password"`
	SmtpFrom              string `in:"form=smtp_from"`
	SmtpTo                string `in:"form=smtp_to"`
	NotifyUpdates         bool   `in:"form=notify_updates"`
	NotifyDlc             bool   `in:"form=notify_dlc"`
	NotifyWishlist        bool   `in:"form=notify_wishlist"`
	NotifyCheats          bool   `in:"form=notify_cheats"`
	NotifyNewGames        bool   `in:"form=notify_new_games"`
	NotifyTaskFailures    bool   `in:"form=notify_task_failures"`
	NotifyQueueDone       bool   `in:"form=notify_queue_done"`
	TelegramCommands      bool   `in:"form=telegram_commands"`
	// the order of the files, sent by the setup wizard only: empty leaves the options of
	// Organize as they are
	OrganizeLayout       string `in:"form=organize_layout"`
	KeepGameFolders      bool   `in:"form=keep_game_folders"`
	RenameFiles          bool   `in:"form=rename_files"`
	KeepNamesWithId      bool   `in:"form=keep_names_with_id"`
	DeleteEmptyFolders   bool   `in:"form=delete_empty_folders"`
	MoveExtraFiles       bool   `in:"form=move_extra_files"`
	DeleteDuplicateFiles bool   `in:"form=delete_duplicate_files"`
	KeepDuplicate        string `in:"form=keep_duplicate"`
}

// skipList is a list of the folders to skip of the form: its field, its kind and its lines.
type skipList struct{ field, kind, lines string }

func (f *SettingsForm) skipLists() []skipList {
	return []skipList{{"skip_starting", settings.SKIP_STARTING, f.SkipStarting}, {"skip_ending", settings.SKIP_ENDING, f.SkipEnding},
		{"skip_containing", settings.SKIP_CONTAINING, f.SkipContaining}}
}

func (f *SettingsForm) notificationOptions() settings.NotificationOptions {
	return settings.NotificationOptions{
		DiscordWebhookUrl: strings.TrimSpace(f.DiscordWebhookUrl),
		TelegramBotToken:  strings.TrimSpace(f.TelegramBotToken),
		TelegramChatId:    strings.TrimSpace(f.TelegramChatId),
		WebhookUrl:        strings.TrimSpace(f.WebhookUrl),
		NtfyUrl:           strings.TrimSpace(f.NtfyUrl),
		NtfyToken:         strings.TrimSpace(f.NtfyToken),
		GotifyUrl:         strings.TrimSpace(f.GotifyUrl),
		GotifyToken:       strings.TrimSpace(f.GotifyToken),
		PushoverUser:      strings.TrimSpace(f.PushoverUser),
		PushoverToken:     strings.TrimSpace(f.PushoverToken),
		SmtpHost:          strings.TrimSpace(f.SmtpHost),
		SmtpPort:          formPort(strings.TrimSpace(f.SmtpPort)),
		SmtpSecurity:      formSecurity(strings.TrimSpace(f.SmtpSecurity)),
		SmtpUser:          strings.TrimSpace(f.SmtpUser),
		// a password may begin or end with a space
		SmtpPassword:       f.SmtpPassword,
		SmtpFrom:           strings.TrimSpace(f.SmtpFrom),
		SmtpTo:             strings.TrimSpace(f.SmtpTo),
		NotifyUpdates:      f.NotifyUpdates,
		NotifyDlc:          f.NotifyDlc,
		NotifyWishlist:     f.NotifyWishlist,
		NotifyCheats:       f.NotifyCheats,
		NotifyNewGames:     f.NotifyNewGames,
		NotifyTaskFailures: f.NotifyTaskFailures,
		QueueDoneQuiet:     !f.NotifyQueueDone,
		TelegramCommands:   f.TelegramCommands,
	}
}

var titleIdRegex = regexp.MustCompile("^[0-9A-Fa-f]{16}$")

func SplitAndTrimSpaceArray(s string, sep string) []string {
	arr := []string{}

	for _, v := range strings.Split(s, sep) {
		if value := strings.TrimSpace(v); value != "" {
			arr = append(arr, value)
		}
	}

	return arr
}

// toLowerSet returns the values as a lower case lookup set.
func toLowerSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	return set
}

func (web *Web) HandleSettings() {
	fsPatterns := []string{
		"resources/layout.html",
		"resources/pages/settings.html",
		"resources/partials/notifications.html",
	}

	web.HandleValidated("/settings.html", SettingsForm{}, func() any {
		return web.settingsPageData()
	}, func(value any, lang string) ErrorResponse {
		return web.settingsErrors(value.(*SettingsForm), lang)
	}, func(value any, lang string) SuccessResponse {
		return web.saveSettings(value.(*SettingsForm), lang)
	}, web.embedFS, fsPatterns...)
}

// settingsErrors checks the settings sent by the form, by groups of fields in the order of the
// page.
func (web *Web) settingsErrors(form *SettingsForm, lang string) ErrorResponse {
	errs := []FieldError{}
	errs = append(errs, keysErrors(form, lang)...)
	errs = append(errs, foldersErrors(form, lang)...)
	errs = append(errs, titleIdsErrors(form, lang)...)
	errs = append(errs, choicesErrors(form, lang)...)
	errs = append(errs, web.vaultErrors(form, lang)...)
	if _, ok := quarantineKeeps[form.QuarantineKeep]; !ok {
		errs = append(errs, FieldError{Field: "quarantine_keep", Message: i18n.Translate(lang, "Unknown option")})
	}
	if !slices.Contains(quarantineLimits, form.QuarantineLimit) {
		errs = append(errs, FieldError{Field: "quarantine_limit", Message: i18n.Translate(lang, "Unknown option")})
	}

	if _, ok := allowedVerifyIntervals[form.VerifyIntervalDays]; !ok {
		errs = append(errs, FieldError{
			Field:   "verify_interval_days",
			Message: i18n.Translate(lang, "Invalid verification interval"),
		})
	}

	// the secrets the page got masked are checked as they are saved
	sent := notify.KeptSecrets(form.notificationOptions(), settings.ReadSettings(web.dataFolder).Notifications)
	errs = append(errs, validateNotifications(sent, lang)...)

	if !validWizardOrganize(form.OrganizeLayout, form.KeepDuplicate) {
		errs = append(errs, FieldError{
			Field:   "organize_layout",
			Message: i18n.Translate(lang, "Unknown option"),
		})
	}

	if form.Language != "" && !i18n.IsSupportedLanguage(form.Language) {
		errs = append(errs, FieldError{
			Field:   "language",
			Message: i18n.Translate(lang, "Unsupported language"),
		})
	}
	return ErrorResponse{FieldErrors: errs}
}

// keysErrors checks the prod.keys written in the form, when there are.
func keysErrors(form *SettingsForm, lang string) []FieldError {
	if strings.TrimSpace(form.Prodkeys) == "" {
		return nil
	}
	keys, err := settings.GetSwitchKeys(form.Prodkeys)
	if err != nil {
		return []FieldError{{
			Field:   "prod_keys",
			Message: i18n.Translatef(lang, "Error trying to read Product Keys (%v)", err),
		}}
	}
	if keys["header_key"] == "" {
		return []FieldError{{
			Field:   "prod_keys",
			Message: i18n.Translate(lang, "Please provide a valid Product Keys Path"),
		}}
	}
	return nil
}

// foldersErrors checks the folders to scan, which must be there, each once, and the folders to
// skip, which are names or patterns.
func foldersErrors(form *SettingsForm, lang string) []FieldError {
	errs := []FieldError{}
	scanFolders := SplitAndTrimSpaceArray(form.ScanFolders, "\n")

	if len(scanFolders) == 0 {
		errs = append(errs, FieldError{
			Field:   "scan_folders",
			Message: i18n.Translate(lang, "Please provide at least one Folder to scan"),
		})
	} else {
		for _, value := range scanFolders {
			if _, err := os.Stat(value); os.IsNotExist(err) || os.IsPermission(err) {
				errs = append(errs, FieldError{
					Field:   "scan_folders",
					Message: i18n.Translatef(lang, "Folder not found: %v", value),
				})

				break
			}
		}
		// a folder twice, or one inside another, would have its files read twice
		if message := repeatedFolders(scanFolders, lang); message != "" {
			errs = append(errs, FieldError{
				Field:   "scan_folders",
				Message: message,
			})
		}
	}

	for _, list := range form.skipLists() {
		for _, value := range SplitAndTrimSpaceArray(list.lines, "\n") {
			// a line of * alone skips nothing, and is left out
			if settings.SkipText(value) != "" && !settings.ValidFolderPattern(value) {
				errs = append(errs, FieldError{
					Field:   list.field,
					Message: i18n.Translatef(lang, "Write a part of the name of a folder, not a path: %v", value),
				})
				break
			}
		}
	}
	return errs
}

// titleIdsErrors checks the title IDs of the updates and DLC to ignore.
func titleIdsErrors(form *SettingsForm, lang string) []FieldError {
	errs := []FieldError{}
	for field, ids := range map[string]string{"ignore_dlc_title_ids": form.IgnoreDLCTitleIds, "ignore_update_title_ids": form.IgnoreUpdateTitleIds} {
		for _, value := range SplitAndTrimSpaceArray(ids, "\n") {
			if !titleIdRegex.MatchString(value) {
				errs = append(errs, FieldError{
					Field:   field,
					Message: i18n.Translatef(lang, "Invalid Title ID (16 hexadecimal characters): %v", value),
				})

				break
			}
		}
	}
	return errs
}

// choicesErrors checks the options chosen in lists: each has to be one the page offers.
func choicesErrors(form *SettingsForm, lang string) []FieldError {
	errs := []FieldError{}
	if _, ok := allowedSyncIntervals[form.SyncIntervalHours]; !ok {
		errs = append(errs, FieldError{
			Field:   "sync_interval_hours",
			Message: i18n.Translate(lang, "Invalid synchronization interval"),
		})
	}

	if firmware := strings.TrimSpace(form.ConsoleFirmware); firmware != "" {
		if _, ok := parseFirmware(firmware); !ok {
			errs = append(errs, FieldError{
				Field:   "console_firmware",
				Message: i18n.Translate(lang, "Write the firmware like 18.1.0"),
			})
		}
	}

	if _, ok := allowedAutoCompress[form.AutoCompress]; !ok {
		errs = append(errs, FieldError{
			Field:   "auto_compress",
			Message: i18n.Translate(lang, "Unknown option"),
		})
	}

	if form.CompressThreads < 0 || form.CompressThreads > runtime.NumCPU() {
		errs = append(errs, FieldError{
			Field:   "compress_threads",
			Message: i18n.Translate(lang, "Unknown option"),
		})
	}
	if form.CompressFilesAtOnce < 0 || form.CompressFilesAtOnce > maxFilesAtOnce {
		errs = append(errs, FieldError{
			Field:   "compress_files_at_once",
			Message: i18n.Translate(lang, "Unknown option"),
		})
	}

	for field, value := range map[string]string{"preferred_region": form.PreferredRegion, "preferred_region_2": form.PreferredRegion2, "preferred_region_3": form.PreferredRegion3} {
		for _, region := range strings.Split(value, ",") {
			if region = strings.TrimSpace(region); region != "" && !regionPattern.MatchString(strings.ToUpper(region)) {
				errs = append(errs, FieldError{
					Field:   field,
					Message: i18n.Translate(lang, "Unknown option"),
				})
				break
			}
		}
	}

	if _, ok := allowedBackgroundHours[form.BackgroundHours]; !ok {
		errs = append(errs, FieldError{
			Field:   "background_hours",
			Message: i18n.Translate(lang, "Unknown option"),
		})
	}

	if _, ok := allowedWatchIntervals[form.WatchIntervalMinutes]; !ok {
		errs = append(errs, FieldError{
			Field:   "watch_interval_minutes",
			Message: i18n.Translate(lang, "Unknown option"),
		})
	}

	if _, ok := allowedVerifySpeeds[form.VerifySpeed]; !ok {
		errs = append(errs, FieldError{
			Field:   "verify_speed",
			Message: i18n.Translate(lang, "Unknown option"),
		})
	}
	return errs
}

// vaultErrors checks the save vault: it needs a user, and a password the first time (the hash
// only is kept).
func (web *Web) vaultErrors(form *SettingsForm, lang string) []FieldError {
	errs := []FieldError{}
	if form.VaultEnabled {
		vaultUser := strings.TrimSpace(form.VaultUser)
		if !validUserName.MatchString(vaultUser) {
			errs = append(errs, FieldError{Field: "vault_user", Message: i18n.Translate(lang, ErrUserName.Error())})
		}
		if password := form.VaultPassword; password != "" {
			if err := checkPassword(vaultUser, password); err != nil {
				errs = append(errs, FieldError{Field: "vault_password", Message: i18n.Translate(lang, err.Error())})
			}
		} else if settings.ReadSettings(web.dataFolder).Vault.PasswordHash == "" {
			errs = append(errs, FieldError{Field: "vault_password", Message: i18n.Translate(lang, ErrPasswordLength.Error())})
		}
	}
	if _, ok := allowedVaultKeep[form.VaultKeep]; !ok {
		errs = append(errs, FieldError{Field: "vault_keep", Message: i18n.Translate(lang, "Unknown option")})
	}
	return errs
}

// saveSettings saves the settings of the form, already checked, and scans the library with them.
func (web *Web) saveSettings(form *SettingsForm, lang string) SuccessResponse {
	before := settings.ReadSettings(web.dataFolder)
	cheatsWereOff, wasLowPriority := before.CheatsDisabled, before.LowPriority
	_, err := settings.TryUpdateSettings(web.dataFolder, form.apply)
	if err != nil {
		return notSaved(lang, err)
	}
	if form.LowPriority != wasLowPriority {
		web.applyPriority(form.LowPriority)
	}
	// turned on: the cheats come now, not with the next synchronization
	if cheatsWereOff && form.Cheats {
		go web.downloadCheats()
	}
	// the copy kept of each game of several regions follows the preferred region
	web.invalidateDerived()

	if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
		web.sugarLogger.Debugf("prod.keys not loaded: %s", err)
	}

	message := i18n.Translate(lang, "Settings changed successfully. The library is being rescanned.")
	if !web.Rescan(tasklog.TRIGGER_SETTINGS) {
		message = i18n.Translate(lang, "Settings changed successfully. They will be applied by the next synchronization.")
	}

	return SuccessResponse{
		StrongMessage: i18n.Translate(lang, "Success!"),
		Message:       message,
	}
}

// applyPriority runs the app below the normal priority of the processor, or at the normal one
// again (on Linux a user may not be let raise it back: the app then stays low until it starts
// again, as Settings says).
func (web *Web) applyPriority(low bool) {
	if err := priority.SetLow(low); err != nil {
		web.sugarLogger.Warnf("The priority of the app could not be changed: %v", err)
	}
}

// apply puts the values of the form in the settings.
func (form *SettingsForm) apply(appSettings *settings.AppSettings) {
	scanFolders := SplitAndTrimSpaceArray(form.ScanFolders, "\n")
	appSettings.Prodkeys = form.Prodkeys
	appSettings.IgnoreDLCTitleIds = SplitAndTrimSpaceArray(form.IgnoreDLCTitleIds, "\n")
	appSettings.IgnoreUpdateTitleIds = SplitAndTrimSpaceArray(form.IgnoreUpdateTitleIds, "\n")
	appSettings.IgnoreDLCUpdates = form.IgnoreDLCUpdates
	appSettings.IgnoreFileTypes = SplitAndTrimSpaceArray(strings.ReplaceAll(form.IgnoreFileTypes, ",", " "), " ")
	appSettings.HideDemoGames = form.HideDemoGames
	appSettings.CheatsDisabled = !form.Cheats
	appSettings.OrganizeOptions.DeleteUpdatesWithCheats = !form.KeepUpdatesWithCheats
	appSettings.CountRegionsOnce = form.CountRegionsOnce
	appSettings.HasSwitch2 = form.HasSwitch2
	// the zones in order, each once: "EU,US,JP"
	appSettings.PreferredRegion = strings.Join(preferredZones(strings.ToUpper(strings.Join([]string{form.PreferredRegion, form.PreferredRegion2, form.PreferredRegion3}, ","))), ",")
	appSettings.WatchFolders = form.WatchFolders
	appSettings.WatchIntervalMinutes = form.WatchIntervalMinutes
	appSettings.BackgroundHours = form.BackgroundHours
	appSettings.VerifyIntervalDays = form.VerifyIntervalDays
	appSettings.VerifySpeed = form.VerifySpeed
	appSettings.ConsoleFirmware = strings.TrimSpace(form.ConsoleFirmware)
	appSettings.CheckForUpdates = form.CheckForUpdates
	appSettings.AutoInstallUpdates = form.AutoInstallUpdates
	appSettings.Automation = settings.AutomationOptions{Enabled: form.AutomationEnabled, Verify: form.AutomationVerify,
		Compress: form.AutomationCompress, CleanupUpdates: form.AutomationCleanup, Organize: form.AutomationOrganize,
		OrganizeNewOnly: form.AutomationNewOnly, Notify: form.AutomationNotify, BackgroundHoursOnly: form.AutomationNight}
	// the vault password is kept as a hash only; an empty field keeps the one there is
	appSettings.Vault.Enabled = form.VaultEnabled
	appSettings.Vault.User = strings.TrimSpace(form.VaultUser)
	appSettings.Vault.Keep = form.VaultKeep
	appSettings.Vault.Notify = form.VaultNotify
	appSettings.Quarantine = settings.QuarantineOptions{Keep: form.QuarantineKeep, LimitGB: form.QuarantineLimit,
		MakeRoom: form.QuarantineMakeRoom, Originals: form.QuarantineOriginals}
	if form.VaultPassword != "" {
		if hash, err := hashVaultPassword(form.VaultPassword); err == nil {
			appSettings.Vault.PasswordHash = hash
		}
	}
	// the secret is never sent to the page: an empty field keeps it, no Client ID removes both
	appSettings.IgdbClientId = strings.TrimSpace(form.IgdbClientId)
	if appSettings.IgdbClientId == "" {
		appSettings.IgdbClientSecret = ""
	} else if secret := strings.TrimSpace(form.IgdbClientSecret); secret != "" {
		appSettings.IgdbClientSecret = secret
	}
	appSettings.AutoCompress = form.AutoCompress
	appSettings.AutoCompressLevel = form.AutoCompressLevel
	appSettings.AutoCompressKeep = form.AutoCompressKeep
	appSettings.CompressThreads = form.CompressThreads
	appSettings.CompressFilesAtOnce = form.CompressFilesAtOnce
	appSettings.LowPriority = form.LowPriority
	appSettings.SyncIntervalHours = form.SyncIntervalHours
	appSettings.Language = form.Language
	appSettings.Notifications = notify.KeptSecrets(form.notificationOptions(), appSettings.Notifications)
	if form.OrganizeLayout != "" {
		appSettings.OrganizeOptions = wizardOrganize(appSettings.OrganizeOptions, form.OrganizeLayout, wizardOrder{
			keepFolders: form.KeepGameFolders, rename: form.RenameFiles, keepNames: form.KeepNamesWithId, deleteEmpty: form.DeleteEmptyFolders,
			moveExtras: form.MoveExtraFiles, deleteDuplicates: form.DeleteDuplicateFiles, keepDuplicate: form.KeepDuplicate})
	}
	appSettings.Folder = scanFolders[0]
	if len(scanFolders) > 1 {
		appSettings.ScanFolders = scanFolders[1:]
	} else {
		appSettings.ScanFolders = []string{}
	}
	appSettings.ExcludeFolders = nil
	for _, list := range form.skipLists() {
		for _, line := range SplitAndTrimSpaceArray(list.lines, "\n") {
			if pattern := settings.SkipPattern(list.kind, line); pattern != "" {
				appSettings.ExcludeFolders = append(appSettings.ExcludeFolders, pattern)
			}
		}
	}
}

// notSaved is the answer to a form whose settings could not be saved (a disk full or that
// cannot be written, a file held by an antivirus): an error, never "Saved", which the next start
// would prove wrong.
func notSaved(lang string, err error) SuccessResponse {
	return SuccessResponse{failure: &ErrorResponse{FieldErrors: []FieldError{},
		GlobalError: GlobalError{StrongMessage: i18n.Translate(lang, "Error!"), Message: i18n.Translatef(lang, "The settings could not be saved: %v", err)}}}
}

// VerifySpeedOption is a verification speed and how many files it checks at once here.
type VerifySpeedOption struct {
	Value   string
	Label   string
	Workers int
}

func verifySpeedOptions() []VerifySpeedOption {
	cores := runtime.NumCPU()
	return []VerifySpeedOption{
		{Value: VERIFY_SPEED_LOW, Label: "Gentle", Workers: verifyWorkers(VERIFY_SPEED_LOW, cores)},
		{Value: "", Label: "Normal", Workers: verifyWorkers(VERIFY_SPEED_NORMAL, cores)},
		{Value: VERIFY_SPEED_FAST, Label: "Fast", Workers: verifyWorkers(VERIFY_SPEED_FAST, cores)},
	}
}

// settingsPageData is what the Settings page and the setup wizard show: the current settings
// (ignoring a title or a synchronization also changes them) and what they depend on.
func (web *Web) settingsPageData() SettingsPageData {
	current := settings.ReadSettings(web.dataFolder)
	// the secrets of the notifications never go back to the page (see secretMask)
	shown := *current
	shown.Notifications = notify.Masked(current.Notifications)
	portal := PortalView{}
	if web.auth != nil {
		portal = web.auth.portalView()
	}
	_, titleKeys, _ := settings.KeysSummary()
	return SettingsPageData{
		Portal:                  portal,
		GlobalPageData:          web.globalPageData("settings"),
		Settings:                &shown,
		NextSync:                nextSyncTime(current),
		SyncIntervals:           []int{0, 6, 12, 24, 168},
		Languages:               i18n.SupportedLanguages,
		MissingCovers:           web.missingCovers(),
		VerifySpeeds:            verifySpeedOptions(),
		AutoBackups:             web.autoBackups(),
		Quarantine:              web.quarantineSummary(),
		QuarantineLimits:        quarantineLimits,
		KeysAvailable:           settings.IsKeysFileAvailable(),
		TitleKeys:               titleKeys,
		NotificationsConfigured: notify.Configured(current.Notifications),
		NotifyServices:          notify.Services(current.Notifications),
		SmtpSecurityChoices:     settings.SmtpSecurityChoices,
		AutomationPending:       web.automationPending(),
		CompressThreadChoices:   compressThreadChoices(),
		Processors:              runtime.NumCPU(),
		DefaultThreads:          switchfs.UsableProcessors(),
		FilesAtOnceChoices:      []int{1, 2, 3, maxFilesAtOnce},
		SelfUpdatable:           selfUpdatable(),
		LastMeasure:             web.lastMeasure(),
	}
}

// repeatedFolders tells, in the language of the page, the first folder listed twice or inside
// another listed folder (the library is scanned with its subfolders); empty when there is none.
// The folders are compared on the disk too: a drive of Windows and the share it maps, or two
// volumes of a container on one folder, are one folder written in two ways. A folder written
// with ".." is compared by its name only, never looked for on the disk.
func repeatedFolders(folders []string, lang string) string {
	for i, folder := range folders {
		for j, other := range folders {
			if i == j {
				continue
			}
			same := db.SameFolderName(folder, other)
			if !same && !strings.Contains(folder, "..") && !strings.Contains(other, "..") {
				same = db.SameFolder(folder, other)
			}
			if same {
				if i < j {
					return i18n.Translatef(lang, "This folder is listed twice: %v", folder)
				}
				continue
			}
			inside := db.FolderInsideName(folder, other)
			if !inside && !strings.Contains(folder, "..") && !strings.Contains(other, "..") {
				inside = db.FolderInside(folder, other)
			}
			if inside {
				return i18n.Translatef(lang, "%v is inside %v: its files would be read twice. Keep only one of them.", folder, other)
			}
		}
	}
	return ""
}
