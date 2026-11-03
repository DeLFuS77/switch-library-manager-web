package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/awake"
	"github.com/dtrunk90/switch-library-manager-web/internal/diskfree"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// problems of a compression, translated by the interface
const (
	NOTE_COMPRESS_FAILED  = "The file could not be compressed, the original is kept."
	NOTE_COMPRESS_DAMAGED = "The file is damaged or was modified, it was not compressed."
	NOTE_COMPRESS_SPACE   = "Not enough free space to compress the file."
	NOTE_COMPRESS_EXISTS  = "A compressed file with the same name already exists."
	NOTE_COMPRESS_NOTHING = "Nothing in the file can be compressed."
	NOTE_COMPRESS_SPARSE  = "This game uses a recent layout (sparse storage) that no compressor can shrink yet: it stays as it is, and installs normally."
	// why nothing else of a file can be compressed, when the library tells it
	NOTE_COMPRESS_ALREADY    = "This file is compressed already: it is an NSZ or XCZ under the name of an NSP or XCI. Give it its own extension to use it."
	NOTE_COMPRESS_NO_DATA    = "Its NCA files hold no game data (only control, manual or meta files), so there is nothing to compress: it stays as it is."
	NOTE_COMPRESS_ENCRYPTION = "Its sections use an encryption no compressor reads: it stays as it is, and installs normally."
	NOTE_COMPRESS_DELETE     = "The original could not be deleted."
	NOTE_COMPRESS_NO_KEY     = "This NSP has no ticket, and title.keys does not have its title key: without it the game cannot be read to compress it. Add the title.keys dumped from your console in Settings, or use a copy of the game with its ticket."
	NOTE_DECOMPRESS_FAILED   = "The file could not be decompressed, the compressed file is kept."
)

var compressNoteTexts = []string{NOTE_SPLIT_FAILED, NOTE_SPLIT_EXISTS, NOTE_SPLIT_KEPT, NOTE_SPLIT_PART_UNSOUND, NOTE_PACK_FAILED, NOTE_PACK_EXISTS, NOTE_DECOMPRESS_FAILED, NOTE_COMPRESS_FAILED, NOTE_COMPRESS_DAMAGED, NOTE_COMPRESS_SPACE, NOTE_COMPRESS_EXISTS, NOTE_COMPRESS_NOTHING, NOTE_COMPRESS_SPARSE, NOTE_COMPRESS_ALREADY, NOTE_COMPRESS_NO_DATA, NOTE_COMPRESS_ENCRYPTION, tasklog.NOTE_COMPRESS_CANCELED, tasklog.NOTE_COMPRESS_CANCELED_OLD, NOTE_COMPRESS_DELETE, NOTE_COMPRESS_NO_KEY}

// CompressCandidate is an NSP of the library that can be compressed.
type CompressCandidate struct {
	Path string
	Name string
	// Base, Update or DLC
	Kind string
	Size int64
	// the share it will likely save, from the files compressed before (see ratioStore)
	Estimate    int
	HasEstimate bool
	// in the lists to split and to pack: the titles in the file, the files of the pack
	Titles int
	Files  int
	// in the list to split: the games of a game card with several games
	Games int
	// in the list to pack: the titles of each kind the pack holds (1G+1U+2D), and the pack made
	// before, for a game already packed
	Contents string
	Packed   string
	// in the list to pack: the version of the game a DLC needs when the newest update of the
	// library is older (0 else), and that newest update
	NeedsGame int
	Latest    int
}

// KindKey is the kind for the colors and the filter of the list: game, update or dlc.
func (c CompressCandidate) KindKey() string {
	switch c.Kind {
	case "Base":
		return "game"
	case "Update":
		return "update"
	}
	return "dlc"
}

// Format is the type of the file, e.g. NSP or XCI.
func (c CompressCandidate) Format() string {
	return strings.ToUpper(strings.TrimPrefix(filepath.Ext(c.Path), "."))
}

// CompressGroup is a game of the list of Compress with its files, the biggest first.
type CompressGroup struct {
	Name string
	Size int64
	Rows []CompressRow
}

// CompressRow is a file of the list with its place in it, for the id of its box.
type CompressRow struct {
	CompressCandidate
	Index int
}

// Groups puts the files of the same game together; the games come in the order of their
// biggest file.
func (d CompressPageData) Groups() []CompressGroup {
	groups := []CompressGroup{}
	at := map[string]int{}
	for i, candidate := range d.Candidates {
		g, ok := at[candidate.Name]
		if !ok {
			g = len(groups)
			at[candidate.Name] = g
			groups = append(groups, CompressGroup{Name: candidate.Name})
		}
		groups[g].Size += candidate.Size
		groups[g].Rows = append(groups[g].Rows, CompressRow{CompressCandidate: candidate, Index: i})
	}
	return groups
}

// Weight is the size of a file as a share of the biggest one listed, for its bar.
func (d CompressPageData) Weight(size int64) int {
	biggest := int64(0)
	for _, candidate := range d.Candidates {
		biggest = max(biggest, candidate.Size)
	}
	if biggest == 0 {
		return 0
	}
	return int(max(2, size*100/biggest))
}

// NszListData is a page of the NSZ files that can be decompressed.
type NszListData struct {
	Files   []CompressCandidate
	Matches int
	Total   int
	// the start of the ids of the boxes, one per list of the page
	Prefix string
	// the files not shown, chosen together with one box, in the lists that allow it
	Rest     int
	RestSize int64
	// in the list to pack: the games packed before, shown without a box
	Packed []CompressCandidate
}

type CompressPageData struct {
	GlobalPageData
	Candidates []CompressCandidate
	// files not listed one by one, compressed with "the rest"
	RestCount int
	RestSize  int64
	// NSZ files that can be decompressed; the list is loaded when the section is opened
	CompressedCount int
	// XCI and XCZ files that can be converted to NSP and NSZ
	CardCount int
	// files that hold several titles, and games a complete pack can be made of
	SplitCount int
	PackCount  int
	// NSP and XCI files that already have a compressed copy (listed on the Space page)
	AlreadyCompressed int
	// files tried before that nothing could shrink, not listed
	Uncompressible []UncompressibleEntry
	// NSP and XCI files found damaged, not listed (they are in Issues)
	Damaged int
	// what each kind saved before, and what all the files listed would likely save
	Ratios    []RatioSummary
	Estimated int64
	TotalSize int64
	// compressed files that can be decompressed
	Running bool
	// the level chosen at first: the one of Settings, which Measure this server measures
	Level string
}

// settingsLevel is the compression level chosen in Settings, balanced when none is.
func settingsLevel(appSettings *settings.AppSettings) string {
	switch appSettings.AutoCompressLevel {
	case switchfs.LevelFast, switchfs.LevelMax:
		return appSettings.AutoCompressLevel
	}
	return switchfs.LevelBalanced
}

// compressor runs one compression at a time and can cancel it.
type compressor struct {
	mutex  sync.Mutex
	cancel context.CancelFunc
	// the kind of the task working on the files, which tells whether it can be paused
	kind string
	// a pause holds the work where it is until resume is closed; taskId is the task paused
	paused bool
	resume chan struct{}
	taskId int64
	// keeps the computer awake while the task works, not while it is paused
	awake *awake.Hold
	// the bytes the files being written will still take, for the space checks
	reserved int64
	// the version of the check results when the task began, which the Space page follows
	// while it runs (see spaceKey)
	spaceVersion uint64
	// the compressed copies written since the last scan, which does not know them yet, with
	// when they were written, and how many were written, for the list of the copies
	written        map[string]time.Time
	writtenVersion int
	// saves what the task working on the files has left, now: the app is about to stop (see Stop)
	flush func()
}

