package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
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
	NOTE_VERIFY_DAMAGED  = "The file is damaged: its data does not match its hashes."
	NOTE_VERIFY_FAILED   = "The files could not be checked."
	NOTE_VERIFY_UNSIGNED = "An NCA is not signed by Nintendo: a tool rewrote it."
	// the warning of a damaged file in older versions, still in the history of the tasks
	NOTE_VERIFY_DAMAGED_OLD = "The file is damaged or was modified."
	// a file that could not be read (gone, in use, a disk or a share that failed): not damaged
	NOTE_VERIFY_UNREADABLE = "The file could not be read, it was left unchecked."
	// a check of the signatures only that could not read the headers of a file with the keys
	NOTE_VERIFY_NO_SIGNATURES = "The signatures could not be read, it was left unchecked."
)

// the levels of a check: the signature of Nintendo on the header of every NCA, read in
// seconds; the hashes of all their data, read whole; or both, what a check was before it had
// levels
const (
	VERIFY_LEVEL_SIGNATURE = "signature"
	VERIFY_LEVEL_HASH      = "hash"
	VERIFY_LEVEL_BOTH      = "both"
)

// what a check of a file ended in, besides sound: its data does not match its hashes (damaged),
// or an NCA is not signed by Nintendo while nothing is damaged (a tool rewrote it, and it usually
// installs and plays: no failure)
const (
	VERIFY_FAILED   = "failed"
	VERIFY_MODIFIED = "modified"
)

// verifyLevel gives the level asked, false for one not known: empty is both, as a check was
// before it had levels, full is hash, as NXPress named it before 1.4.0, and signatures is the
// scope the pages of older versions sent for a check of the signatures only.
func verifyLevel(value string) (string, bool) {
	switch value {
	case "", VERIFY_LEVEL_BOTH:
		return VERIFY_LEVEL_BOTH, true
	case VERIFY_LEVEL_HASH, "full":
		return VERIFY_LEVEL_HASH, true
	case VERIFY_LEVEL_SIGNATURE, "signatures":
		return VERIFY_LEVEL_SIGNATURE, true
	}
	return "", false
}

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
// big games found intact by an older version are checked again. Version 4 tells a file that
// could not be read (in use, gone, a disk or a share that failed) from a damaged one: files
// found damaged by an older version are checked again.
const verifyChecker = 4

// verifyCheckerIntact is the oldest version whose rewritten NCA files found intact still stand.
const verifyCheckerIntact = 3

// sound tells a file checked whole and found sound: only such a copy lets another one be
// deleted.
func (r verifyRecord) sound() bool {
	return r.OK && !r.Partial
}

// stale tells a result an older version of the check may have got wrong: a damage, or a
// rewritten NCA found intact without the hashes of its sections past 4 GB. Other sound files
// matched the SHA-256 of their content IDs, which no version got wrong.
func (r verifyRecord) stale() bool {
	if !r.OK {
		return r.Checker < verifyChecker
	}
	return r.Modified && r.Checker < verifyCheckerIntact
}

// signatureRecord is what a check of the signatures found in a file, valid while its size and
// modification time do not change: the NCA files not signed by Nintendo, or why its headers
// could not be read with the keys (they are not read again while the file does not change).
type signatureRecord struct {
	Size     int64     `json:"size"`
	ModTime  int64     `json:"mod_time"`
	Unsigned []string  `json:"unsigned,omitempty"`
	Error    string    `json:"error,omitempty"`
	Checked  time.Time `json:"checked"`
}

// verifyStore keeps the results in verify.json in the data folder.
type verifyStore struct {
	mutex   sync.Mutex
	path    string
	records map[string]verifyRecord
	// the signatures, checked with the whole files or on their own
	signatures map[string]signatureRecord
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
	LastRun    time.Time                  `json:"last_run"`
	Records    map[string]verifyRecord    `json:"records"`
	Signatures map[string]signatureRecord `json:"signatures,omitempty"`
}

