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
		// the current settings: ignoring a title or a synchronization also changes them
		current := settings.ReadSettings(web.dataFolder)
		return SettingsPageData {
			NextSync: nextSyncTime(current),
			SyncIntervals: []int{0, 6, 12, 24, 168},
			Languages: supportedLanguages,
			GlobalPageData: web.globalPageData("settings"),
			Settings: current,
			MissingCovers: web.missingCovers(),
			VerifySpeeds: verifySpeedOptions(),
		}
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