// filesTaken tells whether a task may not start on the files: one works on them, files are
// being moved or deleted, the server is being measured, or the app is stopping. The caller
// holds the mutex of the compressor.
func (web *Web) filesTaken() bool {
	return web.compressor.cancel != nil || web.organizing.Load() || web.measuring.Load() || web.stopping.Load()
}

// rememberWritten keeps a compressed copy just written, until the scan that follows.
func (web *Web) rememberWritten(path string) {
	web.compressor.mutex.Lock()
	if web.compressor.written == nil {
		web.compressor.written = map[string]time.Time{}
	}
	web.compressor.written[filepath.Clean(path)] = time.Now()
	web.compressor.writtenVersion++
	web.compressor.mutex.Unlock()
}

// forgetWritten forgets the copies written before a scan started, which knows them now; the
// ones written while it ran may have been missed by it.
func (web *Web) forgetWritten(scanStarted time.Time) {
	web.compressor.mutex.Lock()
	for path, written := range web.compressor.written {
		if written.Before(scanStarted) {
			delete(web.compressor.written, path)
		}
	}
	web.compressor.mutex.Unlock()
}

func (web *Web) writtenCopy(path string) bool {
	web.compressor.mutex.Lock()
	defer web.compressor.mutex.Unlock()
	_, written := web.compressor.written[filepath.Clean(path)]
	return written
}

// compressCandidates lists the NSP and XCI files of the library, biggest first, but the old
// updates and DLC the cleanup deletes: compressing them would be work thrown away (a file
// that also holds the game or a newer content stays).
func (web *Web) compressCandidates() []CompressCandidate {
	return web.derived("compressCandidates", func() any {
		_, localDB := web.state.get()
		old := map[string]bool{}
		if localDB != nil {
			inUse := process.FilesInUse(localDB)
			for file, skipped := range localDB.Skipped {
				path := filepath.Join(file.BaseFolder, file.FileName)
				if skipped.ReasonCode == db.REASON_OLD_UPDATE && !inUse[path] {
					old[path] = true
				}
			}
		}
		// a file tried before that nothing could shrink is not offered again
		store := web.uncompressible()
		// nor a game without its title key: it cannot be read to compress it
		noKey := web.missingTitleKeys()
		// nor a file found damaged, while it does not change: it failed on every run
		damaged := web.verifications().damaged()
		candidates := []CompressCandidate{}
		for _, candidate := range web.libraryFiles(".nsp", ".xci") {
			if _, uncompressible := store.get(candidate.Path, candidate.Size); !old[candidate.Path] && !uncompressible && noKey[candidate.Path] == "" && damaged[candidate.Path] == "" {
				candidates = append(candidates, candidate)
			}
		}
		return candidates
	}).([]CompressCandidate)
}

// compressedCopies are the compress candidates that have a compressed copy next to them,
// among the files the last scan found and the copies written since: asking the disk for every
// file took seconds on NAS shares.
func (web *Web) compressedCopies() map[string]bool {
	web.compressor.mutex.Lock()
	written := web.compressor.writtenVersion
	web.compressor.mutex.Unlock()
	return web.derivedLatest("compressedCopies:", fmt.Sprintf("compressedCopies:%d", written), func() any {
		scanned := web.scannedPaths()
		copies := map[string]bool{}
		for _, candidate := range web.compressCandidates() {
			if target := filepath.Clean(switchfs.CompressedPath(candidate.Path)); scanned[target] || web.writtenCopy(target) {
				copies[candidate.Path] = true
			}
		}
		return copies
	}).(map[string]bool)
}

// scannedPaths are the paths of every file the last scan found: those of the library and
// those it left out (duplicates, old versions, files it could not read).
func (web *Web) scannedPaths() map[string]bool {
	_, localDB := web.state.get()
	paths := map[string]bool{}
	if localDB == nil {
		return paths
	}
	add := func(file db.ExtendedFileInfo) {
		paths[filepath.Clean(filepath.Join(file.BaseFolder, file.FileName))] = true
	}
	for _, title := range localDB.TitlesMap {
		add(title.File.ExtendedInfo)
		for _, update := range title.Updates {
			add(update.ExtendedInfo)
		}
		for _, dlc := range title.Dlc {
			add(dlc.ExtendedInfo)
		}
	}
	for file := range localDB.Skipped {
		add(file)
	}
	return paths
}

// uncompressedCandidates are the compress candidates without a compressed copy next to
// them; compressing those again would only redo the work.
func (web *Web) uncompressedCandidates() []CompressCandidate {
	copies := web.compressedCopies()
	result := []CompressCandidate{}
	for _, candidate := range web.compressCandidates() {
		if !copies[candidate.Path] {
			result = append(result, candidate)
		}
	}
	return result
}

// fullyChecked tells a file written by the app that was checked whole and found as it should
// be: only then does the file it was made from go.
func fullyChecked(check switchfs.FileCheck) bool {
	return !check.Partial && len(check.Modified) == 0
}

// temporaryOf is the file a task writes before it puts the result in place.
func temporaryOf(source string, target string) string {
	return filepath.Join(filepath.Dir(source), "."+filepath.Base(target)+".tmp")
}

// convertCandidates are the game cards (XCI, XCZ) of the library.
func (web *Web) convertCandidates() []CompressCandidate {
	return web.derived("convertCandidates", func() any { return web.libraryFiles(".xci", ".xcz") }).([]CompressCandidate)
}

// startConversion turns game cards into NSP (or NSZ) files, each one checked before the
// original is deleted, if asked.
func (web *Web) startConversion(paths []string, deleteOriginals bool) bool {
	return web.startConversionBy(paths, deleteOriginals, tasklog.TRIGGER_MANUAL)
}

func (web *Web) startConversionBy(paths []string, deleteOriginals bool, trigger string) bool {
	// every file is copied, then the result is checked
	// read once: converted, and checked while it is written
	return web.runResumableTask(tasklog.TASK_CONVERT, trigger, paths, 1, &tasklog.Resume{Delete: deleteOriginals}, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		return 0, web.convertFile(ctx, path, deleteOriginals, report)
	})
}

func (web *Web) convertFile(ctx context.Context, path string, deleteOriginal bool, report func(step int64) func(int64, int64)) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	target := switchfs.ConvertedPath(path)
	if _, err := os.Stat(target); err == nil {
		return errCompressExists
	}
	release, err := web.reserveSpace(filepath.Dir(path), info.Size())
	if err != nil {
		return err
	}
	defer release()
	temporary := temporaryOf(path, target)
	defer os.Remove(temporary)
	// every NCA must match its content ID: checked while it is written, then the NSP is read
	// back for its files, their sizes and their ends
	written, err := switchfs.ConvertAndCheck(ctx, path, temporary, filling(info.Size(), report(0)))
	if err != nil {
		return err
	}
	// every NCA of the card must be in the NSP
	if same, err := switchfs.HoldsSameContents(path, temporary); err != nil || !same {
		return errors.New("the converted file does not hold every content of the card")
	}
	if err := durable.CommitFile(temporary, target); err != nil {
		return err
	}
	web.verifications().remember(target, written)
	if deleteOriginal && fullyChecked(written) {
		if err := web.discardOriginal(path, QUARANTINE_CONVERTED); err != nil {
			return fmt.Errorf("could not delete the original: %w", err)
		}
		web.moves.replaced(path, target)
	}
	web.sugarLogger.Infof("Converted %s", path)
	return nil
}