func loadVerifyStore(dataFolder string) *verifyStore {
	store := &verifyStore{path: filepath.Join(dataFolder, VERIFY_FILENAME), records: map[string]verifyRecord{}, signatures: map[string]signatureRecord{}}
	if data, ok := readStore(store.path); ok {
		saved := verifyFile{}
		if parseStore(store.path, data, &saved) == nil && saved.Records != nil {
			store.records = saved.Records
			store.LastRun = saved.LastRun
			if saved.Signatures != nil {
				store.signatures = saved.Signatures
			}
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
	s.signatures = loaded.signatures
	s.LastRun = loaded.LastRun
}

func (s *verifyStore) save() error {
	// one save at a time: an older one never ends after a newer one
	s.saving.Lock()
	defer s.saving.Unlock()
	s.mutex.Lock()
	// without indentation: tens of thousands of records, read only by the app
	data, err := json.Marshal(verifyFile{LastRun: s.LastRun, Records: s.records, Signatures: s.signatures})
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

// the reasons a file is checked; signature: its data was checked before, only its signatures
// are checked
const (
	VERIFY_WHY_NEW       = "new"
	VERIFY_WHY_CHANGED   = "changed"
	VERIFY_WHY_IMPROVED  = "improved"
	VERIFY_WHY_AGAIN     = "again"
	VERIFY_WHY_SIGNATURE = "signature"
)

func (s *verifyStore) set(path string, record verifyRecord) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.records[path] = record
	s.version.Add(1)
}

// setSignatures keeps what a check of the signatures of a file found.
func (s *verifyStore) setSignatures(path string, info os.FileInfo, unsigned []string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.signatures[path] = signatureRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), Unsigned: unsigned, Checked: time.Now()}
	s.version.Add(1)
}

// signaturesFailed keeps that the headers of a file could not be read with the keys, so they are
// not read again while the file does not change.
func (s *verifyStore) signaturesFailed(path string, info os.FileInfo, reason string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.signatures[path] = signatureRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), Error: reason, Checked: time.Now()}
	s.version.Add(1)
}

// signatureResult returns what the check of the signatures of a file found, if it is still
// valid for the file as it is now.
func (s *verifyStore) signatureResult(path string, info os.FileInfo) (signatureRecord, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	record, ok := s.signatures[path]
	if !ok || record.Size != info.Size() || record.ModTime != info.ModTime().UnixNano() {
		return signatureRecord{}, false
	}
	return record, true
}

