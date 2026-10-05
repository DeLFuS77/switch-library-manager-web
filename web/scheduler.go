package web

import (
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// allowed values of sync_interval_hours; 0 disables scheduled synchronization
var allowedSyncIntervals = map[int]struct{}{0: {}, 6: {}, 12: {}, 24: {}, 168: {}}

// nextSyncTime returns when the next scheduled synchronization is due, or the zero time
// if scheduled synchronization is disabled.
func nextSyncTime(s *settings.AppSettings) time.Time {
	if s.SyncIntervalHours <= 0 {
		return time.Time{}
	}
	if s.LastSyncTime.IsZero() {
		// never synchronized: due now
		return time.Unix(0, 0)
	}
	return s.LastSyncTime.Add(time.Duration(s.SyncIntervalHours) * time.Hour)
}

func syncDue(s *settings.AppSettings, now time.Time) bool {
	next := nextSyncTime(s)
	return !next.IsZero() && !now.Before(next)
}

// StartScheduler checks every minute whether a scheduled synchronization is due.
func (web *Web) StartScheduler() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			if syncDue(settings.ReadSettings(web.dataFolder), now) && web.Synchronize(TRIGGER_SCHEDULE) {
				web.sugarLogger.Info("[Scheduled synchronization started]")
			}
		}
	}()
}
