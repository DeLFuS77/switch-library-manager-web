package web

import (
	"runtime"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"os"
	"regexp"
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
	// files waiting for the automations
	AutomationPending int
}

type SettingsForm struct {
	Prodkeys          string `in:"form=prod_keys"`
	ScanFolders       string `in:"form=scan_folders"`
	IgnoreDLCTitleIds    string `in:"form=ignore_dlc_title_ids"`
	IgnoreUpdateTitleIds string `in:"form=ignore_update_title_ids"`
	IgnoreDLCUpdates     bool   `in:"form=ignore_dlc_updates"`
	IgnoreFileTypes      string `in:"form=ignore_file_types"`
	HideDemoGames        bool   `in:"form=hide_demo_games"`
	WatchFolders         bool   `in:"form=watch_folders"`
	WatchIntervalMinutes int    `in:"form=watch_interval_minutes"`
	BackgroundHours      string `in:"form=background_hours"`
	VerifyIntervalDays   int    `in:"form=verify_interval_days"`
	VerifySpeed          string `in:"form=verify_speed"`
	ConsoleFirmware      string `in:"form=console_firmware"`
	CheckForUpdates      bool   `in:"form=check_for_updates"`
	AutomationEnabled    bool   `in:"form=automation_enabled"`
	AutomationVerify     bool   `in:"form=automation_verify"`
	AutomationCompress   bool   `in:"form=automation_compress"`
	AutomationCleanup    bool   `in:"form=automation_cleanup"`
	AutomationOrganize   bool   `in:"form=automation_organize"`
	AutomationNotify     bool   `in:"form=automation_notify"`
	AutomationNight      bool   `in:"form=automation_night"`
	VaultEnabled         bool   `in:"form=vault_enabled"`
	VaultUser            string `in:"form=vault_user"`
	VaultPassword        string `in:"form=vault_password"`
	VaultKeep            int    `in:"form=vault_keep"`
	VaultNotify          bool   `in:"form=vault_notify"`
	IgdbClientId         string `in:"form=igdb_client_id"`
	IgdbClientSecret     string `in:"form=igdb_client_secret"`
	AutoCompress         string `in:"form=auto_compress"`
	AutoCompressLevel    string `in:"form=auto_compress_level"`
	AutoCompressKeep     bool   `in:"form=auto_compress_keep"`
	SyncIntervalHours    int    `in:"form=sync_interval_hours"`
	Language             string `in:"form=language"`
	DiscordWebhookUrl    string `in:"form=discord_webhook_url"`
	TelegramBotToken     string `in:"form=telegram_bot_token"`
	TelegramChatId       string `in:"form=telegram_chat_id"`
	WebhookUrl           string `in:"form=webhook_url"`
	NotifyUpdates        bool   `in:"form=notify_updates"`
	NotifyDlc            bool   `in:"form=notify_dlc"`
	NotifyWishlist       bool   `in:"form=notify_wishlist"`
	NotifyNewGames       bool   `in:"form=notify_new_games"`
}

