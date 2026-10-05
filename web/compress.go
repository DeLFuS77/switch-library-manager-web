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
)

var compressNoteTexts = []string{NOTE_COMPRESS_FAILED, NOTE_COMPRESS_DAMAGED, NOTE_COMPRESS_SPACE, NOTE_COMPRESS_EXISTS, NOTE_COMPRESS_NOTHING, NOTE_COMPRESS_CANCELED, NOTE_COMPRESS_DELETE}

// CompressCandidate is an NSP of the library that can be compressed.
type CompressCandidate struct {
	Path string
	Name string
	// Base, Update or DLC
	Kind string
	Size int64
}

type CompressPageData struct {
	GlobalPageData
	Candidates []CompressCandidate
	TotalSize  int64
	Running    bool
}

// compressor runs one compression at a time and can cancel it.
type compressor struct {
	mutex  sync.Mutex
	cancel context.CancelFunc
}

// compressCandidates lists the NSP files of the library, biggest first.
func (web *Web) compressCandidates() []CompressCandidate {
	switchDB, localDB := web.state.get()
	candidates := []CompressCandidate{}
	if localDB == nil {
		return candidates
	}
	add := func(title *db.SwitchGameFiles, file db.SwitchFileInfo, kind string) {
		if !strings.EqualFold(filepath.Ext(file.ExtendedInfo.FileName), ".nsp") {
			return
		}
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
		candidates = append(candidates, CompressCandidate{
			Path: filepath.Join(file.ExtendedInfo.BaseFolder, file.ExtendedInfo.FileName),
			Name: name,
			Kind: kind,
			Size: file.ExtendedInfo.Size,
		})
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

// startCompression compresses the files in the background. It returns false if a
// compression is already running.
func (web *Web) startCompression(paths []string, level string, deleteOriginals bool) bool {
	web.compressor.mutex.Lock()
	if web.compressor.cancel != nil {
		web.compressor.mutex.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	web.compressor.cancel = cancel
	web.compressor.mutex.Unlock()

	taskId := web.taskLog().Start(TASK_COMPRESS, TRIGGER_MANUAL)
	go func() {
		defer func() {
			web.compressor.mutex.Lock()
			web.compressor.cancel = nil
			web.compressor.mutex.Unlock()
			cancel()
		}()

		var total, done int64
		for _, path := range paths {
			if info, err := os.Stat(path); err == nil {
				total += info.Size()
			}
		}
		// every file is read three times: hashed, compressed and verified
		total *= 3

		compressed := 0
		var saved int64
		var failure *TaskNote
		for i, path := range paths {
			if ctx.Err() != nil {
				failure = &TaskNote{Text: NOTE_COMPRESS_CANCELED}
				break
			}
			name := filepath.Base(path)
			base := done
			report := func(step int64) func(int64, int64) {
				return func(fileDone int64, fileTotal int64) {
					web.taskLog().Progress(taskId, int((base+step+fileDone)>>20), int(total>>20), fmt.Sprintf("%s (%d/%d)", name, i+1, len(paths)))
				}
			}
			size, err := web.compressFile(ctx, path, level, deleteOriginals, report)
			if info, statErr := os.Stat(path); statErr == nil {
				done += info.Size() * 3
			} else {
				done += size * 3
			}
			if err != nil {
				if errors.Is(err, context.Canceled) {
					failure = &TaskNote{Text: NOTE_COMPRESS_CANCELED}
					break
				}
				web.sugarLogger.Warnf("Compressing %s failed: %v", path, err)
				web.taskLog().Warn(taskId, compressNote(err), name+": "+err.Error())
				continue
			}
			compressed++
			saved += size
			web.taskLog().SetCompressResult(taskId, compressed, saved)
		}
		if failure == nil && compressed == 0 && len(paths) > 0 {
			failure = &TaskNote{Text: NOTE_COMPRESS_FAILED}
		}
		web.taskLog().Finish(taskId, failure)
		if compressed > 0 {
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

func compressNote(err error) string {
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
	return NOTE_COMPRESS_FAILED
}

// compressFile compresses one NSP. The NSZ is written next to it under a hidden
// temporary name, verified, and only then renamed; the original is deleted last, if
// asked. It returns the bytes saved.
func (web *Web) compressFile(ctx context.Context, path string, level string, deleteOriginal bool, report func(step int64) func(int64, int64)) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	target := switchfs.NszPath(path)
	if _, err := os.Stat(target); err == nil {
		return 0, errCompressExists
	}
	if free, err := diskFree(filepath.Dir(path)); err == nil && free < uint64(info.Size())+(64<<20) {
		return 0, errCompressSpace
	}

	expected, err := switchfs.HashNcas(ctx, path, report(0))
	if err != nil {
		return 0, err
	}

	// hidden, so scans ignore the file while it is written
	temporary := filepath.Join(filepath.Dir(path), "."+filepath.Base(target)+".tmp")
	defer os.Remove(temporary)
	result, err := switchfs.CompressNsp(ctx, path, temporary, switchfs.CompressOptions{
		Level: level, Workers: compressWorkers(), Progress: report(info.Size()),
	})
	if err != nil {
		return 0, err
	}
	if err := switchfs.VerifyNsz(ctx, temporary, expected, report(2*info.Size())); err != nil {
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

func (web *Web) cancelCompression() bool {
	web.compressor.mutex.Lock()
	defer web.compressor.mutex.Unlock()
	if web.compressor.cancel == nil {
		return false
	}
	web.compressor.cancel()
	return true
}

func (web *Web) compressionRunning() bool {
	web.compressor.mutex.Lock()
	defer web.compressor.mutex.Unlock()
	return web.compressor.cancel != nil
}

func (web *Web) HandleCompress() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/compress.html")

	web.router.HandleFunc("/compress.html", func(w http.ResponseWriter, r *http.Request) {
		candidates := web.compressCandidates()
		data := CompressPageData{GlobalPageData: web.globalPageData("compress"), Candidates: candidates, Running: web.compressionRunning()}
		for _, candidate := range candidates {
			data.TotalSize += candidate.Size
		}
		web.render(w, r, templates, data)
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
		for _, candidate := range web.compressCandidates() {
			known[candidate.Path] = true
		}
		paths := []string{}
		for _, path := range r.Form["path"] {
			if known[path] {
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

	web.router.HandleFunc("/compress/cancel", func(w http.ResponseWriter, r *http.Request) {
		web.cancelCompression()
		w.WriteHeader(http.StatusNoContent)
	}).Methods("POST")
}