// decompressCandidates lists the NSZ files of the library, biggest first.
func (web *Web) decompressCandidates() []CompressCandidate {
	return web.derived("decompressCandidates", func() any { return web.libraryFiles(".nsz") }).([]CompressCandidate)
}

// libraryFiles lists the files of the library with one of the extensions, each file
// once: an XCI can hold a game with its update and DLC.
func (web *Web) libraryFiles(extensions ...string) []CompressCandidate {
	switchDB, localDB := web.state.get()
	candidates := []CompressCandidate{}
	if localDB == nil {
		return candidates
	}
	seen := map[string]bool{}
	add := func(title *db.SwitchGameFiles, file db.SwitchFileInfo, kind string) {
		extension := strings.ToLower(filepath.Ext(file.ExtendedInfo.FileName))
		wanted := false
		for _, candidate := range extensions {
			if extension == candidate {
				wanted = true
			}
		}
		path := filepath.Join(file.ExtendedInfo.BaseFolder, file.ExtendedInfo.FileName)
		if !wanted || seen[path] {
			return
		}
		seen[path] = true
		name := ""
		if title.File.Metadata != nil {
			var known *db.SwitchTitle
			if prefix, err := db.TitleIDPrefix(title.File.Metadata.TitleId); err == nil && switchDB != nil {
				known = switchDB.TitlesMap[prefix]
			}
			name = getLocalTitleName(known, title)
		}
		if name == "" {
			name = strings.TrimSpace(db.ParseTitleNameFromFileName(file.ExtendedInfo.FileName))
		}
		candidates = append(candidates, CompressCandidate{Path: path, Name: name, Kind: kind, Size: file.ExtendedInfo.Size})
	}
	for _, title := range localDB.TitlesMap {
		if title.BaseExist && !title.IsSplit {
			add(title, title.File, "Base")
		}
		for _, update := range title.Updates {
			add(title, update, "Update")
		}
		for _, dlc := range title.Dlc {
			add(title, dlc, "DLC")
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Size == candidates[j].Size {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Size > candidates[j].Size
	})
	return candidates
}

// compressThreads is the number of threads that compress each file: as set, at most every
// processor, or when not set switchfs.UsableProcessors shared among the files compressed at once.
// Measure this server tells the fastest setting on its disk.
func compressThreads(setting int, files int) int {
	if setting <= 0 {
		return max(1, switchfs.UsableProcessors()/max(1, files))
	}
	return min(setting, runtime.NumCPU())
}

// maxFilesAtOnce is the most files a task on files works on at the same time.
const maxFilesAtOnce = 4

// filesAtOnce is how many files a task on files works on at the same time: one when not set.
func filesAtOnce(setting int) int {
	return max(1, min(setting, maxFilesAtOnce))
}

// compressThreadChoices are the numbers of threads offered on the settings page.
func compressThreadChoices() []int {
	choices := []int{}
	for _, n := range []int{1, 2, 4, 6, 8, 12, 16, 24, 32, 48, 64} {
		if n < runtime.NumCPU() {
			choices = append(choices, n)
		}
	}
	return append(choices, runtime.NumCPU())
}

// reserveSpace makes sure a volume can take need more bytes besides the ones the other
// files being written at the same time will take; release gives them back.
func (web *Web) reserveSpace(folder string, need int64) (release func(), err error) {
	web.compressor.mutex.Lock()
	defer web.compressor.mutex.Unlock()
	if free, err := diskfree.Free(folder); err == nil && int64(free) < web.compressor.reserved+need+(64<<20) {
		// the oldest files of the quarantine on that disk give their room first, when they may
		if !web.makeRoom(folder, web.compressor.reserved+need+(64<<20)) {
			return nil, errCompressSpace
		}
		if free, err := diskfree.Free(folder); err == nil && int64(free) < web.compressor.reserved+need+(64<<20) {
			return nil, errCompressSpace
		}
	}
	web.compressor.reserved += need
	return func() {
		web.compressor.mutex.Lock()
		web.compressor.reserved -= need
		web.compressor.mutex.Unlock()
	}, nil
}

// fileJob processes one file and returns the bytes saved; report returns the progress
// callback of a pass over the file, given the bytes of the earlier passes.
type fileJob func(ctx context.Context, path string, report func(step int64) func(int64, int64)) (int64, error)

// startCompression compresses the files in the background. It returns false if a
// compression or decompression is already running.
func (web *Web) startCompression(paths []string, level string, deleteOriginals bool) bool {
	return web.startCompressionBy(paths, level, deleteOriginals, tasklog.TRIGGER_MANUAL)
}

func (web *Web) startCompressionBy(paths []string, level string, deleteOriginals bool, trigger string) bool {
	// every file is read twice: compressed (and hashed meanwhile), then verified
	return web.runResumableTask(tasklog.TASK_COMPRESS, trigger, paths, 2, &tasklog.Resume{Level: level, Delete: deleteOriginals}, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		return web.compressFile(ctx, path, level, deleteOriginals, report)
	})
}

// startDecompression decompresses NSZ files to NSP in the background.
func (web *Web) startDecompression(paths []string, deleteCompressed bool) bool {
	return web.startDecompressionBy(paths, deleteCompressed, tasklog.TRIGGER_MANUAL)
}

func (web *Web) startDecompressionBy(paths []string, deleteCompressed bool, trigger string) bool {
	// every file is read once: decompressed, and checked while it is written
	return web.runResumableTask(tasklog.TASK_DECOMPRESS, trigger, paths, 1, &tasklog.Resume{Delete: deleteCompressed}, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		return 0, web.decompressFile(ctx, path, deleteCompressed, report)
	})
}

// resumeSaveInterval is how often a task on files saves what it did and has left.
var resumeSaveInterval = 30 * time.Second

// runResumableTask is runFileTaskWithTrigger for a task that goes on after a restart.
func (web *Web) runResumableTask(kind string, trigger string, paths []string, passes int64, resume *tasklog.Resume, job fileJob) bool {
	return web.runFileTaskFull(kind, trigger, paths, nil, passes, resume, job)
}

// sortedByName orders files by their name, case aside, then by their folder; the sizes, when
// given, follow their files.
func sortedByName(paths []string, sizes []int64) ([]string, []int64) {
	order := make([]int, len(paths))
	for i := range order {
		order[i] = i
	}
	key := func(path string) string { return strings.ToLower(filepath.Base(path)) }
	sort.SliceStable(order, func(a, b int) bool {
		pa, pb := paths[order[a]], paths[order[b]]
		if ka, kb := key(pa), key(pb); ka != kb {
			return ka < kb
		}
		return pa < pb
	})
	sortedPaths := make([]string, len(paths))
	var sortedSizes []int64
	if len(sizes) == len(paths) {
		sortedSizes = make([]int64, len(sizes))
	}
	for i, from := range order {
		sortedPaths[i] = paths[from]
		if sortedSizes != nil {
			sortedSizes[i] = sizes[from]
		}
	}
	if sortedSizes == nil {
		sortedSizes = sizes
	}
	return sortedPaths, sortedSizes
}

// fileSizes asks the disk the size of every file.
func fileSizes(paths []string) []int64 {
	sizes := make([]int64, len(paths))
	for i, path := range paths {
		if info, err := os.Stat(path); err == nil {
			sizes[i] = info.Size()
		}
	}
	return sizes
}

