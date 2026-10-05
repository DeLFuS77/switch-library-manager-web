package web

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// System versions are stored as major << 26 | minor << 20 | micro << 16 | build.

var firmwarePattern = regexp.MustCompile(`^\d{1,2}\.\d{1,2}\.\d{1,2}$`)

// firmwareVersion formats a system version, e.g. 12.1.0, or "" when there is none.
func firmwareVersion(code int) string {
	if code <= 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", code>>26, (code>>20)&0x3F, (code>>16)&0xF)
}

// parseFirmware reads a version like 18.1.0 as a system version.
func parseFirmware(text string) (int, bool) {
	text = strings.TrimSpace(text)
	if !firmwarePattern.MatchString(text) {
		return 0, false
	}
	parts := strings.Split(text, ".")
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	micro, _ := strconv.Atoi(parts[2])
	if major > 63 || minor > 63 || micro > 15 {
		return 0, false
	}
	return major<<26 | minor<<20 | micro<<16, true
}

// requiredSystemVersion is the firmware a file needs: games and updates store it in their
// metadata (for DLC the same field is a version of the game, not a firmware).
func requiredSystemVersion(info db.SwitchFileInfo) int {
	if info.Metadata == nil || info.Metadata.Type == "DLC" {
		return 0
	}
	return info.Metadata.RequiredTitleVersion
}

// installedRequirement is the firmware the installed game needs: its latest update's, or
// the game's when it has none.
func installedRequirement(local *db.SwitchGameFiles) int {
	if local == nil {
		return 0
	}
	required := 0
	if local.BaseExist {
		required = requiredSystemVersion(local.File)
	}
	if update, ok := local.Updates[local.LatestUpdate]; ok {
		if code := requiredSystemVersion(update); code > required {
			required = code
		}
	}
	return required
}

// consoleFirmware is the firmware of the user's console from the settings, or 0.
func (web *Web) consoleFirmware() int {
	code, _ := parseFirmware(settings.ReadSettings(web.dataFolder).ConsoleFirmware)
	return code
}

// firmwareTooNew reports whether a file needs a newer firmware than the console has.
func (web *Web) firmwareTooNew(required int) bool {
	console := web.consoleFirmware()
	return console > 0 && required > console
}
