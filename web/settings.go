package web

import (
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"os"
	"regexp"
	"strings"
)

type SettingsPageData struct {
	GlobalPageData
	Settings *settings.AppSettings
}

type SettingsForm struct {
	Prodkeys          string `in:"form=prod_keys"`
	ScanFolders       string `in:"form=scan_folders"`
	IgnoreDLCTitleIds    string `in:"form=ignore_dlc_title_ids"`
	IgnoreUpdateTitleIds string `in:"form=ignore_update_title_ids"`
	IgnoreDLCUpdates     bool   `in:"form=ignore_dlc_updates"`
	IgnoreFileTypes      string `in:"form=ignore_file_types"`
	HideDemoGames        bool   `in:"form=hide_demo_games"`
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
		return SettingsPageData {
			GlobalPageData: web.globalPageData("settings"),
			Settings: web.appSettings,
		}
	}, func(value any) ErrorResponse {
		settingsForm := value.(*SettingsForm)
		errorResponse := ErrorResponse{
			FieldErrors: []FieldError{},
		}

		if strings.TrimSpace(settingsForm.Prodkeys) != "" {
			keys, err := settings.GetSwitchKeys(settingsForm.Prodkeys)
			if err != nil {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
					Field: "prod_keys",
					Message: "Error trying to read Product Keys (" + err.Error() + ")",
				})
			} else if keys["header_key"] == "" {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
					Field: "prod_keys",
					Message: "Please provide a valid Product Keys Path",
				})
			}
		}

		scanFolders := SplitAndTrimSpaceArray(settingsForm.ScanFolders, "\n")

		if len(scanFolders) == 0 {
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
				Field: "scan_folders",
				Message: "Please provide at least one Folder to scan",
			})
		} else {
			for _, value := range scanFolders {
				if _, err := os.Stat(value); os.IsNotExist(err) || os.IsPermission(err) {
					errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError {
						Field: "scan_folders",
						Message: "Folder not found: " + value,
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
						Message: "Invalid Title ID (16 hexadecimal characters): " + value,
					})

					break
				}
			}
		}

		return errorResponse
	}, func(value any) SuccessResponse {
		settingsForm := value.(*SettingsForm)
		scanFolders := SplitAndTrimSpaceArray(settingsForm.ScanFolders, "\n")

		appSettings := settings.ReadSettings(web.dataFolder)
		appSettings.Prodkeys = settingsForm.Prodkeys
		appSettings.IgnoreDLCTitleIds = SplitAndTrimSpaceArray(settingsForm.IgnoreDLCTitleIds, "\n")
		appSettings.IgnoreUpdateTitleIds = SplitAndTrimSpaceArray(settingsForm.IgnoreUpdateTitleIds, "\n")
		appSettings.IgnoreDLCUpdates = settingsForm.IgnoreDLCUpdates
		appSettings.IgnoreFileTypes = SplitAndTrimSpaceArray(strings.ReplaceAll(settingsForm.IgnoreFileTypes, ",", " "), " ")
		appSettings.HideDemoGames = settingsForm.HideDemoGames
		appSettings.Folder = scanFolders[0]
		if len(scanFolders) > 1 {
			appSettings.ScanFolders = scanFolders[1:]
		} else {
			appSettings.ScanFolders = []string{}
		}

		settings.SaveSettings(appSettings, web.dataFolder)
		web.appSettings = appSettings

		if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
			web.sugarLogger.Warnf("Failed to initialize switch keys: %s", err)
		}

		message := "Settings changed successfully. The library is being rescanned."
		if !web.Rescan() {
			message = "Settings changed successfully. They will be applied by the next synchronization."
		}

		return SuccessResponse {
			StrongMessage: "Success!",
			Message: message,
		}
	}, web.embedFS, fsPatterns...)
}