func (f *SettingsForm) notificationOptions() settings.NotificationOptions {
	return settings.NotificationOptions{
		DiscordWebhookUrl: strings.TrimSpace(f.DiscordWebhookUrl),
		TelegramBotToken:  strings.TrimSpace(f.TelegramBotToken),
		TelegramChatId:    strings.TrimSpace(f.TelegramChatId),
		WebhookUrl:        strings.TrimSpace(f.WebhookUrl),
		NotifyUpdates:     f.NotifyUpdates,
		NotifyDlc:         f.NotifyDlc,
		NotifyWishlist:    f.NotifyWishlist,
		NotifyNewGames:    f.NotifyNewGames,
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
	fsPatterns := []string {
		"resources/layout.html",
		"resources/pages/settings.html",
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
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
					Field: "prod_keys",
					Message: translatef(lang, "Error trying to read Product Keys (%v)", err),
				})
			} else if keys["header_key"] == "" {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
					Field: "prod_keys",
					Message: translate(lang, "Please provide a valid Product Keys Path"),
				})
			}
		}

		scanFolders := SplitAndTrimSpaceArray(settingsForm.ScanFolders, "\n")

		if len(scanFolders) == 0 {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
				Field: "scan_folders",
				Message: translate(lang, "Please provide at least one Folder to scan"),
			})
		} else {
			for _, value := range scanFolders {
				if _, err := os.Stat(value); os.IsNotExist(err) || os.IsPermission(err) {
					errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
						Field: "scan_folders",
						Message: translatef(lang, "Folder not found: %v", value),
					})

					break
				}
			}
		}

		for field, ids := range map[string]string{"ignore_dlc_title_ids": settingsForm.IgnoreDLCTitleIds, "ignore_update_title_ids": settingsForm.IgnoreUpdateTitleIds} {
			for _, value := range SplitAndTrimSpaceArray(ids, "\n") {
				if !titleIdRegex.MatchString(value) {
					errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
						Field: field,
						Message: translatef(lang, "Invalid Title ID (16 hexadecimal characters): %v", value),
					})

					break
				}
			}
		}

		if _, ok := allowedSyncIntervals[settingsForm.SyncIntervalHours]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
				Field: "sync_interval_hours",
				Message: translate(lang, "Invalid synchronization interval"),
			})
		}

		if firmware := strings.TrimSpace(settingsForm.ConsoleFirmware); firmware != "" {
			if _, ok := parseFirmware(firmware); !ok {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
					Field: "console_firmware",
					Message: translate(lang, "Write the firmware like 18.1.0"),
				})
			}
		}

		if _, ok := allowedAutoCompress[settingsForm.AutoCompress]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
				Field: "auto_compress",
				Message: translate(lang, "Unknown option"),
			})
		}

		if _, ok := allowedBackgroundHours[settingsForm.BackgroundHours]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
				Field: "background_hours",
				Message: translate(lang, "Unknown option"),
			})
		}

		if _, ok := allowedWatchIntervals[settingsForm.WatchIntervalMinutes]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
				Field: "watch_interval_minutes",
				Message: translate(lang, "Unknown option"),
			})
		}

		if _, ok := allowedVerifySpeeds[settingsForm.VerifySpeed]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
				Field: "verify_speed",
				Message: translate(lang, "Unknown option"),
			})
		}

		// the save vault needs a user, and a password the first time (the hash only is kept)
		if settingsForm.VaultEnabled {
			vaultUser := strings.TrimSpace(settingsForm.VaultUser)
			if !validUserName.MatchString(vaultUser) {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: "vault_user", Message: translate(lang, ErrUserName.Error())})
			}
			if password := settingsForm.VaultPassword; password != "" {
				if err := checkPassword(vaultUser, password); err != nil {
					errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: "vault_password", Message: translate(lang, err.Error())})
				}
			} else if settings.ReadSettings(web.dataFolder).Vault.PasswordHash == "" {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: "vault_password", Message: translate(lang, ErrPasswordLength.Error())})
			}
		}
		if _, ok := allowedVaultKeep[settingsForm.VaultKeep]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: "vault_keep", Message: translate(lang, "Unknown option")})
		}

		if _, ok := allowedVerifyIntervals[settingsForm.VerifyIntervalDays]; !ok {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
				Field: "verify_interval_days",
				Message: translate(lang, "Invalid verification interval"),
			})
		}

		errorResponse.FieldErrors = append(errorResponse.FieldErrors, validateNotifications(settingsForm.notificationOptions(), lang)...)

		if settingsForm.Language != "" && !isSupportedLanguage(settingsForm.Language) {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
				Field: "language",
				Message: translate(lang, "Unsupported language"),
			})
		}

		return errorResponse
	}, func(value any, lang string) SuccessResponse {
		settingsForm := value.(*SettingsForm)
		scanFolders := SplitAndTrimSpaceArray(settingsForm.ScanFolders, "\n")

		settings.UpdateSettings(web.dataFolder, func(appSettings *settings.AppSettings) {
			appSettings.Prodkeys = settingsForm.Prodkeys
			appSettings.IgnoreDLCTitleIds = SplitAndTrimSpaceArray(settingsForm.IgnoreDLCTitleIds, "\n")
			appSettings.IgnoreUpdateTitleIds = SplitAndTrimSpaceArray(settingsForm.IgnoreUpdateTitleIds, "\n")
			appSettings.IgnoreDLCUpdates = settingsForm.IgnoreDLCUpdates
			appSettings.IgnoreFileTypes = SplitAndTrimSpaceArray(strings.ReplaceAll(settingsForm.IgnoreFileTypes, ",", " "), " ")
			appSettings.HideDemoGames = settingsForm.HideDemoGames
			appSettings.WatchFolders = settingsForm.WatchFolders
			appSettings.WatchIntervalMinutes = settingsForm.WatchIntervalMinutes
			appSettings.BackgroundHours = settingsForm.BackgroundHours
			appSettings.VerifyIntervalDays = settingsForm.VerifyIntervalDays
			appSettings.VerifySpeed = settingsForm.VerifySpeed
			appSettings.ConsoleFirmware = strings.TrimSpace(settingsForm.ConsoleFirmware)
			appSettings.CheckForUpdates = settingsForm.CheckForUpdates
			appSettings.Automation = settings.AutomationOptions{Enabled: settingsForm.AutomationEnabled, Verify: settingsForm.AutomationVerify,
				Compress: settingsForm.AutomationCompress, CleanupUpdates: settingsForm.AutomationCleanup, Organize: settingsForm.AutomationOrganize,
				Notify: settingsForm.AutomationNotify, BackgroundHoursOnly: settingsForm.AutomationNight}
			// the vault password is kept as a hash only; an empty field keeps the one there is
			appSettings.Vault.Enabled = settingsForm.VaultEnabled
			appSettings.Vault.User = strings.TrimSpace(settingsForm.VaultUser)
			appSettings.Vault.Keep = settingsForm.VaultKeep
			appSettings.Vault.Notify = settingsForm.VaultNotify
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
			appSettings.SyncIntervalHours = settingsForm.SyncIntervalHours
			appSettings.Language = settingsForm.Language
			appSettings.Notifications = settingsForm.notificationOptions()
			appSettings.Folder = scanFolders[0]
			if len(scanFolders) > 1 {
				appSettings.ScanFolders = scanFolders[1:]
			} else {
				appSettings.ScanFolders = []string{}
			}
		})

		if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
			web.sugarLogger.Debugf("prod.keys not loaded: %s", err)
		}

		message := translate(lang, "Settings changed successfully. The library is being rescanned.")
		if !web.Rescan(TRIGGER_SETTINGS) {
			message = translate(lang, "Settings changed successfully. They will be applied by the next synchronization.")
		}

		return SuccessResponse {
			StrongMessage: translate(lang, "Success!"),
			Message: message,
		}
	}, web.embedFS, fsPatterns...)
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
	return SettingsPageData{
		GlobalPageData:          web.globalPageData("settings"),
		Settings:                current,
		NextSync:                nextSyncTime(current),
		SyncIntervals:           []int{0, 6, 12, 24, 168},
		Languages:               supportedLanguages,
		MissingCovers:           web.missingCovers(),
		VerifySpeeds:            verifySpeedOptions(),
		AutoBackups:             web.autoBackups(),
		KeysAvailable:           settings.IsKeysFileAvailable(),
		NotificationsConfigured: notificationsConfigured(current.Notifications),
		AutomationPending:       web.automationPending(),
	}
}