// resumeInterrupted goes on with the compression, decompression or conversion the app was
// stopped in the middle of, with the files it had left that still need it.
func (web *Web) resumeInterrupted() {
	id, kind, resume := web.taskLog().Interrupted()
	if resume == nil {
		return
	}
	// a check reads the files again where they are now, and leaves out the ones it checked
	if kind == tasklog.TASK_VERIFY {
		if web.resumeVerification(resume) {
			web.continued(id, kind)
		}
		return
	}
	type left struct{ path, target string }
	files := []left{}
	for _, path := range resume.Paths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		target := ""
		switch kind {
		case tasklog.TASK_COMPRESS:
			target = switchfs.CompressedPath(path)
		case tasklog.TASK_DECOMPRESS:
			target = switchfs.DecompressedPath(path)
		case tasklog.TASK_CONVERT:
			target = switchfs.ConvertedPath(path)
		case tasklog.TASK_PACK:
			// the path is the one of the base file of the game, which writes its pack next to it
			packed, _, _, err := web.packTarget(path, "")
			if err != nil {
				continue
			}
			target = packed
		}
		files = append(files, left{path, target})
	}
	// the file being written when the app stopped is left half written: it would keep the file
	// from being written again. Removed only while no task works on the files, under the lock a
	// task takes to start: a task started by hand meanwhile may be writing that very file
	web.compressor.mutex.Lock()
	if web.filesTaken() {
		// kept for when the files are free
		web.compressor.mutex.Unlock()
		return
	}
	for _, file := range files {
		// a split writes several files: it removes what it left half written and keeps the
		// parts it wrote whole
		if file.target != "" {
			os.Remove(temporaryOf(file.path, file.target))
		}
	}
	web.compressor.mutex.Unlock()
	paths := []string{}
	for _, file := range files {
		// done just before the app stopped
		if file.target != "" {
			if _, err := os.Stat(file.target); err == nil {
				continue
			}
		}
		paths = append(paths, file.path)
	}
	if len(paths) == 0 {
		web.taskLog().SetResume(id, nil)
		return
	}
	started := false
	switch kind {
	case tasklog.TASK_COMPRESS:
		started = web.startCompressionBy(paths, resume.Level, resume.Delete, tasklog.TRIGGER_RESUME)
	case tasklog.TASK_DECOMPRESS:
		started = web.startDecompressionBy(paths, resume.Delete, tasklog.TRIGGER_RESUME)
	case tasklog.TASK_CONVERT:
		started = web.startConversionBy(paths, resume.Delete, tasklog.TRIGGER_RESUME)
	case tasklog.TASK_PACK:
		started = web.startPacks(paths, nil, resume.Delete, tasklog.TRIGGER_RESUME)
	case tasklog.TASK_SPLIT:
		started = web.startSplitBy(paths, resume.Delete, tasklog.TRIGGER_RESUME)
	}
	// forgotten once it goes on: kept while another task on files keeps it from starting,
	// for the next start of the app
	if started {
		web.continued(id, kind)
		web.sugarLogger.Infof("[%s goes on after a restart with %d files]", kind, len(paths))
	}
}

// continued forgets what a task the app was stopped in the middle of had left, once it went on
// in a new task (the newest of its kind: it took the files, so no other one started meanwhile),
// and points it to the new one.
func (web *Web) continued(id int64, kind string) {
	web.taskLog().SetResume(id, nil)
	if next := web.latestTaskId(kind); next != id {
		web.taskLog().SetContinued(id, next)
	}
}

// filePhases are the passes over each file of the tasks on files, in their order.
var filePhases = map[string][]string{
	tasklog.TASK_COMPRESS:   {tasklog.PHASE_COMPRESSING, tasklog.PHASE_VERIFYING},
	tasklog.TASK_DECOMPRESS: {tasklog.PHASE_DECOMPRESSING},
	tasklog.TASK_CONVERT:    {tasklog.PHASE_CONVERTING},
	tasklog.TASK_PACK:       {tasklog.PHASE_PACKING},
	tasklog.TASK_SPLIT:      {PHASE_SPLITTING},
}

// stepMeasureMin is how long a step must have run before its speed is trusted.
const stepMeasureMin = 5 * time.Second

// estimateLeft is the time left of a task on files: the bytes each of its steps still has to
// read, at the speed measured for that step on one file (read bytes over the time it took),
// shared among the files worked on at once. A step not measured yet makes it unknown (0),
// except a check, which goes no slower than the slowest step measured.
func estimateLeft(phases []string, read []int64, spent []time.Duration, remaining []int64, workers int) time.Duration {
	speeds := make([]float64, len(phases))
	slowest := 0.0
	for step := range phases {
		if spent[step] >= stepMeasureMin && read[step] > 0 {
			speeds[step] = float64(read[step]) / spent[step].Seconds()
			if slowest == 0 || speeds[step] < slowest {
				slowest = speeds[step]
			}
		}
	}
	seconds := 0.0
	for step, phase := range phases {
		if remaining[step] <= 0 {
			continue
		}
		speed := speeds[step]
		if speed == 0 && phase == tasklog.PHASE_VERIFYING {
			speed = slowest
		}
		if speed == 0 {
			return 0
		}
		seconds += float64(remaining[step]) / speed
	}
	return time.Duration(seconds / float64(max(workers, 1)) * float64(time.Second))
}

// runFileTask runs job on every file as one cancellable task. passes is how many times
// each file is read, for the progress.
func (web *Web) runFileTask(kind string, paths []string, passes int64, job fileJob) bool {
	return web.runFileTaskWithTrigger(kind, tasklog.TRIGGER_MANUAL, paths, passes, job)
}

// runFileTaskWithTrigger is runFileTask for a task started by something else than the user.
func (web *Web) runFileTaskWithTrigger(kind string, trigger string, paths []string, passes int64, job fileJob) bool {
	return web.runFileTaskSized(kind, trigger, paths, nil, passes, job)
}

func (web *Web) runFileTaskSized(kind string, trigger string, paths []string, sizes []int64, passes int64, job fileJob) bool {
	return web.runFileTaskFull(kind, trigger, paths, sizes, passes, nil, job)
}

// runFileTaskFull is runFileTaskWithTrigger with the size each job reads, for jobs that read
// more than their path (a pack reads every file of the game), and, unless resume is nil,
// how the task goes on after a restart: it keeps the files it has left as it goes.
func (web *Web) runFileTaskFull(kind string, trigger string, paths []string, sizes []int64, passes int64, resume *tasklog.Resume, job fileJob) bool {
	ctx, cancel, ok := web.claimFilesFor(kind)
	if !ok {
		return false
	}
	// a list made before Organize moved files, or before a restart: each file where it is now
	paths = web.moves.resolveAll(paths)
	// the files go by name, not the biggest first: the count moves from the start and the list
	// reads in order. A task resumed after a restart too: the file it was writing starts again
	// anyway (its half written copy is removed), so its place in the list loses nothing
	if kind == tasklog.TASK_COMPRESS || kind == tasklog.TASK_DECOMPRESS || kind == tasklog.TASK_CONVERT || kind == tasklog.TASK_PACK || kind == tasklog.TASK_SPLIT {
		paths, sizes = sortedByName(paths, sizes)
	}
	taskId := web.taskLog().Start(kind, trigger)
	web.compressor.mutex.Lock()
	web.compressor.taskId = taskId
	// paused as it started
	if web.compressor.paused {
		web.taskLog().SetPaused(taskId, true)
	}
	web.compressor.mutex.Unlock()
	names := make([]string, len(paths))
	for i, path := range paths {
		names[i] = filepath.Base(path)
	}
	web.taskLog().SetPlan(taskId, names)
	run := &fileTaskRun{web: web, ctx: ctx, kind: kind, taskId: taskId, paths: paths, sizes: sizes, passes: passes, resume: resume, job: job,
		pending: map[int]bool{}, resumeSaved: time.Now()}
	if resume != nil {
		for i := range paths {
			run.pending[i] = true
		}
		web.taskLog().SetResume(taskId, run.leftResume())
	}
	workers, threads := fileTaskWorkers(kind, len(paths), resume, settings.ReadSettings(web.dataFolder))
	web.taskLog().SetWorkers(taskId, workers, threads)
	go func() {
		defer func() {
			web.releaseFiles()
			cancel()
			// the files are free: the next task of the queue
			go web.runQueue()
		}()
		// a panic outside the work on a file (each file has its own): the task ends failed, the
		// files are let go, and it is not started again after a restart
		defer func() {
			if recovered := recover(); recovered != nil {
				note := web.crashed(kind, recovered)
				web.taskLog().SetResume(taskId, nil)
				web.taskLog().Finish(taskId, note)
			}
		}()
		run.run(workers)
		run.finish()
	}()
	return true
}

