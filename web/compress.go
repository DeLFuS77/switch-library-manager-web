package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// problems of a compression, translated by the interface
const (
	NOTE_COMPRESS_FAILED   = "The file could not be compressed, the original is kept."
	NOTE_COMPRESS_DAMAGED  = "The file is damaged or was modified, it was not compressed."
	NOTE_COMPRESS_SPACE    = "Not enough free space to compress the file."
	NOTE_COMPRESS_EXISTS   = "A compressed file with the same name already exists."
	NOTE_COMPRESS_NOTHING  = "Nothing in the file can be compressed."
	NOTE_COMPRESS_CANCELED = "The compression was cancelled."
	NOTE_COMPRESS_DELETE   = "The original could not be deleted."
	NOTE_DECOMPRESS_FAILED = "The file could not be decompressed, the compressed file is kept."
)

var compressNoteTexts = []string{NOTE_PACK_FAILED, NOTE_PACK_EXISTS, NOTE_DECOMPRESS_FAILED, NOTE_COMPRESS_FAILED, NOTE_COMPRESS_DAMAGED, NOTE_COMPRESS_SPACE, NOTE_COMPRESS_EXISTS, NOTE_COMPRESS_NOTHING, NOTE_COMPRESS_CANCELED, NOTE_COMPRESS_DELETE}

// CompressCandidate is an NSP of the library that can be compressed.
type CompressCandidate struct {
	Path string
	Name string
	// Base, Update or DLC
	Kind string
	Size int64
}

// NszListData is a page of the NSZ files that can be decompressed.
type NszListData struct {
	Files   []CompressCandidate
	Matches int
	Total   int
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
	// NSP and XCI files that already have a compressed copy (listed on the Space page)
	AlreadyCompressed int
	TotalSize         int64
	// compressed files that can be decompressed
	Running bool
}

// compressor runs one compression at a time and can cancel it.
type compressor struct {
	mutex  sync.Mutex
	cancel context.CancelFunc
	// the bytes the files being written will still take, for the space checks
	reserved int64
}

// compressCandidates lists the NSP and XCI files of the library, biggest first.
func (web *Web) compressCandidates() []CompressCandidate {
	return web.derived("compressCandidates", func() any { return web.libraryFiles(".nsp", ".xci") }).([]CompressCandidate)
}

