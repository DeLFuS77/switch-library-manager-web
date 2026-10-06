package web

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Heavy background work (cover downloads, thumbnails, icons from game files, scheduled
// checks and automatic compression of new files) can be limited to some hours, so a NAS
// stays responsive while it is used. Work found outside those hours waits for them.

// allowed values of background_hours: "" means at any time
var allowedBackgroundHours = map[string]struct{}{"": {}, "22-6": {}, "0-6": {}, "1-7": {}, "2-8": {}}

// inBackgroundHours reports whether heavy work may run at now.
func inBackgroundHours(s *settings.AppSettings, now time.Time) bool {
	from, to, ok := parseHours(s.BackgroundHours)
	if !ok {
		return true
	}
	hour := now.Hour()
	if from < to {
		return hour >= from && hour < to
	}
	// over midnight, e.g. 22-6
	return hour >= from || hour < to
}

func parseHours(value string) (int, int, bool) {
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	from, err1 := strconv.Atoi(parts[0])
	to, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || from < 0 || from > 23 || to < 0 || to > 23 || from == to {
		return 0, 0, false
	}
	return from, to, true
}

// backgroundWork remembers the work that waits for the background hours.
type backgroundWork struct {
	mutex    sync.Mutex
	covers   bool
	compress bool
	// set by "Search covers again": that run ignores the hours
	forced bool
}

// backgroundAllowed reports whether heavy background work may run now.
func (web *Web) backgroundAllowed() bool {
	web.background.mutex.Lock()
	forced := web.background.forced
	web.background.mutex.Unlock()
	return forced || inBackgroundHours(settings.ReadSettings(web.dataFolder), time.Now())
}

// waitForBackgroundHours keeps the cover work for the next background hours.
func (web *Web) waitForBackgroundHours() {
	web.background.mutex.Lock()
	web.background.covers = true
	web.background.mutex.Unlock()
}

// waitToCompress keeps the automatic compression of new files for the background hours.
func (web *Web) waitToCompress() {
	web.background.mutex.Lock()
	web.background.compress = true
	web.background.mutex.Unlock()
}

// resumeBackgroundWork starts the work that waited, once the background hours begin.
// The scheduler calls it every minute.
func (web *Web) resumeBackgroundWork(now time.Time) {
	if web.state.IsSynchronizing() || !inBackgroundHours(settings.ReadSettings(web.dataFolder), now) {
		return
	}
	web.background.mutex.Lock()
	covers, compress := web.background.covers, web.background.compress
	web.background.covers, web.background.compress = false, false
	web.background.mutex.Unlock()
	if covers {
		web.sugarLogger.Info("[Background hours: covers and thumbnails]")
		web.startCoverDownloads()
	}
	if compress && web.autoCompress(TRIGGER_SCHEDULE) {
		web.sugarLogger.Info("[Background hours: compressing new files]")
	}
}
