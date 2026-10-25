package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/process"
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
	// NCA files rewritten by a tool whose data matched all its hashes
	Modified bool `json:"modified,omitempty"`
	// some sections of those files could not be checked on their own (the romfs of an
	// update): not damaged, but not proven sound enough to delete another copy for it
	Partial bool `json:"partial,omitempty"`
	// the version of the check that gave the result
	Checker int `json:"checker,omitempty"`
}

// verifyChecker is the version of the check. Version 2 reads every NCZ that nsz writes
// (updates with many sections, block compression) and tells NCA files rewritten by a tool
// apart from damaged ones: files found damaged by an older version are checked again.
// Version 3 reads the hashes of the sections past 4 GB of an NCA: rewritten NCA files of
// big games found intact by an older version are checked again.
const verifyChecker = 3

// sound tells a file checked whole and found sound: only such a copy lets another one be
// deleted.
func (r verifyRecord) sound() bool {
	return r.OK && !r.Partial
}

// stale tells a result an older version of the check may have got wrong: a damage, or a
// rewritten NCA found intact without the hashes of its sections past 4 GB. Other sound files
// matched the SHA-256 of their content IDs, which no version got wrong.
func (r verifyRecord) stale() bool {
	return (!r.OK || r.Modified) && r.Checker < verifyChecker
}

// verifyStore keeps the results in verify.json in the data folder.
type verifyStore struct {
	mutex   sync.Mutex
	path    string
	records map[string]verifyRecord
	// when the last verification finished
	LastRun time.Time
	// changed with every result, for what is computed from them, and the one last saved
	version atomic.Uint64
	saved   atomic.Uint64
	// one save at a time
	saving sync.Mutex
}

// SavedVersion is the version of the results last saved: while a check runs, every two
// minutes instead of with every file.
func (s *verifyStore) SavedVersion() uint64 {
	return s.saved.Load()
}

// Version changes with every result recorded.
func (s *verifyStore) Version() uint64 {
	return s.version.Load()
}

type verifyFile struct {
	LastRun time.Time               `json:"last_run"`
	Records map[string]verifyRecord `json:"records"`
}

func loadVerifyStore(dataFolder string) *verifyStore {
	store := &verifyStore{path: filepath.Join(dataFolder, VERIFY_FILENAME), records: map[string]verifyRecord{}}
	if data, err := os.ReadFile(store.path); err == nil {
		saved := verifyFile{}
		if parseStore(store.path, data, &saved) == nil && saved.Records != nil {
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
	// one save at a time: an older one never ends after a newer one
	s.saving.Lock()
	defer s.saving.Unlock()
	s.mutex.Lock()
	// without indentation: tens of thousands of records, read only by the app
	data, err := json.Marshal(verifyFile{LastRun: s.LastRun, Records: s.records})
	version := s.version.Load()
	s.mutex.Unlock()
	defer s.saved.Store(version)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data)
}

// current returns the record of a file if it is still valid for the file as it is now.
func (s *verifyStore) current(path string, info os.FileInfo) (verifyRecord, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	record, ok := s.records[path]
	if !ok || record.Size != info.Size() || record.ModTime != info.ModTime().UnixNano() || record.stale() {
		return verifyRecord{}, false
	}
	return record, true
}

// follow keeps the results of the files the organizer moved, under their new path, and
// forgets the ones it deleted: a file moved keeps its content, and would otherwise be
// checked again as a new file.
func (s *verifyStore) follow(operations []process.Operation) {
	changed := false
	for _, op := range operations {
		if op.Error != "" {
			continue
		}
		switch op.Kind {
		case process.OP_MOVE:
			changed = s.carry(op.From, op.To, true) || changed
		case process.OP_DELETE:
			s.mutex.Lock()
			if _, ok := s.records[op.From]; ok {
				delete(s.records, op.From)
				s.version.Add(1)
				changed = true
			}
			s.mutex.Unlock()
		}
	}
	if changed {
		s.save()
	}
}

// carry gives the file at to the result of the file at from, when the result is still
// valid for from as it was and to has the same size: the same content, written by the app
// (moved, or compressed and checked byte by byte against it). With moved the result of
// from is forgotten.
func (s *verifyStore) carry(from string, to string, moved bool) bool {
	info, err := os.Stat(to)
	if err != nil || info.IsDir() {
		return false
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	record, ok := s.records[from]
	if !ok {
		return false
	}
	if moved {
		delete(s.records, from)
		// a move keeps the size and the time of the file: a file changed after it was checked,
		// even to the same size, or a result too old, does not count as checked
		if record.Size != info.Size() || record.ModTime != info.ModTime().UnixNano() || record.stale() {
			s.version.Add(1)
			return true
		}
	}
	record.Size, record.ModTime = info.Size(), info.ModTime().UnixNano()
	s.records[to] = record
	s.version.Add(1)
	return true
}

// inherit gives a file the app made from another one and checked against it (an NSZ of an
// NSP, an NSP of an XCI) the result of that one, while it is still valid.
func (s *verifyStore) inherit(original string, made string) {
	info, err := os.Stat(original)
	if err != nil {
		return
	}
	record, ok := s.current(original, info)
	if !ok {
		return
	}
	madeInfo, err := os.Stat(made)
	if err != nil {
		return
	}
	record.Size, record.ModTime = madeInfo.Size(), madeInfo.ModTime().UnixNano()
	s.set(made, record)
	s.save()
}

// remember keeps the result of a check another task made of a file (a compression reads
// its original whole, a decompression or conversion checks what it wrote), so it is not
// checked again by the next file check. A failed check is left to the file check, which
// tells modified files from damaged ones.
func (s *verifyStore) remember(path string, check switchfs.FileCheck) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}
	s.set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checked: time.Now(),
		Modified: len(check.Modified) > 0, Partial: check.Partial, Checker: verifyChecker})
	s.save()
}

