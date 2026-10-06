package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

const (
	VERIFY_FILENAME = "verify.json"
	// a damaged file found by a verification, shown in Issues
	NOTE_VERIFY_DAMAGED = "The file is damaged or was modified."
	NOTE_VERIFY_FAILED  = "The files could not be checked."
)

// allowed values of verify_interval_days; 0 disables scheduled verification
var allowedVerifyIntervals = map[int]struct{}{0: {}, 7: {}, 30: {}}

// verifyRecord is the result of checking a file, valid while its size and modification
// time do not change.
type verifyRecord struct {
	Size    int64     `json:"size"`
	ModTime int64     `json:"mod_time"`
	OK      bool      `json:"ok"`
	Reason  string    `json:"reason,omitempty"`
	Checked time.Time `json:"checked"`
}

// verifyStore keeps the results in verify.json in the data folder.
type verifyStore struct {
	mutex   sync.Mutex
	path    string
	records map[string]verifyRecord
	// when the last verification finished
	LastRun time.Time
}

type verifyFile struct {
	LastRun time.Time               `json:"last_run"`
	Records map[string]verifyRecord `json:"records"`
}

func loadVerifyStore(dataFolder string) *verifyStore {
	store := &verifyStore{path: filepath.Join(dataFolder, VERIFY_FILENAME), records: map[string]verifyRecord{}}
	if data, err := os.ReadFile(store.path); err == nil {
		saved := verifyFile{}
		if json.Unmarshal(data, &saved) == nil && saved.Records != nil {
			store.records = saved.Records
			store.LastRun = saved.LastRun
		}
	}
	return store
}

// reload reads the results file again, after it was replaced.
func (s *verifyStore) reload() {
	loaded := loadVerifyStore(filepath.Dir(s.path))
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.records = loaded.records
	s.LastRun = loaded.LastRun
}

func (s *verifyStore) save() error {
	s.mutex.Lock()
	data, err := json.MarshalIndent(verifyFile{LastRun: s.LastRun, Records: s.records}, "", " ")
	s.mutex.Unlock()
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// current returns the record of a file if it is still valid for the file as it is now.
func (s *verifyStore) current(path string, info os.FileInfo) (verifyRecord, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	record, ok := s.records[path]
	if !ok || record.Size != info.Size() || record.ModTime != info.ModTime().UnixNano() {
		return verifyRecord{}, false
	}
	return record, true
}

func (s *verifyStore) set(path string, record verifyRecord) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.records[path] = record
}

// damaged returns the files found damaged that are still unchanged, by path.
func (s *verifyStore) damaged() map[string]string {
	s.mutex.Lock()
	records := make(map[string]verifyRecord, len(s.records))
	for path, record := range s.records {
		records[path] = record
	}
	s.mutex.Unlock()

	result := map[string]string{}
	for path, record := range records {
		if record.OK {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.Size() == record.Size && info.ModTime().UnixNano() == record.ModTime {
			result[path] = record.Reason
		}
	}
	return result
}

func (web *Web) verifications() *verifyStore {
	web.verifyOnce.Do(func() {
		if web.verify == nil {
			web.verify = loadVerifyStore(web.dataFolder)
		}
	})
	return web.verify
}

// verifyLibraryFile checks one file: the NCA files of NSP and XCI files against their
// content IDs, and NSZ and XCZ files by decompressing them. No keys are needed.
func verifyLibraryFile(ctx context.Context, path string, progress func(int64, int64)) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".nsz", ".xcz":
		return switchfs.VerifyCompressed(ctx, path, nil, progress)
	default:
		_, err := switchfs.HashNcas(ctx, path, progress)
		return err
	}
}

// files checked again by a scheduled verification once their last check is this old
// (see recheckAfter); new and changed files are always checked
const (
	verifyRecheckChanged = time.Duration(0)
	verifyRecheckAll     = time.Duration(-1)
	// the results are saved this often, so a stopped verification goes on where it was
	verifySaveInterval = 2 * time.Minute
)

// verify speeds: how many files are checked at the same time
const (
	VERIFY_SPEED_LOW    = "low"
	VERIFY_SPEED_NORMAL = "normal"
	VERIFY_SPEED_FAST   = "fast"
)

var allowedVerifySpeeds = map[string]struct{}{"": {}, VERIFY_SPEED_LOW: {}, VERIFY_SPEED_NORMAL: {}, VERIFY_SPEED_FAST: {}}

// verifyWorkers returns how many files are checked at the same time. Compressed files are
// decompressed to be checked, which keeps one processor core busy per file; the files of a
// NAS are spread over several disks, so reading a few at once costs little.
func verifyWorkers(speed string, cores int) int {
	workers := 1
	switch speed {
	case VERIFY_SPEED_LOW:
		return 1
	case VERIFY_SPEED_FAST:
		workers = min(cores-1, 8)
	default:
		workers = min(cores/2, 4)
	}
	return max(workers, 1)
}

// startVerification checks the files of the library in the background; with onlyChanged
// only the files that are new or changed since they were last checked. It returns false
// if a compression or verification is already running.
func (web *Web) startVerification(onlyChanged bool, trigger string) bool {
	recheck := verifyRecheckAll
	if onlyChanged {
		recheck = verifyRecheckChanged
	}
	return web.startVerificationOf(nil, recheck, trigger)
}