// claimFilesFor takes the files for a task on them, false when files are being moved or
// deleted, or the server is being measured. The context ends when the task is cancelled.
func (web *Web) claimFilesFor(kind string) (context.Context, context.CancelFunc, bool) {
	spaceVersion := web.verifications().Version()
	web.compressor.mutex.Lock()
	defer web.compressor.mutex.Unlock()
	if web.filesTaken() {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	web.compressor.cancel, web.compressor.kind, web.compressor.taskId = cancel, kind, 0
	web.compressor.awake = awake.Keep()
	web.compressor.spaceVersion = spaceVersion
	return ctx, cancel, true
}

// fileTaskWorkers are the files a task works on at once, as set (a pack is one file), and the
// threads of a compression, as many as fit in the memory free with the level it goes on with.
func fileTaskWorkers(kind string, files int, resume *tasklog.Resume, appSettings *settings.AppSettings) (int, int) {
	workers := 1
	if kind == tasklog.TASK_COMPRESS || kind == tasklog.TASK_DECOMPRESS || kind == tasklog.TASK_CONVERT {
		workers = filesAtOnce(appSettings.CompressFilesAtOnce)
	}
	workers = max(1, min(workers, files))
	threads := 0
	if kind == tasklog.TASK_COMPRESS {
		threads = compressThreads(appSettings.CompressThreads, filesAtOnce(appSettings.CompressFilesAtOnce))
		if resume != nil {
			threads = switchfs.LimitThreads(resume.Level, threads, workers)
		}
	}
	return workers, threads
}

// fileTaskRun is what the workers of a task on files share: take gives each the next file,
// process runs the job on it and noteResult notes how it ended, reporter and publish tell the
// progress and the time left, and finish how the task went. What follows mutex is kept with it
// held.
type fileTaskRun struct {
	web    *Web
	ctx    context.Context
	kind   string
	taskId int64
	paths  []string
	sizes  []int64
	passes int64
	resume *tasklog.Resume
	job    fileJob
	// the bytes read over every file, and the steps of each file
	total  int64
	phases []string
	phased bool

	mutex     sync.Mutex
	completed int64
	partial   []int64
	lanes     []tasklog.Lane
	next      int
	processed int
	// files finished, done or not: the counter of the task, which counts a file once it
	// is compressed and its copy checked, not when it is started
	finished int
	// files of the list no longer there
	gone    int
	saved   int64
	failure *tasklog.Note
	// the files left, kept as the task goes, for a restart in the middle of it, and when they
	// were saved last
	pending     map[int]bool
	resumeSaved time.Time
	// the bytes each step read and the time it took, over every file, for the time left
	stepRead  []int64
	stepSpent []time.Duration
}

// leftResume is how the task goes on with the files it has left.
func (r *fileTaskRun) leftResume() *tasklog.Resume {
	left := *r.resume
	left.Paths = []string{}
	for i, path := range r.paths {
		if r.pending[i] {
			left.Paths = append(left.Paths, path)
		}
	}
	return &left
}

// run works on the files with as many workers, until every file is done or the task stops.
func (r *fileTaskRun) run(workers int) {
	// the sizes not given are asked here, in the background
	if r.sizes == nil {
		r.sizes = fileSizes(r.paths)
	}
	for _, size := range r.sizes {
		r.total += size * r.passes
	}
	r.partial = make([]int64, workers)
	r.lanes = make([]tasklog.Lane, workers)
	// the app stopping saves what is left now, not up to half a minute before
	if r.resume != nil {
		r.web.compressor.mutex.Lock()
		r.web.compressor.flush = func() {
			r.mutex.Lock()
			left := r.leftResume()
			r.mutex.Unlock()
			r.web.taskLog().SetResume(r.taskId, left)
		}
		r.web.compressor.mutex.Unlock()
	}
	r.phases = filePhases[r.kind]
	r.phased = len(r.phases) > 0 && int64(len(r.phases)) == r.passes
	r.stepRead = make([]int64, len(r.phases))
	r.stepSpent = make([]time.Duration, len(r.phases))
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for {
				i, ok := r.take()
				if !ok {
					return
				}
				r.process(worker, i)
			}
		}(worker)
	}
	wait.Wait()
}

// take gives the next file to a worker, once the task is not paused; false when none is left,
// a file failed the task, or it was cancelled.
func (r *fileTaskRun) take() (int, bool) {
	r.web.waitIfPaused(r.ctx)
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.next >= len(r.paths) || r.failure != nil {
		return 0, false
	}
	if r.ctx.Err() != nil {
		r.failure = &tasklog.Note{Text: tasklog.NOTE_COMPRESS_CANCELED}
		return 0, false
	}
	i := r.next
	r.next++
	return i, true
}

// publish gives the progress of every file being worked on. Called with the mutex held.
func (r *fileTaskRun) publish(message string) {
	done := r.completed
	current := []tasklog.Lane{}
	for i, lane := range r.lanes {
		done += r.partial[i]
		if lane.Name != "" {
			current = append(current, lane)
		}
	}
	if !r.phased {
		r.web.taskLog().ProgressFiles(r.taskId, int(done>>20), int(r.total>>20), message, r.finished, len(r.paths), current)
		return
	}
	// what each step has left: every step of the files not started, and the steps
	// to go of the ones being worked on
	remaining := make([]int64, len(r.phases))
	for _, size := range r.sizes[min(r.next, len(r.sizes)):] {
		for step := range remaining {
			remaining[step] += size
		}
	}
	for _, lane := range current {
		if lane.Steps != len(r.phases) || lane.Step < 1 {
			continue
		}
		remaining[lane.Step-1] += lane.StepTotal() - lane.StepDone()
		for step := lane.Step; step < len(r.phases); step++ {
			remaining[step] += lane.StepTotal()
		}
	}
	left := estimateLeft(r.phases, r.stepRead, r.stepSpent, remaining, len(r.lanes))
	r.web.taskLog().ProgressFilesLeft(r.taskId, int(done>>20), int(r.total>>20), message, r.finished, len(r.paths), current, left)
}

