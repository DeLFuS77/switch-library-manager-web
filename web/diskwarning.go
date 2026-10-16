package web

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const (
	// a volume of the library is short of space below this share of it free
	lowDiskPercent = 10
	// and it is fine again above this one, so a volume around the margin is not reported
	// again and again
	lowDiskRecoveredPercent = 12
	// how long a measure of the volumes is kept
	diskCheckInterval = time.Minute
)

// LowDisk is a volume of the library short of free space.
type LowDisk struct {
	Folder  string
	Free    int64
	Total   int64
	Percent int
}

// diskWatch keeps the last measure of the volumes and the ones already reported.
type diskWatch struct {
	mutex    sync.Mutex
	checked  time.Time
	low      []LowDisk
	reported map[string]bool
}

// lowDisks lists the volumes of the library folders with less than lowDiskPercent free,
// measured at most once a minute; every volume is listed once, under its first folder.
func (web *Web) lowDisks() []LowDisk {
	if isDemoMode() {
		return nil
	}
	web.disks.mutex.Lock()
	defer web.disks.mutex.Unlock()
	if time.Since(web.disks.checked) < diskCheckInterval {
		return web.disks.low
	}
	web.disks.checked = time.Now()
	web.disks.low = measureLowDisks(scanFolders(settings.ReadSettings(web.dataFolder)), lowDiskPercent)
	return web.disks.low
}

// librarySpace is the free space and the size of the disks of the library folders, each
// disk counted once.
func (web *Web) librarySpace() (int64, int64) {
	var free, total int64
	seen := map[[2]uint64]bool{}
	for _, folder := range scanFolders(settings.ReadSettings(web.dataFolder)) {
		available, size, err := diskSpace(folder)
		if err != nil || size == 0 || seen[[2]uint64{available, size}] {
			continue
		}
		seen[[2]uint64{available, size}] = true
		free += int64(available)
		total += int64(size)
	}
	return free, total
}

// measureLowDisks measures the volumes of the folders and lists the ones with less than
// percent free.
func measureLowDisks(folders []string, percent int) []LowDisk {
	low := []LowDisk{}
	seen := map[[2]uint64]bool{}
	for _, folder := range folders {
		free, total, err := diskSpace(folder)
		if err != nil || total == 0 {
			continue
		}
		// folders on the same volume give the same figures
		volume := [2]uint64{free, total}
		if seen[volume] {
			continue
		}
		seen[volume] = true
		if free*100 < total*uint64(percent) {
			low = append(low, LowDisk{Folder: filepath.Clean(folder), Free: int64(free), Total: int64(total), Percent: int(free * 100 / total)})
		}
	}
	return low
}

// checkDiskSpace sends a notification when a volume of the library becomes short of
// space; it is sent again only after the volume had room again. The scheduler calls it
// every minute.
func (web *Web) checkDiskSpace() {
	low := web.lowDisks()
	// the volumes reported stay so until they are above the higher margin
	stillLow := map[string]bool{}
	if len(low) > 0 || web.disks.anyReported() {
		for _, disk := range measureLowDisks(scanFolders(settings.ReadSettings(web.dataFolder)), lowDiskRecoveredPercent) {
			stillLow[disk.Folder] = true
		}
	}
	web.disks.mutex.Lock()
	if web.disks.reported == nil {
		web.disks.reported = map[string]bool{}
	}
	for folder := range web.disks.reported {
		if !stillLow[folder] {
			delete(web.disks.reported, folder)
		}
	}
	fresh := []LowDisk{}
	for _, disk := range low {
		if !web.disks.reported[disk.Folder] {
			web.disks.reported[disk.Folder] = true
			fresh = append(fresh, disk)
		}
	}
	web.disks.mutex.Unlock()
	if len(fresh) > 0 {
		web.notifyLowDisks(fresh)
	}
}

func (d *diskWatch) anyReported() bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return len(d.reported) > 0
}

// notifyLowDisks reports the volumes short of space to the configured channels.
func (web *Web) notifyLowDisks(disks []LowDisk) {
	settingsObj := settings.ReadSettings(web.dataFolder)
	options := settingsObj.Notifications
	if !notificationsConfigured(options) {
		return
	}
	lang := settingsObj.Language
	if !isSupportedLanguage(lang) {
		lang = DEFAULT_LANGUAGE
	}
	items := []NotificationItem{}
	for _, disk := range disks {
		items = append(items, NotificationItem{Key: "disk:" + disk.Folder, Kind: "disk", Name: disk.Folder, Detail: translatef(lang, "%v free (%v%%)", formatSize(disk.Free), disk.Percent)})
	}
	title := translate(lang, "Switch Library Manager: little free space left")
	text := ""
	for _, item := range items {
		text += "• " + translatef(lang, "%v: %v", item.Name, item.Detail) + "\n"
	}
	text += translate(lang, "Free space from the Space page of the app; compressions wait until there is room.")
	if err := sendMessage(options, title, text, items); err != nil {
		web.sugarLogger.Warnf("Failed to send notification: %v", err)
		return
	}
	web.sugarLogger.Infof("Notification sent: little free space left on %d volume(s)", len(disks))
}

// roomToCompress tells whether the volume of a file can take its compressed copy, written
// beside it before the original is deleted: automatic compression waits otherwise.
func roomToCompress(path string, size int64) bool {
	free, err := diskFree(filepath.Dir(path))
	return err != nil || int64(free) >= size+(64<<20)
}