// markDamaged keeps a file another task found damaged while reading it whole (a compression
// hashes every NCA of the original with the check of the files), so it is listed in Issues and
// not offered again to compress while it does not change.
func (s *verifyStore) markDamaged(path string, reason string) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}
	s.set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false, Reason: reason, Checked: time.Now(), Checker: verifyChecker})
	s.save()
}

// adoptMoved gives the files of the library without a result the result of a file that is
// gone, when both have exactly the same size and modification time (to the nanosecond): a
// file moved on the same disk keeps them, as the organizer of older versions moved files
// without their results. A size and time shared by several files are left alone. It gives
// how many files got their result back.
func (s *verifyStore) adoptMoved(paths []string, infos []os.FileInfo) int {
	type key struct{ size, time int64 }
	current := make(map[string]bool, len(paths))
	for _, path := range paths {
		current[path] = true
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	// the results of files gone, by size and time; nil when several share them
	orphans := map[key]*string{}
	for path, record := range s.records {
		if current[path] {
			continue
		}
		k := key{record.Size, record.ModTime}
		if _, seen := orphans[k]; seen {
			orphans[k] = nil
			continue
		}
		owner := path
		orphans[k] = &owner
	}
	if len(orphans) == 0 {
		return 0
	}
	// the files without a result, by size and time; two of the same are left alone too
	homeless := map[key]int{}
	for i, path := range paths {
		if infos[i] == nil {
			continue
		}
		if _, ok := s.records[path]; !ok {
			homeless[key{infos[i].Size(), infos[i].ModTime().UnixNano()}]++
		}
	}
	adopted := 0
	for i, path := range paths {
		info := infos[i]
		if info == nil || info.IsDir() {
			continue
		}
		if _, ok := s.records[path]; ok {
			continue
		}
		k := key{info.Size(), info.ModTime().UnixNano()}
		owner := orphans[k]
		if owner == nil || homeless[k] != 1 {
			continue
		}
		s.records[path] = s.records[*owner]
		delete(s.records, *owner)
		delete(orphans, k)
		adopted++
	}
	if adopted > 0 {
		s.version.Add(1)
	}
	return adopted
}

// why tells why a file without a valid record is checked: it was never checked, it changed
// since, or an older version of the check may have got it wrong.
func (s *verifyStore) why(path string, info os.FileInfo) string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	record, ok := s.records[path]
	switch {
	case !ok:
		return VERIFY_WHY_NEW
	case record.Size != info.Size() || record.ModTime != info.ModTime().UnixNano():
		return VERIFY_WHY_CHANGED
	case record.stale():
		return VERIFY_WHY_IMPROVED
	}
	return VERIFY_WHY_AGAIN
}