// startVerificationOf checks the given files, or every file of the library when paths is
// nil: all of them (verifyRecheckAll), the new and changed ones (verifyRecheckChanged), or
// also the ones last checked longer ago than recheck. The files never checked go first, then
// the ones checked the longest ago.
func (web *Web) startVerificationOf(paths []string, recheck time.Duration, trigger string) bool {
	web.compressor.mutex.Lock()
	if web.compressor.cancel != nil {
		web.compressor.mutex.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	web.compressor.cancel = cancel
	web.compressor.mutex.Unlock()

	store := web.verifications()
	taskId := web.taskLog().Start(TASK_VERIFY, trigger)
	go func() {
		defer func() {
			web.compressor.mutex.Lock()
			web.compressor.cancel = nil
			web.compressor.mutex.Unlock()
			cancel()
		}()

		type pending struct {
			path    string
			info    os.FileInfo
			checked time.Time
		}
		files := []pending{}
		var total int64
		if paths == nil {
			for _, candidate := range web.libraryFiles(".nsp", ".nsz", ".xci", ".xcz") {
				paths = append(paths, candidate.Path)
			}
		}
		now := time.Now()
		for _, path := range paths {
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			record, ok := store.current(path, info)
			if ok && recheck >= 0 && (recheck == 0 || now.Sub(record.Checked) < recheck) {
				continue
			}
			files = append(files, pending{path, info, record.Checked})
			total += info.Size()
		}
		sort.SliceStable(files, func(i, j int) bool { return files[i].checked.Before(files[j].checked) })

		workers := min(verifyWorkers(settings.ReadSettings(web.dataFolder).VerifySpeed, runtime.NumCPU()), max(len(files), 1))
		var (
			mutex      sync.Mutex
			completed  int64
			partial    = make([]int64, workers)
			started    int
			checked    int
			damaged    int
			lastReport time.Time
			lastSave   = time.Now()
			failure    *TaskNote
		)
		report := func(name string, force bool) {
			// called with the mutex held
			if !force && time.Since(lastReport) < 500*time.Millisecond {
				return
			}
			lastReport = time.Now()
			done := completed
			for _, value := range partial {
				done += value
			}
			web.taskLog().Progress(taskId, int(done>>20), int(total>>20), fmt.Sprintf("%s (%d/%d)", name, started, len(files)))
		}
		// a scheduled verification stops when the background hours end and goes on the next time
		stopped := func() bool {
			return ctx.Err() != nil || (trigger == TRIGGER_SCHEDULE && !web.backgroundAllowed())
		}

		next := 0
		var wait sync.WaitGroup
		for worker := 0; worker < workers; worker++ {
			wait.Add(1)
			go func(worker int) {
				defer wait.Done()
				for {
					mutex.Lock()
					if next >= len(files) || stopped() {
						mutex.Unlock()
						return
					}
					file := files[next]
					next++
					started++
					name := filepath.Base(file.path)
					report(name, true)
					mutex.Unlock()

					err := verifyLibraryFile(ctx, file.path, func(fileDone int64, fileTotal int64) {
						mutex.Lock()
						partial[worker] = fileDone
						report(name, false)
						mutex.Unlock()
					})

					mutex.Lock()
					partial[worker] = 0
					completed += file.info.Size()
					if errors.Is(err, context.Canceled) {
						mutex.Unlock()
						return
					}
					record := verifyRecord{Size: file.info.Size(), ModTime: file.info.ModTime().UnixNano(), OK: err == nil, Checked: time.Now()}
					if err != nil {
						damaged++
						record.Reason = "damaged file: " + err.Error()
						web.taskLog().Warn(taskId, NOTE_VERIFY_DAMAGED, name+": "+err.Error())
					}
					store.set(file.path, record)
					checked++
					web.taskLog().SetVerifyResult(taskId, checked, damaged)
					saveNow := time.Since(lastSave) >= verifySaveInterval
					if saveNow {
						lastSave = time.Now()
					}
					mutex.Unlock()
					if saveNow {
						if err := store.save(); err != nil {
							web.sugarLogger.Warnf("Failed to save the verification results: %v", err)
						}
					}
				}
			}(worker)
		}
		wait.Wait()

		finished := checked == len(files)
		switch {
		case ctx.Err() != nil:
			failure = &TaskNote{Text: NOTE_COMPRESS_CANCELED}
		case !finished && trigger == TRIGGER_SCHEDULE:
			// the next scheduled run goes on with the files not checked yet
			web.taskLog().Warn(taskId, NOTE_PAUSED_FOR_HOURS, "")
		}
		if finished {
			store.mutex.Lock()
			store.LastRun = time.Now()
			store.mutex.Unlock()
		}
		if err := store.save(); err != nil {
			web.sugarLogger.Warnf("Failed to save the verification results: %v", err)
		}
		// Issues lists the damaged files
		web.invalidateDerived()
		web.taskLog().Finish(taskId, failure)
	}()
	return true
}

// verifyDue reports whether a scheduled verification should run now.
func verifyDue(s *settings.AppSettings, lastRun time.Time, now time.Time) bool {
	if s.VerifyIntervalDays <= 0 {
		return false
	}
	return lastRun.IsZero() || !now.Before(lastRun.Add(time.Duration(s.VerifyIntervalDays)*24*time.Hour))
}

func (web *Web) HandleVerify() {
	web.router.HandleFunc("/verify/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		var paths []string
		if r.FormValue("scope") == "space" {
			// only the files the Space page needs checked before deleting
			paths = web.spaceVerifyPaths()
		}
		recheck := verifyRecheckChanged
		if r.FormValue("all") == "true" {
			recheck = verifyRecheckAll
		}
		if !web.startVerificationOf(paths, recheck, TRIGGER_MANUAL) {
			writeGlobalError(w, http.StatusConflict, lang, "A compression is already running.")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
	}).Methods("POST")
}

func (s *verifyStore) lastRun() time.Time {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.LastRun
}
