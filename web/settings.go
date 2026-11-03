package web

import (
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/notify"
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
	Prodkeys              string `in:"form=prod_keys"`
	ScanFolders           string `in:"form=scan_folders"`
	ExcludeFolders        string `in:"form=exclude_folders"`
	IgnoreDLCTitleIds     string `in:"form=ignore_dlc_title_ids"`
	IgnoreUpdateTitleIds  string `in:"form=ignore_update_title_ids"`
	IgnoreDLCUpdates      bool   `in:"form=ignore_dlc_updates"`
	IgnoreFileTypes       string `in:"form=ignore_file_types"`
	HideDemoGames         bool   `in:"form=hide_demo_games"`
	Cheats                bool   `in:"form=cheats"`
	KeepUpdatesWithCheats bool   `in:"form=keep_updates_with_cheats"`
	CountRegionsOnce      bool   `in:"form=count_regions_once"`
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
	OrganizeLayout     string `in:"form=organize_layout"`
	KeepGameFolders    bool   `in:"form=keep_game_folders"`
	RenameFiles        bool   `in:"form=rename_files"`
	DeleteEmptyFolders bool   `in:"form=delete_empty_folders"`
	KeepDuplicate      string `in:"form=keep_duplicate"`
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
		settingsForm := value.(*SettingsForm)
		errorResponse := ErrorResponse{
			FieldErrors: []FieldError{},
		}

		if strings.TrimSpace(settingsForm.Prodkeys) != "" {
			keys, err := settings.GetSwitchKeys(settingsForm.Prodkeys)
			if err != nil {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
					Field:   "prod_keys",
					Message: i18n.Translatef(lang, "Error trying to read Product Keys (%v)", err),
				})
			} else if keys["header_key"] == "" {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
					Field:   "prod_keys",
					Message: i18n.Translate(lang, "Please provide a valid Product Keys Path"),
				})
			}
		}

		scanFolders := SplitAndTrimSpaceArray(settingsForm.ScanFolders, "\n")

		if len(scanFolders) == 0 {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "scan_folders",
				Message: i18n.Translate(lang, "Please provide at least one Folder to scan"),
			})
		} else {
			for _, value := range scanFolders {
				if _, err := os.Stat(value); os.IsNotExist(err) || os.IsPermission(err) {
					errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
						Field:   "scan_folders",
						Message: i18n.Translatef(lang, "Folder not found: %v", value),
					})

					break
				}
			}
			// a folder twice, or one inside another, would have its files read twice
			if message := repeatedFolders(scanFolders, lang); message != "" {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
					Field:   "scan_folders",
					Message: message,
				})
			}
		}

		for _, value := range SplitAndTrimSpaceArray(settingsForm.ExcludeFolders, "\n") {
			if !settings.ValidFolderPattern(value) {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
					Field:   "exclude_folders",
					Message: i18n.Translatef(lang, "Write the name of a folder, or a pattern like *_build*, not a path: %v", value),
				})
				break
			}
		}

		for field, ids := range map[string]string{"ignore_dlc_title_ids": settingsForm.IgnoreDLCTitleIds, "ignore_update_title_ids": settingsForm.IgnoreUpdateTitleIds} {
			for _, value := range SplitAndTrimSpaceArray(ids, "\n") {
				if !titleIdRegex.MatchString(value) {
					errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
						Field:   field,
						Message: i18n.Translatef(lang, "Invalid Title ID (16 hexadecimal characters): %v", value),
					})

					break
				}
			}
		}

		if _, ok := allowedSyncIntervals[settingsForm.SyncIntervalHours]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "sync_interval_hours",
				Message: i18n.Translate(lang, "Invalid synchronization interval"),
			})
		}

		if firmware := strings.TrimSpace(settingsForm.ConsoleFirmware); firmware != "" {
			if _, ok := parseFirmware(firmware); !ok {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
					Field:   "console_firmware",
					Message: i18n.Translate(lang, "Write the firmware like 18.1.0"),
				})
			}
		}

		if _, ok := allowedAutoCompress[settingsForm.AutoCompress]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "auto_compress",
				Message: i18n.Translate(lang, "Unknown option"),
			})
		}

		if settingsForm.CompressThreads < 0 || settingsForm.CompressThreads > runtime.NumCPU() {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "compress_threads",
				Message: i18n.Translate(lang, "Unknown option"),
			})
		}
		if settingsForm.CompressFilesAtOnce < 0 || settingsForm.CompressFilesAtOnce > maxFilesAtOnce {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "compress_files_at_once",
				Message: i18n.Translate(lang, "Unknown option"),
			})
		}

		for field, value := range map[string]string{"preferred_region": settingsForm.PreferredRegion, "preferred_region_2": settingsForm.PreferredRegion2, "preferred_region_3": settingsForm.PreferredRegion3} {
			for _, region := range strings.Split(value, ",") {
				if region = strings.TrimSpace(region); region != "" && !regionPattern.MatchString(strings.ToUpper(region)) {
					errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
						Field:   field,
						Message: i18n.Translate(lang, "Unknown option"),
					})
					break
				}
			}
		}

		if _, ok := allowedBackgroundHours[settingsForm.BackgroundHours]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "background_hours",
				Message: i18n.Translate(lang, "Unknown option"),
			})
		}

		if _, ok := allowedWatchIntervals[settingsForm.WatchIntervalMinutes]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "watch_interval_minutes",
				Message: i18n.Translate(lang, "Unknown option"),
			})
		}

		if _, ok := allowedVerifySpeeds[settingsForm.VerifySpeed]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "verify_speed",
				Message: i18n.Translate(lang, "Unknown option"),
			})
		}

		// the save vault needs a user, and a password the first time (the hash only is kept)
		if settingsForm.VaultEnabled {
			vaultUser := strings.TrimSpace(settingsForm.VaultUser)
			if !validUserName.MatchString(vaultUser) {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: "vault_user", Message: i18n.Translate(lang, ErrUserName.Error())})
			}
			if password := settingsForm.VaultPassword; password != "" {
				if err := checkPassword(vaultUser, password); err != nil {
					errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: "vault_password", Message: i18n.Translate(lang, err.Error())})
				}
			} else if settings.ReadSettings(web.dataFolder).Vault.PasswordHash == "" {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: "vault_password", Message: i18n.Translate(lang, ErrPasswordLength.Error())})
			}
		}
		if _, ok := allowedVaultKeep[settingsForm.VaultKeep]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: "vault_keep", Message: i18n.Translate(lang, "Unknown option")})
		}
		if _, ok := quarantineKeeps[settingsForm.QuarantineKeep]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: "quarantine_keep", Message: i18n.Translate(lang, "Unknown option")})
		}
		if !slices.Contains(quarantineLimits, settingsForm.QuarantineLimit) {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: "quarantine_limit", Message: i18n.Translate(lang, "Unknown option")})
		}

		if _, ok := allowedVerifyIntervals[settingsForm.VerifyIntervalDays]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "verify_interval_days",
				Message: i18n.Translate(lang, "Invalid verification interval"),
			})
		}

		// the secrets the page got masked are checked as they are saved
		sent := notify.KeptSecrets(settingsForm.notificationOptions(), settings.ReadSettings(web.dataFolder).Notifications)
		errorResponse.FieldErrors = append(errorResponse.FieldErrors, validateNotifications(sent, lang)...)

		if !validWizardOrganize(settingsForm.OrganizeLayout, settingsForm.KeepDuplicate) {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "organize_layout",
				Message: i18n.Translate(lang, "Unknown option"),
			})
		}

		if settingsForm.Language != "" && !i18n.IsSupportedLanguage(settingsForm.Language) {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{
				Field:   "language",
				Message: i18n.Translate(lang, "Unsupported language"),
			})
		}

		return errorResponse
	}, func(value any, lang string) SuccessResponse {
		settingsForm := value.(*SettingsForm)
		cheatsWereOff := settings.ReadSettings(web.dataFolder).CheatsDisabled
		scanFolders := SplitAndTrimSpaceArray(settingsForm.ScanFolders, "\n")

		_, err := settings.TryUpdateSettings(web.dataFolder, func(appSettings *settings.AppSettings) {
			appSettings.Prodkeys = settingsForm.Prodkeys
			appSettings.IgnoreDLCTitleIds = SplitAndTrimSpaceArray(settingsForm.IgnoreDLCTitleIds, "\n")
			appSettings.IgnoreUpdateTitleIds = SplitAndTrimSpaceArray(settingsForm.IgnoreUpdateTitleIds, "\n")
			appSettings.IgnoreDLCUpdates = settingsForm.IgnoreDLCUpdates
			appSettings.IgnoreFileTypes = SplitAndTrimSpaceArray(strings.ReplaceAll(settingsForm.IgnoreFileTypes, ",", " "), " ")
			appSettings.HideDemoGames = settingsForm.HideDemoGames
			appSettings.CheatsDisabled = !settingsForm.Cheats
			appSettings.OrganizeOptions.DeleteUpdatesWithCheats = !settingsForm.KeepUpdatesWithCheats
			appSettings.CountRegionsOnce = settingsForm.CountRegionsOnce
			// the zones in order, each once: "EU,US,JP"
			appSettings.PreferredRegion = strings.Join(preferredZones(strings.ToUpper(strings.Join([]string{settingsForm.PreferredRegion, settingsForm.PreferredRegion2, settingsForm.PreferredRegion3}, ","))), ",")
			appSettings.WatchFolders = settingsForm.WatchFolders
			appSettings.WatchIntervalMinutes = settingsForm.WatchIntervalMinutes
			appSettings.BackgroundHours = settingsForm.BackgroundHours
			appSettings.VerifyIntervalDays = settingsForm.VerifyIntervalDays
			appSettings.VerifySpeed = settingsForm.VerifySpeed
			appSettings.ConsoleFirmware = strings.TrimSpace(settingsForm.ConsoleFirmware)
			appSettings.CheckForUpdates = settingsForm.CheckForUpdates
			appSettings.AutoInstallUpdates = settingsForm.AutoInstallUpdates
			appSettings.Automation = settings.AutomationOptions{Enabled: settingsForm.AutomationEnabled, Verify: settingsForm.AutomationVerify,
				Compress: settingsForm.AutomationCompress, CleanupUpdates: settingsForm.AutomationCleanup, Organize: settingsForm.AutomationOrganize,
				OrganizeNewOnly: settingsForm.AutomationNewOnly, Notify: settingsForm.AutomationNotify, BackgroundHoursOnly: settingsForm.AutomationNight}
			// the vault password is kept as a hash only; an empty field keeps the one there is
			appSettings.Vault.Enabled = settingsForm.VaultEnabled
			appSettings.Vault.User = strings.TrimSpace(settingsForm.VaultUser)
			appSettings.Vault.Keep = settingsForm.VaultKeep
			appSettings.Vault.Notify = settingsForm.VaultNotify
			appSettings.Quarantine = settings.QuarantineOptions{Keep: settingsForm.QuarantineKeep, LimitGB: settingsForm.QuarantineLimit,
				MakeRoom: settingsForm.QuarantineMakeRoom, Originals: settingsForm.QuarantineOriginals}
			if settingsForm.VaultPassword != "" {
				if hash, err := hashVaultPassword(settingsForm.VaultPassword); err == nil {
					appSettings.Vault.PasswordHash = hash
				}
			}
			// the secret is never sent to the page: an empty field keeps it, no Client ID removes both
			appSettings.IgdbClientId = strings.TrimSpace(settingsForm.IgdbClientId)
			if appSettings.IgdbClientId == "" {
				appSettings.IgdbClientSecret = ""
			} else if secret := strings.TrimSpace(settingsForm.IgdbClientSecret); secret != "" {
				appSettings.IgdbClientSecret = secret
			}
			appSettings.AutoCompress = settingsForm.AutoCompress
			appSettings.AutoCompressLevel = settingsForm.AutoCompressLevel
			appSettings.AutoCompressKeep = settingsForm.AutoCompressKeep
			appSettings.CompressThreads = settingsForm.CompressThreads
			appSettings.CompressFilesAtOnce = settingsForm.CompressFilesAtOnce
			appSettings.SyncIntervalHours = settingsForm.SyncIntervalHours
			appSettings.Language = settingsForm.Language
			appSettings.Notifications = notify.KeptSecrets(settingsForm.notificationOptions(), appSettings.Notifications)
			if settingsForm.OrganizeLayout != "" {
				appSettings.OrganizeOptions = wizardOrganize(appSettings.OrganizeOptions, settingsForm.OrganizeLayout,
					settingsForm.KeepGameFolders, settingsForm.RenameFiles, settingsForm.DeleteEmptyFolders, settingsForm.KeepDuplicate)
			}
			appSettings.Folder = scanFolders[0]
			if len(scanFolders) > 1 {
				appSettings.ScanFolders = scanFolders[1:]
			} else {
				appSettings.ScanFolders = []string{}
			}
			appSettings.ExcludeFolders = nil
			for _, line := range SplitAndTrimSpaceArray(settingsForm.ExcludeFolders, "\n") {
				appSettings.ExcludeFolders = append(appSettings.ExcludeFolders, settings.FolderPattern(line))
			}
		})
		if err != nil {
			return notSaved(lang, err)
		}
		// turned on: the cheats come now, not with the next synchronization
		if cheatsWereOff && settingsForm.Cheats {
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
	}, web.embedFS, fsPatterns...)
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