// the reasons a file is checked
const (
	VERIFY_WHY_NEW      = "new"
	VERIFY_WHY_CHANGED  = "changed"
	VERIFY_WHY_IMPROVED = "improved"
	VERIFY_WHY_AGAIN    = "again"
)

func (s *verifyStore) set(path string, record verifyRecord) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.records[path] = record
	s.version.Add(1)
}

// foundDamaged tells whether a file, unchanged since, was found damaged, also by an older
// version of the check: a deletion must not rely on it until it is checked again.
func (s *verifyStore) foundDamaged(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	record, ok := s.records[path]
	return ok && !record.OK && record.Size == info.Size() && record.ModTime == info.ModTime().UnixNano()
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
		if record.OK || record.stale() {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.Size() == record.Size && info.ModTime().UnixNano() == record.ModTime {
			result[path] = record.Reason
		}
	}
	return result
}

// checkedPartly tells whether the last check of a file, still the same, could not check all of
// it: a copy whose NCA files a tool rewrote, with sections that cannot be checked on their own.
func (web *Web) checkedPartly(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	record, ok := web.verifications().current(path, info)
	return ok && record.Partial
}

func (web *Web) verifications() *verifyStore {
	web.verifyOnce.Do(func() {
		if web.verify == nil {
			web.verify = loadVerifyStore(web.dataFolder)
		}
	})
	return web.verify
}

// NOTE_VERIFY_PREPARING is the message of a verification while it makes its list of files.
const NOTE_VERIFY_PREPARING = "Preparing the list of files to check..."

