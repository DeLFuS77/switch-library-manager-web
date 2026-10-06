package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"go.uber.org/zap"
)

const (
	// parallel cover downloads
	coverWorkers = 4
	// a cover that could not be downloaded is not tried again for this long
	coverRetryAfter = 24 * time.Hour
	// after this many failures in a row without any success, the network is considered
	// unreachable and the remaining covers are left for the next scan
	coverMaxFailuresInARow = 5
	coverFailuresFilename  = ".failed.json"
	maxScanWorkers         = 16
)

// ScanWorkers is how many files are read at the same time: scan_workers in the settings,
// or by default up to 4, which keeps disks and network shares busy without saturating them.
func ScanWorkers(dataFolder string) int {
	workers := settings.ReadSettings(dataFolder).ScanWorkers
	if scanWorkersOverride > 0 {
		workers = scanWorkersOverride
	}
	if workers <= 0 {
		workers = runtime.NumCPU()
		if workers > 4 {
			workers = 4
		}
	}
	if workers > maxScanWorkers {
		workers = maxScanWorkers
	}
	return workers
}

// set by benchmarks to compare worker counts
var scanWorkersOverride int

// coverDownload is an icon or banner of a game in the library.
type coverDownload struct {
	title *SwitchGameFiles
	url   string
	icon  bool
}

func assignCover(cover coverDownload, basename string) {
	if cover.icon {
		cover.title.Icon = basename
	} else {
		cover.title.Banner = basename
	}
}

// CoverCached reports whether the cover at url is in the image cache of the data folder.
func CoverCached(dataFolder string, url string) bool {
	info, err := os.Stat(filepath.Join(dataFolder, "img", filepath.Base(url)))
	return err == nil && info.Size() > 0
}

// assignCachedCovers sets the covers that are already in the image cache and returns the
// others, which are downloaded later without holding up the scan.
func assignCachedCovers(dataFolder string, covers []coverDownload) []coverDownload {
	pending := []coverDownload{}
	for _, cover := range covers {
		if CoverCached(dataFolder, cover.url) {
			assignCover(cover, filepath.Base(cover.url))
		} else {
			pending = append(pending, cover)
		}
	}
	return pending
}

// CoversToTry returns the urls that are worth downloading now: covers that failed
// recently are tried again only after a day.
func CoversToTry(dataFolder string, urls []string) []string {
	failures := map[string]int64{}
	if data, err := os.ReadFile(filepath.Join(dataFolder, "img", coverFailuresFilename)); err == nil {
		json.Unmarshal(data, &failures)
	}
	now := time.Now()
	result := []string{}
	for _, url := range urls {
		if failed, ok := failures[url]; ok && now.Sub(time.Unix(failed, 0)) < coverRetryAfter {
			continue
		}
		result = append(result, url)
	}
	return result
}

// DownloadCovers stores the covers at urls in the image cache, a few at a time. done is
// called after each cover that was downloaded; stop, when not nil, is checked between
// downloads. Covers that failed recently are skipped, and when the cover server cannot be
// reached at all the rest is left for later.
func DownloadCovers(dataFolder string, urls []string, stop func() bool, done func(url string)) {
	folder := filepath.Join(dataFolder, "img")
	if err := os.MkdirAll(folder, 0755); err != nil {
		zap.S().Warnf("Covers are not cached: %v", err)
		return
	}

	failuresPath := filepath.Join(folder, coverFailuresFilename)
	failures := map[string]int64{}
	if data, err := os.ReadFile(failuresPath); err == nil {
		json.Unmarshal(data, &failures)
	}

	order := []string{}
	seen := map[string]bool{}
	now := time.Now()
	for _, url := range urls {
		if seen[url] {
			continue
		}
		seen[url] = true
		if failed, ok := failures[url]; ok && now.Sub(time.Unix(failed, 0)) < coverRetryAfter {
			continue
		}
		order = append(order, url)
	}
	if len(order) == 0 {
		return
	}

	var (
		mutex     sync.Mutex
		finished  atomic.Int64
		inARow    atomic.Int64
		succeeded atomic.Bool
		jobs      = make(chan string)
		workers   sync.WaitGroup
	)
	for w := 0; w < coverWorkers; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for url := range jobs {
				err := DownloadFile(url, filepath.Join(folder, filepath.Base(url)))
				mutex.Lock()
				if err == nil {
					delete(failures, url)
				} else {
					failures[url] = time.Now().Unix()
				}
				mutex.Unlock()
				finished.Add(1)
				if err == nil {
					succeeded.Store(true)
					inARow.Store(0)
					if done != nil {
						done(url)
					}
				} else {
					inARow.Add(1)
				}
			}
		}()
	}
	stopped := false
	for _, url := range order {
		if (!succeeded.Load() && inARow.Load() >= coverMaxFailuresInARow) || (stop != nil && stop()) {
			stopped = true
			break
		}
		jobs <- url
	}
	close(jobs)
	workers.Wait()
	if stopped {
		zap.S().Infof("%v covers are left for later", len(order)-int(finished.Load()))
	}

	mutex.Lock()
	data, err := json.Marshal(failures)
	mutex.Unlock()
	if err == nil {
		os.WriteFile(failuresPath, data, 0644)
	}
}

// downloadCovers sets the cached covers and downloads the others right away.
func downloadCovers(dataFolder string, covers []coverDownload, progress ProgressUpdater) {
	pending := assignCachedCovers(dataFolder, covers)
	byUrl := map[string][]coverDownload{}
	urls := []string{}
	for _, cover := range pending {
		if _, ok := byUrl[cover.url]; !ok {
			urls = append(urls, cover.url)
		}
		byUrl[cover.url] = append(byUrl[cover.url], cover)
	}
	var mutex sync.Mutex
	count := 0
	DownloadCovers(dataFolder, urls, nil, func(url string) {
		mutex.Lock()
		defer mutex.Unlock()
		for _, cover := range byUrl[url] {
			assignCover(cover, filepath.Base(url))
		}
		count++
		if progress != nil {
			progress.UpdateProgress(count, len(urls), "Downloading covers")
		}
	})
}