// compressedCopies are the compress candidates that have a compressed copy next to them.
// Asking the disk for every file is slow on NAS shares, so the answer is kept until the
// library changes (a compression or the folder watcher rescans it).
func (web *Web) compressedCopies() map[string]bool {
	return web.derived("compressedCopies", func() any {
		copies := map[string]bool{}
		for _, candidate := range web.compressCandidates() {
			if _, err := os.Stat(switchfs.CompressedPath(candidate.Path)); err == nil {
				copies[candidate.Path] = true
			}
		}
		return copies
	}).(map[string]bool)
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

// decompressCandidates lists the NSZ files of the library, biggest first.
// convertCandidates are the game cards (XCI, XCZ) of the library.
func (web *Web) convertCandidates() []CompressCandidate {
	return web.derived("convertCandidates", func() any { return web.libraryFiles(".xci", ".xcz") }).([]CompressCandidate)
}

// startConversion turns game cards into NSP (or NSZ) files, each one checked before the
// original is deleted, if asked.
func (web *Web) startConversion(paths []string, deleteOriginals bool) bool {
	// every file is copied, then the result is checked
	return web.runFileTask(TASK_CONVERT, paths, 2, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
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
	temporary := filepath.Join(filepath.Dir(path), "."+filepath.Base(target)+".tmp")
	defer os.Remove(temporary)
	if err := switchfs.ConvertXciToNsp(ctx, path, temporary, report(0)); err != nil {
		return err
	}
	// every NCA must match its content ID
	if strings.EqualFold(filepath.Ext(target), ".nsz") {
		err = switchfs.VerifyCompressed(ctx, temporary, nil, report(info.Size()))
	} else {
		_, err = switchfs.HashNcas(ctx, temporary, report(info.Size()))
	}
	if err != nil {
		return err
	}
	// every NCA of the card must be in the NSP
	if same, err := switchfs.HoldsSameContents(path, temporary); err != nil || !same {
		return errors.New("the converted file does not hold every content of the card")
	}
	if err := os.Rename(temporary, target); err != nil {
		return err
	}
	if deleteOriginal {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("could not delete the original: %w", err)
		}
	}
	web.sugarLogger.Infof("Converted %s", path)
	return nil
}

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

// compressWorkers keeps half of the processors free for the rest of the system.
func compressWorkers() int {
	workers := runtime.NumCPU() / 2
	if workers > 4 {
		workers = 4
	}
	if workers < 1 {
		workers = 1
	}
	return workers
}

// compressThreads is the number of threads that compress each file: as set, at most every
// processor, or when not set compressWorkers shared among the files compressed at once. The
// disk sets the speed more than the processor: on a game of 16 GB, 24 threads were slower
// than 4, and two files with 2 threads each faster than one file after the other with 4.
func compressThreads(setting int, files int) int {
	if setting <= 0 {
		return max(1, compressWorkers()/max(1, files))
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
	if free, err := diskFree(folder); err == nil && int64(free) < web.compressor.reserved+need+(64<<20) {
		return nil, errCompressSpace
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
	// every file is read three times: hashed, compressed and verified
	return web.runFileTask(TASK_COMPRESS, paths, 3, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		return web.compressFile(ctx, path, level, deleteOriginals, report)
	})
}

// startDecompression decompresses NSZ files to NSP in the background.
func (web *Web) startDecompression(paths []string, deleteCompressed bool) bool {
	// every file is decompressed, then the result is hashed
	return web.runFileTask(TASK_DECOMPRESS, paths, 2, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		return 0, web.decompressFile(ctx, path, deleteCompressed, report)
	})
}

// filePhases are the passes over each file of the tasks on files, in their order.
var filePhases = map[string][]string{
	TASK_COMPRESS:   {PHASE_HASHING, PHASE_COMPRESSING, PHASE_VERIFYING},
	TASK_DECOMPRESS: {PHASE_DECOMPRESSING, PHASE_VERIFYING},
	TASK_CONVERT:    {PHASE_CONVERTING, PHASE_VERIFYING},
	TASK_PACK:       {PHASE_PACKING, PHASE_VERIFYING},
}

// the passes over a file, translated by the interface
const (
	PHASE_HASHING       = "Calculating hashes"
	PHASE_COMPRESSING   = "Compressing"
	PHASE_DECOMPRESSING = "Decompressing"
	PHASE_CONVERTING    = "Converting"
	PHASE_PACKING       = "Building the pack"
	PHASE_VERIFYING     = "Verifying"
)

// runFileTask runs job on every file as one cancellable task. passes is how many times
// each file is read, for the progress.
func (web *Web) runFileTask(kind string, paths []string, passes int64, job fileJob) bool {
	return web.runFileTaskWithTrigger(kind, TRIGGER_MANUAL, paths, passes, job)
}

// runFileTaskWithTrigger is runFileTask for a task started by something else than the user.
func (web *Web) runFileTaskWithTrigger(kind string, trigger string, paths []string, passes int64, job fileJob) bool {
	sizes := make([]int64, len(paths))
	for i, path := range paths {
		if info, err := os.Stat(path); err == nil {
			sizes[i] = info.Size()
		}
	}
	return web.runFileTaskSized(kind, trigger, paths, sizes, passes, job)
}

// runFileTaskSized is runFileTaskWithTrigger with the size each job reads, for jobs that
// read more than their path (a pack reads every file of the game).
func (web *Web) runFileTaskSized(kind string, trigger string, paths []string, sizes []int64, passes int64, job fileJob) bool {
	web.compressor.mutex.Lock()
	// files are being moved or deleted, or the server is being measured
	if web.compressor.cancel != nil || web.organizing.Load() || web.measuring.Load() {
		web.compressor.mutex.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	web.compressor.cancel = cancel
	web.compressor.mutex.Unlock()

	taskId := web.taskLog().Start(kind, trigger)
	appSettings := settings.ReadSettings(web.dataFolder)
	threads := 0
	if kind == TASK_COMPRESS {
	}
	// several files at once, as set; a pack is one file
	workers := 1
	if kind == TASK_COMPRESS || kind == TASK_DECOMPRESS || kind == TASK_CONVERT {
		workers = filesAtOnce(appSettings.CompressFilesAtOnce)
	}
	workers = max(1, min(workers, len(paths)))
	if kind == TASK_COMPRESS {
		threads = compressThreads(appSettings.CompressThreads, filesAtOnce(appSettings.CompressFilesAtOnce))
	}
	web.taskLog().SetWorkers(taskId, workers, threads)
	go func() {
		defer func() {
			web.compressor.mutex.Lock()
			web.compressor.cancel = nil
			web.compressor.mutex.Unlock()
			cancel()
		}()

		var total int64
		for _, size := range sizes {
			total += size * passes
		}

		var (
			mutex     sync.Mutex
			completed int64
			partial   = make([]int64, workers)
			lanes     = make([]TaskLane, workers)
			next      int
			processed int
			saved     int64
			failure   *TaskNote
			wait      sync.WaitGroup
		)
		// publish gives the progress of every file being worked on; called with the mutex held
		publish := func(message string) {
			done := completed
			current := []TaskLane{}
			for i, lane := range lanes {
				done += partial[i]
				if lane.Name != "" {
					current = append(current, lane)
				}
			}
			web.taskLog().ProgressLanes(taskId, int(done>>20), int(total>>20), message, current)
		}
		for worker := 0; worker < workers; worker++ {
			wait.Add(1)
			go func(worker int) {
				defer wait.Done()
				for {
					mutex.Lock()
					if next >= len(paths) || failure != nil {
						mutex.Unlock()
						return
					}
					if ctx.Err() != nil {
						failure = &TaskNote{Text: NOTE_COMPRESS_CANCELED}
						mutex.Unlock()
						return
					}
					i := next
					next++
					mutex.Unlock()

					path := paths[i]
					name := filepath.Base(path)
					fileSize := sizes[i] * passes
					message := fmt.Sprintf("%s (%d/%d)", name, i+1, len(paths))
					report := func(step int64) func(int64, int64) {
						lane := TaskLane{Name: name, Total: fileSize}
						if phases := filePhases[kind]; len(phases) > 0 && sizes[i] > 0 {
							index := min(int(step/sizes[i]), len(phases)-1)
							lane.Phase, lane.Step, lane.Steps = phases[index], index+1, len(phases)
						}
						return func(fileDone int64, fileTotal int64) {
							lane.Done = min(step+fileDone, fileSize)
							mutex.Lock()
							lanes[worker], partial[worker] = lane, lane.Done
							publish(message)
							mutex.Unlock()
						}
					}
					report(0)(0, 0)
					fileSaved, err := job(ctx, path, report)

					mutex.Lock()
					lanes[worker], partial[worker] = TaskLane{}, 0
					completed += fileSize
					switch {
					case errors.Is(err, context.Canceled):
						if failure == nil {
							failure = &TaskNote{Text: NOTE_COMPRESS_CANCELED}
						}
					case err != nil:
						web.sugarLogger.Warnf("%s of %s failed: %v", kind, path, err)
						web.taskLog().Warn(taskId, compressNote(kind, err), name+": "+err.Error())
					default:
						processed++
						saved += fileSaved
						if kind == TASK_COMPRESS {
							web.taskLog().AddFileResult(taskId, name, sizes[i], sizes[i]-fileSaved)
						}
						web.taskLog().SetCompressResult(taskId, processed, saved)
					}
					publish(message)
					mutex.Unlock()
				}
			}(worker)
		}
		wait.Wait()

		if failure == nil && processed == 0 && len(paths) > 0 {
			failure = &TaskNote{Text: NOTE_COMPRESS_FAILED}
			if kind == TASK_DECOMPRESS {
				failure = &TaskNote{Text: NOTE_DECOMPRESS_FAILED}
			} else if kind == TASK_PACK {
				failure = &TaskNote{Text: NOTE_PACK_FAILED}
			}
		}
		web.taskLog().Finish(taskId, failure)
		if processed > 0 {
			web.Rescan(TRIGGER_COMPRESS)
		}
	}()
	return true
}

// errors of compressFile, mapped to notes
var (
	errCompressSpace  = errors.New("not enough free space")
	errCompressExists = errors.New("the compressed file already exists")
)

func compressNote(kind string, err error) string {
	if kind == TASK_PACK {
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
	case errors.Is(err, switchfs.ErrNotCompressible):
		return NOTE_COMPRESS_NOTHING
	case strings.Contains(err.Error(), "damaged"):
		return NOTE_COMPRESS_DAMAGED
	case strings.Contains(err.Error(), "could not delete"):
		return NOTE_COMPRESS_DELETE
	}
	if kind == TASK_DECOMPRESS {
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

	expected, err := switchfs.HashNcas(ctx, path, report(0))
	if err != nil {
		return 0, err
	}

	// hidden, so scans ignore the file while it is written
	temporary := filepath.Join(filepath.Dir(path), "."+filepath.Base(target)+".tmp")
	defer os.Remove(temporary)
	result, err := switchfs.CompressGame(ctx, path, temporary, switchfs.CompressOptions{
		Level: level, Workers: compressThreads(appSettings.CompressThreads, filesAtOnce(appSettings.CompressFilesAtOnce)), Progress: report(info.Size()),
	})
	if err != nil {
		return 0, err
	}
	if err := switchfs.VerifyCompressed(ctx, temporary, expected, report(2*info.Size())); err != nil {
		return 0, err
	}
	if err := os.Rename(temporary, target); err != nil {
		return 0, err
	}
	if deleteOriginal {
		if err := os.Remove(path); err != nil {
			return 0, fmt.Errorf("could not delete the original: %w", err)
		}
	}
	web.sugarLogger.Infof("Compressed %s: %d -> %d bytes", path, result.InputSize, result.OutputSize)
	return result.InputSize - result.OutputSize, nil
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

	temporary := filepath.Join(filepath.Dir(path), "."+filepath.Base(target)+".tmp")
	defer os.Remove(temporary)
	if err := switchfs.DecompressNsz(ctx, path, temporary, report(0)); err != nil {
		return err
	}
	// every NCA must match its content ID
	if _, err := switchfs.HashNcas(ctx, temporary, report(info.Size())); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		return err
	}
	if deleteCompressed {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("could not delete the original: %w", err)
		}
	}
	web.sugarLogger.Infof("Decompressed %s", path)
	return nil
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
	return web.organizing.Load() || web.measuring.Load() || web.compressionRunning()
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
		data := CompressPageData{GlobalPageData: web.globalPageData("compress"), Candidates: candidates, CompressedCount: len(web.decompressCandidates()), CardCount: len(web.convertCandidates()), Running: web.compressionRunning()}
		data.AlreadyCompressed = len(web.compressCandidates()) - len(candidates)
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
		web.writeFileRows(w, r, templates, web.convertCandidates())
	}).Methods("GET")

	web.router.HandleFunc("/convert/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		known := map[string]bool{}
		for _, candidate := range web.convertCandidates() {
			known[candidate.Path] = true
		}
		paths := []string{}
		for _, path := range r.Form["path"] {
			if known[path] {
				paths = append(paths, path)
			}
		}
		if len(paths) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the files to convert.")
			return
		}
		if !web.startConversion(paths, r.FormValue("delete_originals") == "true") {
			writeGlobalError(w, http.StatusConflict, lang, "A compression is already running.")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"started": len(paths)})
	}).Methods("POST")

	web.router.HandleFunc("/compress/nsz-list", func(w http.ResponseWriter, r *http.Request) {
		web.writeFileRows(w, r, templates, web.decompressCandidates())
	}).Methods("GET")

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
		// only files of the library can be compressed
		known := map[string]bool{}
		requested := r.Form["path"]
		for i, candidate := range web.uncompressedCandidates() {
			known[candidate.Path] = true
			// "the rest": the files not listed one by one
			if i >= shownFiles && r.FormValue("rest") == "compress" {
				requested = append(requested, candidate.Path)
			}
		}
		paths := []string{}
		seen := map[string]bool{}
		for _, path := range requested {
			if known[path] && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
		if len(paths) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the files to compress.")
			return
		}
		level := r.FormValue("level")
		if level != switchfs.LevelFast && level != switchfs.LevelMax {
			level = switchfs.LevelBalanced
		}
		if !web.startCompression(paths, level, r.FormValue("delete_originals") == "true") {
			writeGlobalError(w, http.StatusConflict, lang, "A compression is already running.")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"started": len(paths)})
	}).Methods("POST")

	web.router.HandleFunc("/decompress/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		known := map[string]bool{}
		for _, candidate := range web.decompressCandidates() {
			known[candidate.Path] = true
		}
		paths := []string{}
		for _, path := range r.Form["path"] {
			if known[path] {
				paths = append(paths, path)
			}
		}
		if len(paths) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the files to decompress.")
			return
		}
		if !web.startDecompression(paths, r.FormValue("delete_compressed") == "true") {
			writeGlobalError(w, http.StatusConflict, lang, "A compression is already running.")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"started": len(paths)})
	}).Methods("POST")

	web.router.HandleFunc("/compress/cancel", func(w http.ResponseWriter, r *http.Request) {
		web.cancelCompression()
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")
}

// writeFileRows writes the rows of a list of files of the Compress page, the ones matching
// the search, at most shownFiles.
func (web *Web) writeFileRows(w http.ResponseWriter, r *http.Request, templates templateSet, all []CompressCandidate) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	data := NszListData{Total: len(all)}
	for _, candidate := range all {
		if query != "" && !strings.Contains(strings.ToLower(candidate.Name+" "+filepath.Base(candidate.Path)), query) {
			continue
		}
		data.Matches++
		if len(data.Files) < shownFiles {
			data.Files = append(data.Files, candidate)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.executeTemplate(w, web.requestLanguage(r), "nszRows", data); err != nil {
		web.sugarLogger.Error(err)
	}
}