// reporter gives the job of file i, on the lane of worker, how to tell its progress in each of
// its steps.
func (r *fileTaskRun) reporter(worker int, i int, name string, fileSize int64) func(step int64) func(int64, int64) {
	return func(step int64) func(int64, int64) {
		lane := tasklog.Lane{Name: name, Total: fileSize, Kind: tasklog.LaneKind(name)}
		index := -1
		if len(r.phases) > 0 && r.sizes[i] > 0 {
			index = min(int(step/r.sizes[i]), len(r.phases)-1)
			lane.Phase, lane.Step, lane.Steps, lane.Phases = r.phases[index], index+1, len(r.phases), r.phases
		}
		// the last report of this step, to measure its speed
		var last time.Time
		var lastDone int64
		return func(fileDone int64, fileTotal int64) {
			reached := time.Now()
			// a pause holds the reading, compressing or checking where it is
			r.web.waitIfPaused(r.ctx)
			resumed := time.Now()
			lane.Done = min(step+fileDone, fileSize)
			r.mutex.Lock()
			if r.phased && index >= 0 {
				// the time held by a pause is not the step's
				if !last.IsZero() {
					r.stepRead[index] += max(lane.Done-lastDone, 0)
					r.stepSpent[index] += reached.Sub(last)
				}
				last, lastDone = resumed, lane.Done
			}
			r.lanes[worker], r.partial[worker] = lane, lane.Done
			r.publish(name)
			r.mutex.Unlock()
		}
	}
}

