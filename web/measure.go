package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// ServerMeasure is what the measure of the server found: how fast each setting compressed
// games of the library, and the setting chosen.
type ServerMeasure struct {
	Processors int            `json:"processors"`
	Results    []MeasureTrial `json:"results"`
	// the settings chosen
	Threads     int `json:"threads"`
	FilesAtOnce int `json:"files_at_once"`
}

// MeasureTrial is a setting tried and the MB of games it compressed per second, all files
// together.
type MeasureTrial struct {
	Files   int `json:"files"`
	Threads int `json:"threads"`
	Speed   int `json:"speed"`
}

// how long each setting is tried: seven settings make about half a minute
const measureTrialTime = 4 * time.Second

var errNoGameToMeasure = errorString("There is no game in the library to measure the disk with.")

// measureTrials are the settings tried, the lightest first.
func measureTrials(processors int) []MeasureTrial {
	// a fourth of the processors stays free for the rest of the server
	budget := max(1, processors*3/4)
	trials := []MeasureTrial{}
	for _, trial := range []MeasureTrial{{Files: 1, Threads: 1}, {Files: 1, Threads: 2}, {Files: 2, Threads: 1}, {Files: 1, Threads: 4},
		{Files: 2, Threads: 2}, {Files: 3, Threads: 1}, {Files: 4, Threads: 1}} {
		if trial.Files*trial.Threads <= budget {
			trials = append(trials, trial)
		}
	}
	return trials
}

// measureServer tries settings of the compression on the biggest games of the library, as
// a compression reads, decrypts and compresses them, and chooses the fastest. Each trial
// reads its own part of the games, so none of them finds the data already in memory.
func (web *Web) measureServer(ctx context.Context) (ServerMeasure, error) {
	result := ServerMeasure{Processors: runtime.NumCPU()}
	games := web.libraryFiles(".nsp", ".xci")
	sort.Slice(games, func(i, j int) bool { return games[i].Size > games[j].Size })
	if len(games) == 0 {
		return result, errNoGameToMeasure
	}
	games = games[:min(len(games), maxFilesAtOnce)]
	level := settings.ReadSettings(web.dataFolder).AutoCompressLevel

	trials := measureTrials(result.Processors)
	// every file of every trial starts at its own place in its game
	places := 0
	for _, trial := range trials {
		places += trial.Files
	}
	place := 0
	for i, trial := range trials {
		var (
			total int64
			mutex sync.Mutex
			wait  sync.WaitGroup
			first error
		)
		start := time.Now()
		for f := 0; f < trial.Files; f++ {
			wait.Add(1)
			go func(game string, from float64) {
				defer wait.Done()
				done, err := web.measureOne(ctx, game, from, switchfs.CompressOptions{Level: level, Workers: trial.Threads})
				mutex.Lock()
				defer mutex.Unlock()
				total += done
				if err != nil && first == nil {
					first = err
				}
			}(games[f%len(games)].Path, float64(place)/float64(places))
			place++
		}
		wait.Wait()
		if first != nil {
			return result, first
		}
		trials[i].Speed = megabytesPerSecond(total, time.Since(start))
	}
	result.Results = trials
	result.Threads, result.FilesAtOnce = chooseTrial(trials)
	return result, nil
}

// measureMaxWrite is the most a trial may write: what a fast computer compresses in the
// time of a trial.
const measureMaxWrite = 4 << 30

// measureOne compresses a part of a game for a trial into a hidden file next to it, as a
// compression writes its NSZ there, and deletes it. Without the free space for it, nothing
// is written: the measure then leaves the writing out.
func (web *Web) measureOne(ctx context.Context, game string, from float64, options switchfs.CompressOptions) (int64, error) {
	folder := filepath.Dir(game)
	release, err := web.reserveSpace(folder, measureMaxWrite)
	if err != nil {
		return switchfs.MeasureCompression(ctx, game, from, options, measureTrialTime, io.Discard)
	}
	defer release()
	output, err := os.CreateTemp(folder, ".slm-measure-*.tmp")
	if err != nil {
		return switchfs.MeasureCompression(ctx, game, from, options, measureTrialTime, io.Discard)
	}
	defer os.Remove(output.Name())
	defer output.Close()
	return switchfs.MeasureCompression(ctx, game, from, options, measureTrialTime, output)
}

func megabytesPerSecond(bytes int64, elapsed time.Duration) int {
	if elapsed <= 0 {
		return 0
	}
	return int(float64(bytes) / (1 << 20) / elapsed.Seconds())
}

// chooseTrial gives the fastest setting tried; a heavier one must be clearly faster than a
// lighter one, as the measure varies a little.
func chooseTrial(trials []MeasureTrial) (threads int, files int) {
	best := MeasureTrial{Files: 1, Threads: 1}
	for _, trial := range trials {
		if float64(trial.Speed) > float64(best.Speed)*1.1 {
			best = trial
		}
	}
	return best.Threads, best.Files
}

func (web *Web) HandleMeasure() {
	web.router.HandleFunc("/settings/measure", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		// one measure at a time, and not while a task works on the files: it would
		// measure the task instead of the server
		if !web.measuring.CompareAndSwap(false, true) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": translate(lang, string(errOrganizeBusy))})
			return
		}
		if web.organizing.Load() || web.compressionRunning() {
			web.measuring.Store(false)
			writeJSON(w, http.StatusConflict, map[string]string{"error": translate(lang, string(errOrganizeBusy))})
			return
		}
		// once the measure ends, the next task of the queue
		defer func() {
			web.measuring.Store(false)
			go web.runQueue()
		}()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		result, err := web.measureServer(ctx)
		if err != nil {
			status, message := http.StatusInternalServerError, err.Error()
			switch {
			case errors.Is(err, errNoGameToMeasure):
				status, message = http.StatusConflict, translate(lang, message)
			case errors.Is(err, switchfs.ErrNothingToMeasure):
				status, message = http.StatusConflict, translate(lang, string(errNoGameToMeasure))
			}
			writeJSON(w, status, map[string]string{"error": message})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}).Methods("POST")
}
