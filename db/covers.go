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

// downloadCovers stores the covers of the library in the image cache of the data folder.
// Covers already cached are used right away; the others are downloaded in parallel.
func downloadCovers(dataFolder string, covers []coverDownload, progress ProgressUpdater) {
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

	assign := func(cover coverDownload, basename string) {
		if cover.icon {
			cover.title.Icon = basename
		} else {
			cover.title.Banner = basename
		}
	}

	pending := map[string][]coverDownload{}
	order := []string{}
	now := time.Now()
	for _, cover := range covers {
		basename := filepath.Base(cover.url)
		if info, err := os.Stat(filepath.Join(folder, basename)); err == nil && info.Size() > 0 {
			assign(cover, basename)
			continue
		}
		if failed, ok := failures[cover.url]; ok && now.Sub(time.Unix(failed, 0)) < coverRetryAfter {
			continue
		}
		if _, ok := pending[cover.url]; !ok {
			order = append(order, cover.url)
		}
		pending[cover.url] = append(pending[cover.url], cover)
	}
	if len(order) == 0 {
		return
	}

	var (
		mutex      sync.Mutex
		done       atomic.Int64
		inARow     atomic.Int64
		succeeded  atomic.Bool
		downloaded = map[string]bool{}
		jobs       = make(chan string)
		workers    sync.WaitGroup
	)
	for w := 0; w < coverWorkers; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for url := range jobs {
				err := DownloadFile(url, filepath.Join(folder, filepath.Base(url)))
				mutex.Lock()
				if err == nil {
					downloaded[url] = true
					delete(failures, url)
				} else {
					failures[url] = time.Now().Unix()
				}
				mutex.Unlock()
				if err == nil {
					succeeded.Store(true)
					inARow.Store(0)
				} else {
					inARow.Add(1)
				}
				if progress != nil {
					progress.UpdateProgress(int(done.Add(1)), len(order), "Downloading covers")
				}
			}
		}()
	}
	stopped := false
	for _, url := range order {
		if !succeeded.Load() && inARow.Load() >= coverMaxFailuresInARow {
			stopped = true
			break
		}
		jobs <- url
	}
	close(jobs)
	workers.Wait()
	if stopped {
		zap.S().Infof("The cover server cannot be reached, %v covers are left for the next scan", len(order)-int(done.Load()))
	}

	for url := range downloaded {
		for _, cover := range pending[url] {
			assign(cover, filepath.Base(url))
		}
	}
	if data, err := json.Marshal(failures); err == nil {
		os.WriteFile(failuresPath, data, 0644)
	}
}