// process runs the job on file i, on the lane of worker, and notes how it ended.
func (r *fileTaskRun) process(worker int, i int) {
	path := r.paths[i]
	name := filepath.Base(path)
	fileSize := r.sizes[i] * r.passes
	report := r.reporter(worker, i, name, fileSize)
	report(0)(0, 0)
	// a file deleted or moved by something else since the list was made: told
	// as such, not as a file that failed
	var fileSaved int64
	err := r.web.fileThere(path)
	if err == nil {
		fileSaved, err = safely(func() (int64, error) {
			return r.job(r.ctx, path, report)
		})
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.lanes[worker], r.partial[worker] = tasklog.Lane{}, 0
	r.completed += fileSize
	r.noteResult(i, name, fileSaved, err)
	r.publish(name)
}

// noteResult notes how the job on file i ended: done, cancelled, or failed and why, what
// Issues and the Compress page then tell of the file. Called with the mutex held.
func (r *fileTaskRun) noteResult(i int, name string, fileSaved int64, err error) {
	web, kind, taskId, path := r.web, r.kind, r.taskId, r.paths[i]
	if !errors.Is(err, context.Canceled) {
		r.finished++
	}
	if r.resume != nil && !errors.Is(err, context.Canceled) {
		delete(r.pending, i)
		// saved every half minute with what the task did: at most that is done
		// again after a restart
		if time.Since(r.resumeSaved) >= resumeSaveInterval {
			r.resumeSaved = time.Now()
			web.taskLog().SetResume(taskId, r.leftResume())
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		if r.failure == nil {
			r.failure = &tasklog.Note{Text: tasklog.NOTE_COMPRESS_CANCELED}
		}
	case err != nil:
		if fileGone(err) {
			r.gone++
		}
		web.sugarLogger.Warnf("%s of %s failed: %v", kind, path, err)
		web.taskLog().Warn(taskId, compressNote(kind, err), name+": "+err.Error())
		// nothing in it can be compressed (sparse storage, compressed already...): left
		// as it is, which is no failure
		if kind == tasklog.TASK_COMPRESS && errors.Is(err, switchfs.ErrNotCompressible) {
			web.taskLog().SkipFileTile(taskId, name, compressNote(kind, err))
		} else {
			web.taskLog().AddFileTile(taskId, name, true, compressNote(kind, err))
		}
		// nothing in it can be compressed: no longer offered while it stays the same
		if kind == tasklog.TASK_COMPRESS && errors.Is(err, switchfs.ErrNotCompressible) {
			// the size of the file, not the one of its progress (read once per pass)
			web.uncompressible().add(path, r.sizes[i], uncompressibleReason(err))
			web.invalidateDerived()
		}
		// a damaged file: told in Issues like a check finds it, and no longer offered
		// to compress while it does not change; it failed again on every run
		if kind == tasklog.TASK_COMPRESS && compressDamage(err) {
			web.verifications().markDamaged(path, err.Error())
			web.invalidateDerived()
		}
		// a game without its title key: told in Issues, and no longer offered
		if errors.Is(err, switchfs.ErrNoTitleKey) {
			web.noteMissingTitleKey(path)
			web.invalidateDerived()
		}
	default:
		r.processed++
		r.saved += fileSaved
		web.taskLog().AddFileTile(taskId, name, false, "")
		if kind == tasklog.TASK_COMPRESS {
			web.taskLog().AddFileResult(taskId, name, r.sizes[i], r.sizes[i]-fileSaved)
			web.ratios().add(tasklog.ContentKind(path), path, r.sizes[i], r.sizes[i]-fileSaved)
			web.history().addSaved(fileSaved)
		}
		web.taskLog().SetCompressResult(taskId, r.processed, r.saved)
	}
}

// finish ends the task: cancelled, failed when no file was done, or done, and scans the library
// when files were written or are gone.
func (r *fileTaskRun) finish() {
	if r.failure == nil && r.processed == 0 && len(r.paths) > 0 && r.gone == len(r.paths) {
		r.failure = &tasklog.Note{Text: NOTE_FILE_GONE}
	}
	if r.failure == nil && r.processed == 0 && len(r.paths) > 0 {
		r.failure = &tasklog.Note{Text: NOTE_COMPRESS_FAILED}
		if r.kind == tasklog.TASK_DECOMPRESS {
			r.failure = &tasklog.Note{Text: NOTE_DECOMPRESS_FAILED}
		} else if r.kind == tasklog.TASK_PACK {
			r.failure = &tasklog.Note{Text: NOTE_PACK_FAILED}
		} else if r.kind == tasklog.TASK_SPLIT {
			r.failure = &tasklog.Note{Text: NOTE_SPLIT_FAILED}
		}
	}
	// ended by itself or cancelled: nothing goes on after a restart
	if r.resume != nil {
		r.web.taskLog().SetResume(r.taskId, nil)
	}
	r.web.taskLog().Finish(r.taskId, r.failure)
	// files were written, or the library still lists files that are gone
	if r.processed > 0 || r.gone > 0 {
		r.web.Rescan(tasklog.TRIGGER_COMPRESS)
	}
}

// NOTE_FILE_GONE is told of a file of the list that is no longer where the list had it, and
// that the app did not see move: deleted, or moved by something else.
const NOTE_FILE_GONE = "The file is no longer there: it was moved, renamed or deleted after the list was made. Try again once the library has been scanned."

// errFileGone is a file of the list of a task that is no longer there.
var errFileGone = errors.New("the file is no longer there")

// fileGone tells an error of a file of the list that is no longer there, not one of a file
// that was there and failed (an original that could not be deleted has its own note).
func fileGone(err error) bool {
	if errors.Is(err, errFileGone) {
		return true
	}
	return errors.Is(err, fs.ErrNotExist) && !strings.Contains(err.Error(), "could not delete")
}

// errors of compressFile, mapped to notes
var (
	errCompressSpace  = errors.New("not enough free space")
	errCompressExists = errors.New("the compressed file already exists")
)

// compressDamage tells a compression that failed on the file itself: an NCA that does not
// match its content ID and fails its hash trees, or a file cut short. A file that could not be
// read, or a copy that failed its check, says nothing of what the file holds.
func compressDamage(err error) bool {
	if notContent(err) {
		return false
	}
	return strings.Contains(err.Error(), "damaged") || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func compressNote(kind string, err error) string {
	if fileGone(err) {
		return NOTE_FILE_GONE
	}
	if kind == tasklog.TASK_SPLIT {
		switch {
		case errors.Is(err, errCompressSpace):
			return NOTE_COMPRESS_SPACE
		case errors.Is(err, errSplitExists):
			return NOTE_SPLIT_EXISTS
		case strings.Contains(err.Error(), "could not delete"):
			return NOTE_COMPRESS_DELETE
		}
		return NOTE_SPLIT_FAILED
	}
	if kind == tasklog.TASK_PACK {
		switch {
		case errors.Is(err, errCompressSpace):
			return NOTE_COMPRESS_SPACE
		case errors.Is(err, errPackExists):
			return NOTE_PACK_EXISTS
		case strings.Contains(err.Error(), "could not delete"):
			return NOTE_COMPRESS_DELETE
		}
		return NOTE_PACK_FAILED
	}
	switch {
	case errors.Is(err, errCompressSpace):
		return NOTE_COMPRESS_SPACE
	case errors.Is(err, errCompressExists):
		return NOTE_COMPRESS_EXISTS
	case uncompressibleReason(err) != "":
		return uncompressibleNotes[uncompressibleReason(err)]
	case errors.Is(err, switchfs.ErrNotCompressible):
		return NOTE_COMPRESS_NOTHING
	case errors.Is(err, switchfs.ErrNoTitleKey):
		return NOTE_COMPRESS_NO_KEY
	case strings.Contains(err.Error(), "damaged") && !notContent(err):
		return NOTE_COMPRESS_DAMAGED
	case kind == tasklog.TASK_COMPRESS && compressDamage(err):
		return NOTE_COMPRESS_DAMAGED
	case strings.Contains(err.Error(), "could not delete"):
		return NOTE_COMPRESS_DELETE
	}
	if kind == tasklog.TASK_DECOMPRESS {
		return NOTE_DECOMPRESS_FAILED
	}
	return NOTE_COMPRESS_FAILED
}

// compressFile compresses one NSP. The NSZ is written next to it under a hidden
// temporary name, verified, and only then renamed; the original is deleted last, if
// asked. It returns the bytes saved.
func (web *Web) compressFile(ctx context.Context, path string, level string, deleteOriginal bool, report func(step int64) func(int64, int64)) (int64, error) {
	appSettings := settings.ReadSettings(web.dataFolder)
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	target := switchfs.CompressedPath(path)
	if _, err := os.Stat(target); err == nil {
		return 0, errCompressExists
	}
	release, err := web.reserveSpace(filepath.Dir(path), info.Size())
	if err != nil {
		return 0, err
	}
	defer release()

	// hidden, so scans ignore the file while it is written
	temporary := temporaryOf(path, target)
	defer os.Remove(temporary)
	// the original is hashed while it is compressed: it is read once, not twice
	result, err := switchfs.CompressGame(ctx, path, temporary, switchfs.CompressOptions{
		Level: level, Workers: switchfs.LimitThreads(level, compressThreads(appSettings.CompressThreads, filesAtOnce(appSettings.CompressFilesAtOnce)), filesAtOnce(appSettings.CompressFilesAtOnce)), Progress: filling(info.Size(), report(0)),
	})
	if err != nil {
		// a file that could not be read is not damaged
		return 0, readFailure(path, info, err)
	}
	// an NCA whose hash is not its content ID: rewritten by a tool, or damaged
	originalCheck, err := switchfs.CheckHashes(ctx, path, result.Hashes)
	if err != nil {
		return 0, readFailure(path, info, err)
	}
	// reading the original whole was its check: the next file check does not read it again
	web.verifications().remember(path, originalCheck)
	if err := switchfs.VerifyCompressed(ctx, temporary, result.Hashes, filling(info.Size(), report(info.Size()))); err != nil {
		// the copy failed its check, not the original
		if errors.Is(err, context.Canceled) {
			return 0, err
		}
		return 0, &notContentError{err}
	}
	if err := durable.CommitFile(temporary, target); err != nil {
		return 0, err
	}
	web.rememberWritten(target)
	web.verifications().inherit(path, target)
	if deleteOriginal {
		if err := web.discardOriginal(path, QUARANTINE_COMPRESSED); err != nil {
			return 0, fmt.Errorf("could not delete the original: %w", err)
		}
		// a pack asked for the game in the queue makes it of the NSZ
		web.moves.replaced(path, target)
	}
	web.sugarLogger.Infof("Compressed %s: %d -> %d bytes", path, result.InputSize, result.OutputSize)
	return result.InputSize - result.OutputSize, nil
}

// filling makes the progress of one step fill its share of the bar of a file, the size of the
// file: a step counts its own bytes (the files inside an NSZ, the NSP inside a game card, the
// files a pack keeps), fewer than the file holds, and the bar stopped short of its end.
func filling(size int64, progress func(int64, int64)) func(int64, int64) {
	return func(done int64, total int64) {
		if total > 0 && total != size {
			done = int64(float64(done) / float64(total) * float64(size))
		}
		progress(min(done, size), size)
	}
}

// decompressFile writes the NSP of an NSZ next to it under a hidden temporary name,
// checks every NCA against its content ID, and only then renames it; the NSZ is deleted
// last, if asked.
func (web *Web) decompressFile(ctx context.Context, path string, deleteCompressed bool, report func(step int64) func(int64, int64)) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	target := switchfs.DecompressedPath(path)
	if _, err := os.Stat(target); err == nil {
		return errCompressExists
	}
	size, err := switchfs.DecompressedSize(path)
	if err != nil {
		return err
	}
	release, err := web.reserveSpace(filepath.Dir(path), size)
	if err != nil {
		return err
	}
	defer release()

	temporary := temporaryOf(path, target)
	defer os.Remove(temporary)
	// every NCA must match its content ID: checked while it is written, then the NSP is read
	// back for its files, their sizes and their ends
	written, err := switchfs.DecompressAndCheck(ctx, path, temporary, filling(info.Size(), report(0)))
	if err != nil {
		return err
	}
	if err := durable.CommitFile(temporary, target); err != nil {
		return err
	}
	web.verifications().remember(target, written)
	if deleteCompressed && fullyChecked(written) {
		if err := web.discardOriginal(path, QUARANTINE_DECOMPRESSED); err != nil {
			return fmt.Errorf("could not delete the original: %w", err)
		}
		web.moves.replaced(path, target)
	}
	web.sugarLogger.Infof("Decompressed %s", path)
	return nil
}

// pausableKinds are the tasks on files that hold where they are while paused; the others (a
// check, a copy to the SD card) are only cancelled.
var pausableKinds = map[string]bool{tasklog.TASK_COMPRESS: true, tasklog.TASK_DECOMPRESS: true, tasklog.TASK_CONVERT: true, tasklog.TASK_PACK: true, tasklog.TASK_SPLIT: true}

// pauseCompression holds the running compression (or decompression, conversion, pack) where
// it is, in the middle of a file too, or lets it go on; a cancel still stops it while paused.
func (web *Web) pauseCompression(paused bool) bool {
	c := &web.compressor
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.cancel == nil || c.paused == paused || !pausableKinds[c.kind] {
		return false
	}
	c.paused = paused
	if paused {
		c.resume = make(chan struct{})
	} else {
		close(c.resume)
	}
	// paused, the computer may sleep
	c.awake.Pause(paused)
	if c.taskId != 0 {
		web.taskLog().SetPaused(c.taskId, paused)
	}
	return true
}

// releaseFiles ends the task working on the files: none runs, and nothing stays paused for
// the next one.
func (web *Web) releaseFiles() {
	c := &web.compressor
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.cancel, c.kind, c.flush = nil, "", nil
	c.awake.Release()
	c.awake = nil
	if c.paused {
		c.paused = false
		close(c.resume)
		if c.taskId != 0 {
			web.taskLog().SetPaused(c.taskId, false)
		}
	}
	c.taskId = 0
}

// waitIfPaused returns at once, or when the paused task is resumed or cancelled.
func (web *Web) waitIfPaused(ctx context.Context) {
	c := &web.compressor
	c.mutex.Lock()
	if !c.paused {
		c.mutex.Unlock()
		return
	}
	resume := c.resume
	c.mutex.Unlock()
	select {
	case <-resume:
	case <-ctx.Done():
	}
}

func (web *Web) cancelCompression() bool {
	web.compressor.mutex.Lock()
	defer web.compressor.mutex.Unlock()
	if web.compressor.cancel == nil {
		return false
	}
	web.compressor.cancel()
	return true
}

// filesBusy tells whether a task is changing the files of the library: a compression,
// decompression, conversion, pack, organizing, cleanup or the measure of the server. Scans
// wait for it to end.
func (web *Web) filesBusy() bool {
	return web.organizing.Load() || web.measuring.Load() || web.compressionRunning() || web.uploads.writing()
}

// spaceHeld gives the version of the check results the Space page follows while a task writes
// files: the one of when it began.
func (web *Web) spaceHeld() (uint64, bool) {
	web.compressor.mutex.Lock()
	defer web.compressor.mutex.Unlock()
	return web.compressor.spaceVersion, web.compressor.cancel != nil && pausableKinds[web.compressor.kind]
}

func (web *Web) compressionRunning() bool {
	web.compressor.mutex.Lock()
	defer web.compressor.mutex.Unlock()
	return web.compressor.cancel != nil
}

func (web *Web) HandleCompress() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/compress.html")

	web.router.HandleFunc("/compress.html", func(w http.ResponseWriter, r *http.Request) {
		candidates := web.uncompressedCandidates()
		data := CompressPageData{GlobalPageData: web.globalPageData("compress"), Candidates: candidates, CompressedCount: len(web.decompressCandidates()), CardCount: len(web.convertCandidates()), SplitCount: len(web.splitCandidates()), PackCount: len(web.packCandidates()), Running: web.compressionRunning()}
		data.AlreadyCompressed = len(web.compressCandidates()) - len(candidates)
		data.Level = settingsLevel(settings.ReadSettings(web.dataFolder))
		data.Uncompressible = web.uncompressibleFiles()
		damaged := web.verifications().damaged()
		for _, file := range web.libraryFiles(".nsp", ".xci") {
			if damaged[file.Path] != "" {
				data.Damaged++
			}
		}
		// the candidates are shared: the estimates go on a copy
		ratios := web.ratios()
		candidates = append([]CompressCandidate(nil), candidates...)
		for i := range candidates {
			candidates[i].Estimate, candidates[i].HasEstimate = ratios.estimate(candidates[i].Kind, candidates[i].Path)
			data.Estimated += candidates[i].EstimateBytes()
		}
		data.Ratios = ratios.summary()
		data.Candidates = candidates
		for i, candidate := range candidates {
			data.TotalSize += candidate.Size
			if i >= shownFiles {
				data.RestCount++
				data.RestSize += candidate.Size
			}
		}
		if len(candidates) > shownFiles {
			data.Candidates = candidates[:shownFiles]
		}
		web.render(w, r, templates, data)
	}).Methods("GET")

	// the NSZ files that match a search, for the Decompress section
	web.router.HandleFunc("/compress/xci-list", func(w http.ResponseWriter, r *http.Request) {
		web.writeFileRows(w, r, templates, "xci", false, web.convertCandidates())
	}).Methods("GET")

	web.router.HandleFunc("/convert/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		paths := web.libraryPaths(r.Form["path"], web.convertCandidates())
		if len(paths) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the files to convert.")
			return
		}
		web.startWriting(w, r, QueuedTask{Kind: tasklog.TASK_CONVERT, Paths: paths, Delete: r.FormValue("delete_originals") == "true"})
	}).Methods("POST")

	web.router.HandleFunc("/compress/nsz-list", func(w http.ResponseWriter, r *http.Request) {
		web.writeFileRows(w, r, templates, "nsz", false, web.decompressCandidates())
	}).Methods("GET")

	// the files that hold several titles, and the games a pack can be made of
	web.router.HandleFunc("/compress/split-list", func(w http.ResponseWriter, r *http.Request) {
		web.writeFileRows(w, r, templates, "split", true, web.splitCandidates())
	}).Methods("GET")
	web.router.HandleFunc("/compress/pack-list", func(w http.ResponseWriter, r *http.Request) {
		web.writeFileRows(w, r, templates, "pack", true, web.packCandidates())
	}).Methods("GET")
	web.handleSplit()
	web.handlePacks()

	web.router.HandleFunc("/compress/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		if keys, _ := settings.SwitchKeys(); keys == nil || keys.GetKey("header_key") == "" {
			writeGlobalError(w, http.StatusBadRequest, lang, "prod.keys is needed to compress games.")
			return
		}
		candidates := web.uncompressedCandidates()
		requested := r.Form["path"]
		// "the rest": the files not listed one by one
		if r.FormValue("rest") == "compress" {
			for i, candidate := range candidates {
				if i >= shownFiles {
					requested = append(requested, candidate.Path)
				}
			}
		}
		paths := web.libraryPaths(requested, candidates)
		if len(paths) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the files to compress.")
			return
		}
		level := r.FormValue("level")
		if level != switchfs.LevelFast && level != switchfs.LevelMax {
			level = switchfs.LevelBalanced
		}
		// only an administrator deletes the originals
		web.startWriting(w, r, QueuedTask{Kind: tasklog.TASK_COMPRESS, Paths: paths, Level: level, Delete: r.FormValue("delete_originals") == "true" && isAdminRequest(r)})
	}).Methods("POST")

	web.router.HandleFunc("/decompress/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		paths := web.libraryPaths(r.Form["path"], web.decompressCandidates())
		if len(paths) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the files to decompress.")
			return
		}
		web.startWriting(w, r, QueuedTask{Kind: tasklog.TASK_DECOMPRESS, Paths: paths, Delete: r.FormValue("delete_compressed") == "true"})
	}).Methods("POST")

	web.router.HandleFunc("/compress/cancel", func(w http.ResponseWriter, r *http.Request) {
		web.cancelCompression()
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")

	web.router.HandleFunc("/compress/pause", func(w http.ResponseWriter, r *http.Request) {
		web.pauseCompression(r.FormValue("paused") != "false")
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")
}

// writeFileRows writes the rows of a list of files of the Compress page, the ones matching
// the search, at most shownFiles. With rest, the others can be chosen together while there is
// no search.
func (web *Web) writeFileRows(w http.ResponseWriter, r *http.Request, templates templateSet, prefix string, rest bool, all []CompressCandidate) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	data := NszListData{Total: len(all), Prefix: prefix}
	for _, candidate := range all {
		if query != "" && !strings.Contains(strings.ToLower(candidate.Name+" "+filepath.Base(candidate.Path)), query) {
			continue
		}
		data.Matches++
		if len(data.Files) < shownFiles {
			data.Files = append(data.Files, candidate)
		} else if rest && query == "" {
			data.Rest++
			data.RestSize += candidate.Size
		}
	}
	// the games packed before follow the list, so it tells why they are not in it
	if prefix == "pack" {
		for _, game := range web.packedGames() {
			if query == "" || strings.Contains(strings.ToLower(game.Name+" "+game.Packed), query) {
				data.Packed = append(data.Packed, game)
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.executeTemplate(w, web.requestLanguage(r), "nszRows", data); err != nil {
		web.sugarLogger.Error(err)
	}
}