// statFiles looks at files with several workers at once, as a NAS answers each one slowly;
// a file that cannot be looked at is nil.
func statFiles(paths []string, workers int) []os.FileInfo {
	infos := make([]os.FileInfo, len(paths))
	jobs := make(chan int)
	var wait sync.WaitGroup
	for w := 0; w < workers; w++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for i := range jobs {
				if info, err := os.Stat(paths[i]); err == nil {
					infos[i] = info
				}
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wait.Wait()
	return infos
}

// verifyLibraryFile checks one file: every NCA against its content ID (NSZ and XCZ files
// decompressed), and an NCA that does not match it against the hashes of its data, to
// tell one rewritten by a tool from a damaged one.
func verifyLibraryFile(ctx context.Context, path string, progress func(int64, int64)) (switchfs.FileCheck, error) {
	return switchfs.VerifyFile(ctx, path, progress)
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
	if web.compressor.cancel != nil || web.organizing.Load() || web.measuring.Load() {
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
			// the files are free: the next task of the queue
			go web.runQueue()
		}()

		type pending struct {
			path    string
			info    os.FileInfo
			checked time.Time
		}
		files := []pending{}
		breakdown := VerifyBreakdown{}
		var total int64
		// making the list of a big library on a NAS takes a while: the task says so
		web.taskLog().Progress(taskId, 0, 0, NOTE_VERIFY_PREPARING)
		// the files of a whole library: results of files gone may belong to files moved
		whole := paths == nil
		if paths == nil {
			for _, candidate := range web.libraryFiles(".nsp", ".nsz", ".xci", ".xcz") {
				paths = append(paths, candidate.Path)
			}
		}
		// the library may still have the paths from before Organize moved the files
		paths = web.moves.resolveAll(paths)
		now := time.Now()
		infos := statFiles(paths, 16)
		if whole {
			if found := store.adoptMoved(paths, infos); found > 0 {
				web.sugarLogger.Infof("[File check] %d files moved by an older version keep their results", found)
			}
		}
		for i, path := range paths {
			info := infos[i]
			if info == nil || info.IsDir() {
				continue
			}
			record, ok := store.current(path, info)
			if ok && recheck >= 0 && (recheck == 0 || now.Sub(record.Checked) < recheck) {
				continue
			}
			files = append(files, pending{path, info, record.Checked})
			total += info.Size()
			// the task says why its files are checked
			why := VERIFY_WHY_AGAIN
			if !ok {
				why = store.why(path, info)
			}
			switch why {
			case VERIFY_WHY_NEW:
				breakdown.New++
			case VERIFY_WHY_CHANGED:
				breakdown.Changed++
			case VERIFY_WHY_IMPROVED:
				breakdown.Improved++
			default:
				breakdown.Again++
			}
		}
		web.taskLog().SetBreakdown(taskId, breakdown)
		// the files never checked or checked longest ago first, and among them by name: the count
		// moves from the start, instead of waiting for the biggest files
		sort.SliceStable(files, func(i, j int) bool {
			if !files[i].checked.Equal(files[j].checked) {
				return files[i].checked.Before(files[j].checked)
			}
			return strings.ToLower(filepath.Base(files[i].path)) < strings.ToLower(filepath.Base(files[j].path))
		})

		workers := min(verifyWorkers(settings.ReadSettings(web.dataFolder).VerifySpeed, runtime.NumCPU()), max(len(files), 1))
		var (
			mutex      sync.Mutex
			completed  int64
			partial    = make([]int64, workers)
			laneNames  = make([]string, workers)
			laneSizes  = make([]int64, workers)
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
			lanes := []TaskLane{}
			for i, laneName := range laneNames {
				if laneName != "" {
					lanes = append(lanes, TaskLane{Name: laneName, Done: partial[i], Total: laneSizes[i], Kind: laneKind(laneName)})
				}
			}
			// the files checked: one being read is counted once it is done
			web.taskLog().ProgressFiles(taskId, int(done>>20), int(total>>20), name, checked, len(files), lanes)
		}
		// a scheduled verification stops when the background hours end and goes on the next time
		stopped := func() bool {
			return ctx.Err() != nil || (trigger == TRIGGER_SCHEDULE && !web.backgroundAllowed())
		}

		web.taskLog().SetWorkers(taskId, workers, 0)
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
					name := filepath.Base(file.path)
					laneNames[worker], laneSizes[worker] = name, file.info.Size()
					report(name, true)
					mutex.Unlock()

					check, err := verifyLibraryFile(ctx, file.path, func(fileDone int64, fileTotal int64) {
						mutex.Lock()
						partial[worker] = fileDone
						report(name, false)
						mutex.Unlock()
					})

					mutex.Lock()
					partial[worker] = 0
					laneNames[worker] = ""
					completed += file.info.Size()
					if errors.Is(err, context.Canceled) {
						mutex.Unlock()
						return
					}
					record := verifyRecord{Size: file.info.Size(), ModTime: file.info.ModTime().UnixNano(), OK: err == nil, Checked: time.Now(),
						Modified: err == nil && len(check.Modified) > 0, Partial: err == nil && check.Partial, Checker: verifyChecker}
					if err != nil {
						damaged++
						record.Reason = "damaged file: " + err.Error()
						web.taskLog().Warn(taskId, NOTE_VERIFY_DAMAGED, name+": "+err.Error())
					}
					store.set(file.path, record)
					checked++
					web.taskLog().SetVerifyResult(taskId, checked, damaged)
					report(name, true)
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
		var paths []string
		if r.FormValue("scope") == "space" {
			// only the files the Space page needs checked before deleting
			paths = web.spaceVerifyPaths()
		}
		recheck := verifyRecheckChanged
		if r.FormValue("all") == "true" {
			recheck = verifyRecheckAll
		}
		// nothing new nor changed, and no task that could change files before the check: the
		// page says so, instead of a task that checks nothing (pressed again, as it seemed to do
		// nothing)
		if recheck == verifyRecheckChanged && !web.filesBusy() && len(web.queue().list()) == 0 && web.nothingToVerify(paths) {
			writeJSON(w, http.StatusOK, map[string]any{"nothing": true})
			return
		}
		web.startOrQueue(w, QueuedTask{Kind: TASK_VERIFY, Paths: paths, Recheck: recheck})
	}).Methods("POST")
}

// nothingToVerify tells whether a check of the new and changed files of paths (of the library
// when nil) would find none: each of them was checked as it is now.
func (web *Web) nothingToVerify(paths []string) bool {
	if paths == nil {
		for _, candidate := range web.libraryFiles(".nsp", ".nsz", ".xci", ".xcz") {
			paths = append(paths, candidate.Path)
		}
	}
	paths = web.moves.resolveAll(paths)
	store := web.verifications()
	for i, info := range statFiles(paths, 16) {
		if info == nil || info.IsDir() {
			continue
		}
		if _, ok := store.current(paths[i], info); !ok {
			return false
		}
	}
	return true
}

func (s *verifyStore) lastRun() time.Time {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.LastRun
}
