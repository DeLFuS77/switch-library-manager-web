package web

import (
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/fsnotify/fsnotify"
)

const (
	// network shares do not report changes, so the folders are also checked regularly
	watchPollInterval = 2 * time.Minute
	// wait for this long without file events before checking, so a copy can finish
	watchQuietPeriod = 10 * time.Second
	// a folder must look the same twice in a row, this far apart, before it is scanned
	watchStableDelay = 3 * time.Second
)

// scanFolders returns the configured library folders.
func scanFolders(s *settings.AppSettings) []string {
	folders := []string{}
	for _, folder := range append([]string{s.Folder}, s.ScanFolders...) {
		if folder != "" {
			folders = append(folders, folder)
		}
	}
	return folders
}

// folderFingerprint summarizes the paths, sizes and modification times of every file in
// the folders. It changes when a file is added, removed, renamed or rewritten.
func folderFingerprint(folders []string) uint64 {
	entries := []string{}
	for _, folder := range folders {
		filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				// unreadable folders are skipped, like the scan does
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			entries = append(entries, path+"\x00"+strconv.FormatInt(info.Size(), 10)+"\x00"+strconv.FormatInt(info.ModTime().UnixNano(), 10))
			return nil
		})
	}
	sort.Strings(entries)

	hash := fnv.New64a()
	for _, entry := range entries {
		hash.Write([]byte(entry))
		hash.Write([]byte{'\n'})
	}
	return hash.Sum64()
}

// folderWatcher rescans the library when the files in the library folders change.
type folderWatcher struct {
	web       *Web
	notify    *fsnotify.Watcher
	watched   map[string]struct{}
	scanned   uint64
	lastCheck uint64
}

// StartFolderWatcher watches the library folders and rescans the library when files are
// added, removed or replaced. Local folders report changes right away; every folder is
// also checked every few minutes, which covers network shares.
func (web *Web) StartFolderWatcher() {
	notify, err := fsnotify.NewWatcher()
	if err != nil {
		// polling still works
		web.sugarLogger.Warnf("File change notifications are not available, the folders are checked every %v - %v", watchPollInterval, err)
	}

	w := &folderWatcher{web: web, notify: notify, watched: map[string]struct{}{}}
	// the library may come from the cache of the previous run: files changed while the app
	// was stopped are picked up by the first check, as nothing counts as scanned yet
	w.lastCheck = folderFingerprint(scanFolders(settings.ReadSettings(web.dataFolder)))
	w.updateWatches()

	go w.run()
}

func (w *folderWatcher) run() {
	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()

	quiet := time.NewTimer(time.Hour)
	quiet.Stop()

	var events chan fsnotify.Event
	var errs chan error
	if w.notify != nil {
		events = w.notify.Events
		errs = w.notify.Errors
	}

	for {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if event.Has(fsnotify.Create) {
				// new sub-folders are watched too
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					w.add(event.Name)
				}
			}
			quiet.Reset(watchQuietPeriod)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			w.web.sugarLogger.Debugf("Folder watcher error: %v", err)
		case <-quiet.C:
			w.checkStable()
		case <-ticker.C:
			w.updateWatches()
			w.checkPolled()
		}
	}
}

// checkStable scans the library if the folders changed and stopped changing.
func (w *folderWatcher) checkStable() {
	if !w.enabled() {
		return
	}
	folders := scanFolders(settings.ReadSettings(w.web.dataFolder))
	first := folderFingerprint(folders)
	time.Sleep(watchStableDelay)
	second := folderFingerprint(folders)
	w.lastCheck = second
	if first == second {
		w.scanIfChanged(second)
	}
}

// checkPolled scans the library if the folders changed since the last scan and look the
// same as at the previous check, so files still being copied are not scanned half-written.
func (w *folderWatcher) checkPolled() {
	if !w.enabled() {
		return
	}
	current := folderFingerprint(scanFolders(settings.ReadSettings(w.web.dataFolder)))
	previous := w.lastCheck
	w.lastCheck = current
	if current == previous {
		w.scanIfChanged(current)
	}
}

func (w *folderWatcher) scanIfChanged(fingerprint uint64) {
	if fingerprint == w.scanned {
		return
	}
	// walks the folders again; the metadata of unchanged files comes from the cache, so this is quick
	if w.web.scanInBackground(true) {
		w.scanned = fingerprint
		w.web.sugarLogger.Info("[Library folders changed, scanning]")
	}
	// if a synchronization is running, the next check tries again
}

func (w *folderWatcher) enabled() bool {
	return settings.ReadSettings(w.web.dataFolder).WatchFolders
}

// updateWatches follows changes of the configured folders.
func (w *folderWatcher) updateWatches() {
	if w.notify == nil {
		return
	}

	wanted := map[string]struct{}{}
	if w.enabled() {
		for _, folder := range scanFolders(settings.ReadSettings(w.web.dataFolder)) {
			filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
				if err == nil && d.IsDir() {
					wanted[path] = struct{}{}
				}
				return nil
			})
		}
	}

	for path := range w.watched {
		if _, ok := wanted[path]; !ok {
			w.notify.Remove(path)
			delete(w.watched, path)
		}
	}
	for path := range wanted {
		w.add(path)
	}
}

func (w *folderWatcher) add(path string) {
	if _, ok := w.watched[path]; ok {
		return
	}
	if err := w.notify.Add(path); err != nil {
		w.web.sugarLogger.Debugf("Not watching %s: %v", path, err)
		return
	}
	w.watched[path] = struct{}{}
}
