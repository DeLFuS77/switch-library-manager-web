package web

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

const (
	// downloaded covers are shown in batches, so pages are not rebuilt after every image
	coverBatchInterval = 15 * time.Second
	coverBatchSize     = 250
)

// coverLoader downloads the covers of the library after a scan, in the background.
type coverLoader struct {
	mutex   sync.Mutex
	running bool
	// a scan finished while covers were being downloaded: look again when done
	again bool
}

// coverUpdate is the icon and banner found for a game, by title ID prefix.
type coverUpdate struct {
	icon   string
	banner string
}

// coverTarget is a game waiting for a cover.
type coverTarget struct {
	prefix string
	icon   bool
}

// startCoverDownloads downloads the missing covers of the library in the background and
// shows them as they arrive. The library is usable meanwhile; covers not shown yet use the
// placeholder image.
func (web *Web) startCoverDownloads() {
	web.covers.mutex.Lock()
	if web.covers.running {
		web.covers.again = true
		web.covers.mutex.Unlock()
		return
	}
	web.covers.running = true
	web.covers.mutex.Unlock()

	go func() {
		for {
			web.downloadMissingCovers()
			web.covers.mutex.Lock()
			if !web.covers.again {
				web.covers.running = false
				web.covers.mutex.Unlock()
				return
			}
			web.covers.again = false
			web.covers.mutex.Unlock()
		}
	}()
}

func (web *Web) downloadMissingCovers() {
	switchDB, localDB := web.state.get()
	if switchDB == nil || localDB == nil {
		return
	}

	// covers cached by an earlier run are shown at once, the others are downloaded
	ready := map[string]coverUpdate{}
	waiting := map[string][]coverTarget{}
	urls := []string{}
	need := func(prefix string, url string, icon bool) {
		if db.CoverCached(web.dataFolder, url) {
			update := ready[prefix]
			if icon {
				update.icon = filepath.Base(url)
			} else {
				update.banner = filepath.Base(url)
			}
			ready[prefix] = update
			return
		}
		if _, ok := waiting[url]; !ok {
			urls = append(urls, url)
		}
		waiting[url] = append(waiting[url], coverTarget{prefix: prefix, icon: icon})
	}
	for prefix, local := range localDB.TitlesMap {
		title, ok := switchDB.TitlesMap[prefix]
		if !ok || !local.BaseExist {
			continue
		}
		if local.Icon == "" && title.Attributes.IconUrl != "" {
			need(prefix, title.Attributes.IconUrl, true)
		}
		if local.Banner == "" && title.Attributes.BannerUrl != "" {
			need(prefix, title.Attributes.BannerUrl, false)
		}
	}
	if len(ready) > 0 {
		web.state.applyCovers(ready)
	}
	// covers that failed recently (removed from the cover server) wait a day
	urls = db.CoversToTry(web.dataFolder, urls)
	if len(urls) == 0 {
		extracted := web.extractMissingIcons(0)
		web.saveCovers(len(ready) > 0 || extracted > 0)
		web.pregenerateThumbnails()
		return
	}
	web.sugarLogger.Infof("[Downloading %d covers in the background]", len(urls))
	taskId := web.taskLog().Start(TASK_COVERS, TRIGGER_SCAN)
	web.taskLog().Progress(taskId, 0, len(urls), "Downloading covers")

	var mutex sync.Mutex
	batch := map[string]coverUpdate{}
	lastFlush := time.Now()
	downloaded := 0
	flush := func() {
		if len(batch) > 0 {
			web.state.applyCovers(batch)
			batch = map[string]coverUpdate{}
		}
		lastFlush = time.Now()
	}
	db.DownloadCovers(web.dataFolder, urls,
		// a scan replaces the library: stop, and start again after it
		func() bool { return web.state.IsSynchronizing() },
		func(url string) {
			mutex.Lock()
			defer mutex.Unlock()
			downloaded++
			web.taskLog().Progress(taskId, downloaded, len(urls), "Downloading covers")
			for _, target := range waiting[url] {
				update := batch[target.prefix]
				if target.icon {
					update.icon = filepath.Base(url)
				} else {
					update.banner = filepath.Base(url)
				}
				batch[target.prefix] = update
			}
			if len(batch) >= coverBatchSize || time.Since(lastFlush) >= coverBatchInterval {
				flush()
			}
		})
	mutex.Lock()
	flush()
	mutex.Unlock()
	web.sugarLogger.Infof("[%d covers downloaded]", downloaded)
	web.taskLog().SetResult(taskId, 0, downloaded)
	if web.state.IsSynchronizing() {
		// continue after the scan that interrupted the downloads
		web.taskLog().Warn(taskId, NOTE_PAUSED_FOR_SCAN, "")
		web.taskLog().Finish(taskId, nil)
		web.covers.mutex.Lock()
		web.covers.again = true
		web.covers.mutex.Unlock()
		return
	}
	// games still without a cover take the icon stored in their files
	extracted := web.extractMissingIcons(taskId)
	web.taskLog().SetResult(taskId, 0, downloaded+extracted)
	web.taskLog().Finish(taskId, nil)
	web.saveCovers(downloaded > 0 || extracted > 0 || len(ready) > 0)
	web.pregenerateThumbnails()
}

// saveCovers stores the library with its covers for the next start.
func (web *Web) saveCovers(changed bool) {
	if !changed || web.localDbManager == nil || web.state.IsSynchronizing() {
		return
	}
	_, localDB := web.state.get()
	if localDB == nil {
		return
	}
	if err := web.localDbManager.SaveTitles(localDB.TitlesMap); err != nil {
		web.sugarLogger.Warnf("The covers could not be saved: %v", err)
	}
}

// applyCovers replaces the library with a copy that has the new covers, so pages reading
// the current one are not affected.
func (s *WebState) applyCovers(updates map[string]coverUpdate) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.localDB == nil {
		return
	}
	titles := make(map[string]*db.SwitchGameFiles, len(s.localDB.TitlesMap))
	for prefix, title := range s.localDB.TitlesMap {
		titles[prefix] = title
	}
	for prefix, update := range updates {
		title, ok := titles[prefix]
		if !ok {
			continue
		}
		changed := *title
		if update.icon != "" {
			changed.Icon = update.icon
		}
		if update.banner != "" {
			changed.Banner = update.banner
		}
		titles[prefix] = &changed
	}
	library := *s.localDB
	library.TitlesMap = titles
	s.localDB = &library
	s.version++
}