// whySignatures tells why the signatures of a file without a valid record are checked: they
// were never checked, or the file changed since.
func (s *verifyStore) whySignatures(path string, info os.FileInfo) string {
	if _, ok := s.signatureResult(path, info); ok {
		return VERIFY_WHY_AGAIN
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.signatures[path]; ok {
		return VERIFY_WHY_CHANGED
	}
	return VERIFY_WHY_NEW
}

// unsigned lists the files with NCA files not signed by Nintendo, while they did not change.
func (s *verifyStore) unsigned() map[string][]string {
	s.mutex.Lock()
	records := make(map[string]signatureRecord, len(s.signatures))
	for path, record := range s.signatures {
		if len(record.Unsigned) > 0 {
			records[path] = record
		}
	}
	s.mutex.Unlock()
	result := map[string][]string{}
	for path, record := range records {
		if info, err := os.Stat(path); err == nil && info.Size() == record.Size && info.ModTime().UnixNano() == record.ModTime {
			result[path] = record.Unsigned
		}
	}
	return result
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

// stillDamaged tells whether a file is, as it is now, the one the current check found damaged
// by its contents: what deleting it as of no use relies on.
func (s *verifyStore) stillDamaged(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	record, ok := s.records[path]
	return ok && !record.OK && !record.stale() && record.Size == info.Size() && record.ModTime == info.ModTime().UnixNano()
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
var verifyLibraryFile = func(ctx context.Context, path string, progress func(int64, int64)) (switchfs.FileCheck, error) {
	return switchfs.VerifyFile(ctx, path, progress)
}

// checkFileSignatures checks the signature of Nintendo on the header of every NCA of a file.
var checkFileSignatures = switchfs.CheckSignatures

// missingKey tells a failure for a key prod.keys does not have: the file is not to blame, and
// it is checked again once the keys are there.
func missingKey(err error) bool {
	var missing *switchfs.MissingKeyError
	return errors.As(err, &missing)
}

// notContentError is a failure that tells nothing of what a file holds: the file could not be
// read (gone, in use by another program, a disk or a network share that failed, changed while
// it was read), or what failed was the copy written from it. Only what a file holds tells it
// damaged.
type notContentError struct{ err error }

func (e *notContentError) Error() string { return e.err.Error() }
func (e *notContentError) Unwrap() error { return e.err }

// notContent tells a failure that is not of what the file holds (see notContentError).
func notContent(err error) bool {
	var wrapped *notContentError
	return errors.As(err, &wrapped)
}

// readFailure gives the error of reading the file at path, as it was before (its size and
// time), as a notContentError when the file could not be read: else as it is.
func readFailure(path string, before os.FileInfo, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || notContent(err) || !unreadable(path, before, err) {
		return err
	}
	return &notContentError{err}
}

// unreadable tells a check that failed on reading the file, not on what it holds: the system
// refused or failed a read (the file is gone or in use, the disk or the share failed), or the
// file is no longer there as it was (a share that dropped, a file still being copied).
func unreadable(path string, before os.FileInfo, err error) bool {
	var pathError *fs.PathError
	if errors.As(err, &pathError) {
		return true
	}
	// the error of a read can reach the check in other words: the file must still be there,
	// the same, and open
	info, statErr := os.Stat(path)
	if statErr != nil || before == nil || info.Size() != before.Size() || !info.ModTime().Equal(before.ModTime()) {
		return true
	}
	file, openErr := os.Open(path)
	if openErr != nil {
		return true
	}
	file.Close()
	return false
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

// startVerificationOf checks the given files at the level both (see startVerificationAt).
func (web *Web) startVerificationOf(paths []string, recheck time.Duration, trigger string) bool {
	return web.startVerificationAt(VERIFY_LEVEL_BOTH, paths, recheck, trigger)
}

// startVerificationAt checks the given files, or every file of the library when paths is nil, at
// a level: the signatures of Nintendo from the headers, the hashes of all the data, or both. It
// checks all of them (verifyRecheckAll), the new and changed ones (verifyRecheckChanged), or also
// the ones last checked longer ago than recheck. The files never checked go first, then the ones
// checked the longest ago. A file whose data does not match its hashes ends failed; one with an
// NCA not signed by Nintendo and nothing damaged ends modified, which is no failure. With both,
// the hashes are checked even when the signatures fail.
func (web *Web) startVerificationAt(level string, paths []string, recheck time.Duration, trigger string) bool {
	level, _ = verifyLevel(level)
	hashes, signatures := level != VERIFY_LEVEL_SIGNATURE, level != VERIFY_LEVEL_HASH
	web.compressor.mutex.Lock()
	if web.compressor.cancel != nil || web.organizing.Load() || web.measuring.Load() {
		web.compressor.mutex.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	web.compressor.cancel, web.compressor.kind, web.compressor.taskId = cancel, TASK_VERIFY, 0
	web.compressor.mutex.Unlock()

	store := web.verifications()
	taskId := web.taskLog().Start(TASK_VERIFY, trigger)
	web.taskLog().SetVerifyLevel(taskId, level, false)
	go func() {
		defer func() {
			web.releaseFiles()
			cancel()
			// the files are free: the next task of the queue
			go web.runQueue()
		}()

		type pending struct {
			path    string
			info    os.FileInfo
			checked time.Time
			// what the file needs checked: its hashes, its signatures
			hash, signature bool
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
		// a result is due when there is none for the file as it is now, or it is older than asked
		due := func(ok bool, checked time.Time) bool {
			return !ok || recheck < 0 || (recheck > 0 && now.Sub(checked) >= recheck)
		}
		for i, path := range paths {
			info := infos[i]
			if info == nil || info.IsDir() {
				continue
			}
			file := pending{path: path, info: info}
			// the task says why its files are checked
			why := VERIFY_WHY_AGAIN
			if hashes {
				record, ok := store.current(path, info)
				if file.hash = due(ok, record.Checked); file.hash {
					file.checked = record.Checked
					if !ok {
						why = store.why(path, info)
					}
				}
			}
			if signatures {
				signed, ok := store.signatureResult(path, info)
				if file.signature = due(ok, signed.Checked); file.signature && !file.hash {
					file.checked = signed.Checked
					switch {
					case hashes:
						why = VERIFY_WHY_SIGNATURE
					case !ok:
						why = store.whySignatures(path, info)
					}
				}
			}
			if !file.hash && !file.signature {
				continue
			}
			files = append(files, file)
			if file.hash {
				total += info.Size()
			}
			switch why {
			case VERIFY_WHY_NEW:
				breakdown.New++
			case VERIFY_WHY_CHANGED:
				breakdown.Changed++
			case VERIFY_WHY_IMPROVED:
				breakdown.Improved++
			case VERIFY_WHY_SIGNATURE:
				breakdown.Signatures++
			default:
				breakdown.Again++
			}
		}
		web.taskLog().SetBreakdown(taskId, breakdown)
		// a check that reads no file whole counts its files, not megabytes
		byFiles := total == 0
		web.taskLog().SetVerifyLevel(taskId, level, byFiles)
		// the files never checked or checked longest ago first, and among them by name: the count
		// moves from the start, instead of waiting for the biggest files
		sort.SliceStable(files, func(i, j int) bool {
			if !files[i].checked.Equal(files[j].checked) {
				return files[i].checked.Before(files[j].checked)
			}
			return strings.ToLower(filepath.Base(files[i].path)) < strings.ToLower(filepath.Base(files[j].path))
		})

		names := make([]string, len(files))
		for i, file := range files {
			names[i] = filepath.Base(file.path)
		}
		web.taskLog().SetPlan(taskId, names)

		workers := min(verifyWorkers(settings.ReadSettings(web.dataFolder).VerifySpeed, runtime.NumCPU()), max(len(files), 1))
		var (
			mutex      sync.Mutex
			completed  int64
			partial    = make([]int64, workers)
			laneNames  = make([]string, workers)
			laneSizes  = make([]int64, workers)
			checked    int
			damaged    int
			modified   int
			unread     int // files that could not be read, left unchecked
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
			// the files checked: one being read is counted once it is done
			if byFiles {
				web.taskLog().ProgressFiles(taskId, checked+unread, len(files), name, checked+unread, len(files), nil)
				return
			}
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
			web.taskLog().ProgressFiles(taskId, int(done>>20), int(total>>20), name, checked+unread, len(files), lanes)
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

					// the signatures first: the headers only, read in a moment
					var signed *switchfs.SignatureCheck
					var signErr error
					if file.signature {
						if result, err := checkFileSignatures(file.path); err == nil {
							signed = &result
						} else {
							signErr = err
						}
					}
					// the hashes even when the signatures fail: only damage makes a file fail
					var check switchfs.FileCheck
					var err error
					if file.hash {
						check, err = verifyLibraryFile(ctx, file.path, func(fileDone int64, fileTotal int64) {
							mutex.Lock()
							partial[worker] = fileDone
							report(name, false)
							mutex.Unlock()
						})
					}

					mutex.Lock()
					partial[worker] = 0
					laneNames[worker] = ""
					if file.hash {
						completed += file.info.Size()
					}
					if errors.Is(err, context.Canceled) {
						mutex.Unlock()
						return
					}
					// a file that could not be read stays unchecked: in use, gone, or on a disk or a
					// share that failed, it is not damaged
					readErr := err
					if !file.hash {
						readErr = signErr
					}
					if readErr != nil && unreadable(file.path, file.info, readErr) {
						unread++
						web.taskLog().Warn(taskId, NOTE_VERIFY_UNREADABLE, name+": "+readErr.Error())
						web.taskLog().SkipFileTile(taskId, name, NOTE_VERIFY_UNREADABLE)
						report(name, true)
						mutex.Unlock()
						continue
					}
					switch {
					case signed != nil:
						// Issues lists the files not signed
						store.setSignatures(file.path, file.info, signed.Unsigned)
					case signErr != nil && !missingKey(signErr) && !unreadable(file.path, file.info, signErr):
						// headers the keys cannot read are not read again while the file stays the same
						store.signaturesFailed(file.path, file.info, signErr.Error())
					}
					// a check of the signatures alone that could not read them tells nothing of the file
					if !hashes && signed == nil {
						unread++
						web.taskLog().SkipFileTile(taskId, name, NOTE_VERIFY_NO_SIGNATURES)
						report(name, true)
						mutex.Unlock()
						continue
					}
					if file.hash {
						record := verifyRecord{Size: file.info.Size(), ModTime: file.info.ModTime().UnixNano(), OK: err == nil, Checked: time.Now(),
							Modified: err == nil && len(check.Modified) > 0, Partial: err == nil && check.Partial, Checker: verifyChecker}
						if err != nil {
							record.Reason = "damaged file: " + err.Error()
						}
						store.set(file.path, record)
					}
					// how the file ended, at the level of the check; what was not checked again stands
					// as it was found
					verdict, reason := "", ""
					if hashes {
						if file.hash && err != nil {
							verdict, reason = VERIFY_FAILED, err.Error()
						} else if record, ok := store.current(file.path, file.info); !file.hash && ok && !record.OK {
							verdict, reason = VERIFY_FAILED, strings.TrimPrefix(record.Reason, "damaged file: ")
						}
					}
					if signatures && verdict == "" {
						if signed == nil && !file.signature {
							if saved, ok := store.signatureResult(file.path, file.info); ok {
								signed = &switchfs.SignatureCheck{Unsigned: saved.Unsigned}
							}
						}
						if signed != nil && len(signed.Unsigned) > 0 {
							verdict = VERIFY_MODIFIED
						}
					}
					switch verdict {
					case VERIFY_FAILED:
						damaged++
						web.taskLog().Warn(taskId, NOTE_VERIFY_DAMAGED, name+": "+reason)
						web.taskLog().AddFileTile(taskId, name, true, NOTE_VERIFY_DAMAGED)
					case VERIFY_MODIFIED:
						// told on its cover and in Issues, not as a warning: it is no failure
						modified++
						web.taskLog().ModifiedFileTile(taskId, name, NOTE_VERIFY_UNSIGNED)
					default:
						web.taskLog().AddFileTile(taskId, name, false, "")
					}
					checked++
					web.taskLog().SetVerifyResult(taskId, checked, damaged, modified)
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

		// a file that could not be read was gone through too: the next run of the schedule does
		// not start again at once for it
		finished := checked+unread == len(files)
		switch {
		case ctx.Err() != nil:
			failure = &TaskNote{Text: NOTE_COMPRESS_CANCELED}
		case !finished && trigger == TRIGGER_SCHEDULE:
			// the next scheduled run goes on with the files not checked yet
			web.taskLog().Warn(taskId, NOTE_PAUSED_FOR_HOURS, "")
		}
		// the date of the last check, and the schedule, are of the checks that read the data
		if finished && hashes {
			store.mutex.Lock()
			store.LastRun = time.Now()
			store.mutex.Unlock()
		}
		if err := store.save(); err != nil {
			web.sugarLogger.Warnf("Failed to save the verification results: %v", err)
		}
		// Issues lists the damaged files and the ones not signed
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
		level, ok := verifyLevel(r.FormValue("level"))
		if !ok {
			writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), "Invalid request")
			return
		}
		var paths []string
		switch r.FormValue("scope") {
		case "signatures":
			// the button of the pages of older versions: the signatures only
			level = VERIFY_LEVEL_SIGNATURE
		case "space":
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
		if recheck == verifyRecheckChanged && !web.filesBusy() && len(web.queue().list()) == 0 && web.nothingToVerify(paths, level) {
			writeJSON(w, http.StatusOK, map[string]any{"nothing": true})
			return
		}
		web.startOrQueue(w, QueuedTask{Kind: TASK_VERIFY, Paths: paths, Recheck: recheck, Level: level})
	}).Methods("POST")
}

// nothingToVerify tells whether a check of the new and changed files of paths (of the library
// when nil) at a level would find none: each of them was checked as it is now.
func (web *Web) nothingToVerify(paths []string, level string) bool {
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
		if _, ok := store.current(paths[i], info); !ok && level != VERIFY_LEVEL_SIGNATURE {
			return false
		}
		if _, ok := store.signatureResult(paths[i], info); !ok && level != VERIFY_LEVEL_HASH {
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
