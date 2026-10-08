package web

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// automatic compression modes
const (
	AUTO_COMPRESS_OFF   = ""
	AUTO_COMPRESS_NEW   = "new"
	AUTO_COMPRESS_NIGHT = "night"
	// local hour of the nightly compression
	autoCompressHour = 3
)

var allowedAutoCompress = map[string]struct{}{AUTO_COMPRESS_OFF: {}, AUTO_COMPRESS_NEW: {}, AUTO_COMPRESS_NIGHT: {}}

// autoCompressor remembers the files it could not compress, so they are not tried again
// until they change, and the day of the last nightly run.
type autoCompressor struct {
	mutex   sync.Mutex
	failed  map[string]time.Time // path -> modification time of the failed attempt
	lastDay string
}

// autoCompressCandidates are the NSP and XCI files of the library without a compressed
// copy, except those that failed and did not change since.
func (web *Web) autoCompressCandidates() []string {
	web.auto.mutex.Lock()
	defer web.auto.mutex.Unlock()
	if web.auto.failed == nil {
		web.auto.failed = map[string]time.Time{}
	}
	paths := []string{}
	for _, candidate := range web.compressCandidates() {
		info, err := os.Stat(candidate.Path)
		if err != nil {
			continue
		}
		if _, err := os.Stat(switchfs.CompressedPath(candidate.Path)); err == nil {
			continue
		}
		if failedAt, ok := web.auto.failed[candidate.Path]; ok && failedAt.Equal(info.ModTime()) {
			continue
		}
		paths = append(paths, candidate.Path)
	}
	return paths
}

// autoCompress compresses the files waiting for it, when automatic compression is on
// and the keys are available. It returns whether a compression was started.
func (web *Web) autoCompress(trigger string) bool {
	appSettings := settings.ReadSettings(web.dataFolder)
	if appSettings.AutoCompress == AUTO_COMPRESS_OFF {
		return false
	}
	if keys, _ := settings.SwitchKeys(); keys == nil || keys.GetKey("header_key") == "" {
		return false
	}
	if appSettings.AutoCompress == AUTO_COMPRESS_NEW && !web.backgroundAllowed() {
		web.waitToCompress()
		return false
	}
	paths := web.autoCompressCandidates()
	if len(paths) == 0 {
		return false
	}
	return web.compressPaths(paths, trigger)
}

// compressPaths compresses files as one task, with the level and the keeping of the originals
// of the automatic compression; files that fail are not tried again until they change.
func (web *Web) compressPaths(paths []string, trigger string) bool {
	appSettings := settings.ReadSettings(web.dataFolder)
	level := appSettings.AutoCompressLevel
	if level != switchfs.LevelFast && level != switchfs.LevelMax {
		level = switchfs.LevelBalanced
	}
	web.auto.mutex.Lock()
	if web.auto.failed == nil {
		web.auto.failed = map[string]time.Time{}
	}
	web.auto.mutex.Unlock()
	return web.runFileTaskWithTrigger(TASK_COMPRESS, trigger, paths, 3, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		saved, err := web.compressFile(ctx, path, level, !appSettings.AutoCompressKeep, report)
		if err != nil {
			if info, statErr := os.Stat(path); statErr == nil {
				web.auto.mutex.Lock()
				web.auto.failed[path] = info.ModTime()
				web.auto.mutex.Unlock()
			}
		}
		return saved, err
	})
}

// nightlyCompressDue reports whether the nightly compression should run now; it runs
// once a day, in the hour after autoCompressHour.
func (web *Web) nightlyCompressDue(now time.Time) bool {
	if settings.ReadSettings(web.dataFolder).AutoCompress != AUTO_COMPRESS_NIGHT || now.Hour() != autoCompressHour {
		return false
	}
	day := now.Format("2006-01-02")
	web.auto.mutex.Lock()
	defer web.auto.mutex.Unlock()
	if web.auto.lastDay == day {
		return false
	}
	web.auto.lastDay = day
	return true
}
