package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/diskfree"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"github.com/dtrunk90/switch-library-manager-web/switchfs/optimize"
)

// Optimize for install (experimental, as in NXPress): a game and its newest update made one NSP
// that takes the least room once installed (switchfs/optimize). It is written next to the base
// file of the game; the game and its update are always kept, as a newer update is optimized
// again from them.

const (
	OPTIMIZE_ACTION         = "Optimize for install"
	NOTE_OPTIMIZE_FAILED    = "The game could not be optimized, its files are kept."
	NOTE_OPTIMIZE_EXISTS    = "An optimized file with the same name already exists."
	NOTE_OPTIMIZE_NO_PAIR   = "This game and its update cannot be optimized together."
	NOTE_OPTIMIZE_NOT_BUILT = "This build of the app cannot optimize games."
)

var errOptimizeExists = errors.New("the optimized file already exists")

// optimizeFiles gives the base file of the game and the file of its newest update, when the game
// has both as single files: a base that holds an update already (a merged or optimized game) has
// none to merge.
func optimizeFiles(local *db.SwitchGameFiles) []db.ExtendedFileInfo {
	if local == nil || !local.BaseExist || local.File.Metadata == nil || local.File.Metadata.Version > 0 {
		return nil
	}
	update, ok := local.Updates[local.LatestUpdate]
	if !ok || local.LatestUpdate <= 0 {
		return nil
	}
	files := []db.ExtendedFileInfo{local.File.ExtendedInfo, update.ExtendedInfo}
	for _, file := range files {
		if file.IsDir {
			return nil
		}
		switch strings.ToLower(filepath.Ext(file.FileName)) {
		case ".nsp", ".nsz", ".xci", ".xcz":
		default:
			return nil
		}
	}
	if filepath.Join(files[0].BaseFolder, files[0].FileName) == filepath.Join(files[1].BaseFolder, files[1].FileName) {
		return nil // a SuperNSP: its game and update are in one file, given once
	}
	return files
}

func optimizeSources(local *db.SwitchGameFiles) []string {
	sources := []string{}
	for _, file := range optimizeFiles(local) {
		sources = append(sources, filepath.Join(file.BaseFolder, file.FileName))
	}
	if len(sources) == 0 {
		return nil
	}
	return sources
}

// optimizeName is the name of the optimized file of a game, next to its base file: the game at
// the version of its update, as NXPress names it.
func optimizeName(name string, titleId string, local *db.SwitchGameFiles) string {
	return safeFileName(name) + " [" + strings.ToUpper(titleId) + "][v" + strconv.Itoa(local.LatestUpdate) + "] (optimized).nsp"
}

// optimizeCandidates are the games of the library with a game and an update to optimize and no
// optimized file yet, biggest first: the path of a candidate is the one of its base file.
func (web *Web) optimizeCandidates() []CompressCandidate {
	return web.derived("optimizeCandidates", func() any {
		switchDB, localDB := web.state.get()
		candidates := []CompressCandidate{}
		if localDB == nil || !optimize.Built {
			return candidates
		}
		scanned := web.scannedPaths()
		for _, local := range localDB.TitlesMap {
			files := optimizeFiles(local)
			if files == nil {
				continue
			}
			base := filepath.Join(files[0].BaseFolder, files[0].FileName)
			name := web.gameName(switchDB, local, base)
			if scanned[filepath.Clean(filepath.Join(files[0].BaseFolder, optimizeName(name, local.File.Metadata.TitleId, local)))] {
				continue
			}
			var size int64
			for _, file := range files {
				size += file.Size
			}
			candidates = append(candidates, CompressCandidate{Path: base, Name: name, Kind: "Base", Size: size, Files: len(files), Latest: local.LatestUpdate})
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].Size == candidates[j].Size {
				return candidates[i].Path < candidates[j].Path
			}
			return candidates[i].Size > candidates[j].Size
		})
		return candidates
	}).([]CompressCandidate)
}

// optimizeTarget is where the optimized file of the game whose base file is at path goes, with
// the files it is made of and their size.
func (web *Web) optimizeTarget(path string, name string) (target string, sources []string, size int64, err error) {
	local, titleId := web.packGame(path)
	if local == nil {
		return "", nil, 0, errorString("The game is not in your library.")
	}
	sources = web.moves.resolveAll(optimizeSources(local))
	if len(sources) == 0 {
		return "", nil, 0, errorString(NOTE_OPTIMIZE_NO_PAIR)
	}
	for _, source := range sources {
		if info, err := os.Stat(source); err == nil {
			size += info.Size()
		}
	}
	if name == "" {
		switchDB, _ := web.state.get()
		name = web.gameName(switchDB, local, path)
	}
	return filepath.Join(filepath.Dir(sources[0]), optimizeName(name, titleId, local)), sources, size, nil
}

// optimizeTask is the optimization of a game to do in the background, from its page.
func (web *Web) optimizeTask(titleId string, name string) (QueuedTask, error) {
	if !optimize.Built {
		return QueuedTask{}, errorString(NOTE_OPTIMIZE_NOT_BUILT)
	}
	_, localDB := web.state.get()
	prefix, err := db.TitleIDPrefix(titleId)
	if err != nil || localDB == nil || localDB.TitlesMap[prefix] == nil {
		return QueuedTask{}, errorString("The game is not in your library.")
	}
	sources := optimizeSources(localDB.TitlesMap[prefix])
	if len(sources) == 0 {
		return QueuedTask{}, errorString(NOTE_OPTIMIZE_NO_PAIR)
	}
	return QueuedTask{Kind: tasklog.TASK_OPTIMIZE, Paths: []string{sources[0]}, Names: map[string]string{sources[0]: name}}, nil
}

// startOptimize optimizes the games whose base files are at paths, one after the other, as one
// task that goes on after a restart. names gives the name of a game, when known.
func (web *Web) startOptimize(paths []string, names map[string]string, trigger string) bool {
	sizes := make([]int64, len(paths))
	for i, path := range paths {
		if _, _, size, err := web.optimizeTarget(path, ""); err == nil {
			sizes[i] = size
		}
	}
	names = web.moves.resolveKeys(names)
	return web.runFileTaskFull(tasklog.TASK_OPTIMIZE, trigger, paths, sizes, 1, &tasklog.Resume{}, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		target, sources, size, err := web.optimizeTarget(path, names[path])
		if err != nil {
			return 0, err
		}
		return 0, web.makeOptimized(ctx, sources, target, size, report)
	})
}

// the steps of an optimization, in their order, each a share of the bar of the game
var optimizeSteps = []string{"Decompressing", "Reading", "Measuring", "Writing", "Checking", "Verifying"}

// makeOptimized writes the optimized NSP under a hidden name, checked whole, and only then gives
// it its name. The game and its update stay.
func (web *Web) makeOptimized(ctx context.Context, sources []string, target string, size int64, report func(step int64) func(int64, int64)) error {
	if _, err := os.Stat(target); err == nil {
		return errOptimizeExists
	}
	// room for the NSP made of an NSZ or XCZ given, and for the new file, at most their size
	if free, err := diskfree.Free(filepath.Dir(target)); err == nil && free < uint64(2*size)+(64<<20) {
		if !web.makeRoom(filepath.Dir(target), 2*size+(64<<20)) {
			return errCompressSpace
		}
	}
	temporary := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".tmp")
	defer os.Remove(temporary)
	bar := filling(size, report(0))
	threads := settings.ReadSettings(web.dataFolder).CompressThreads
	_, err := optimize.Run(ctx, sources, temporary, optimize.Options{
		Threads: threads,
		Work:    filepath.Dir(target),
		Progress: func(step string, done int64, total int64) {
			at := 0
			for i, s := range optimizeSteps {
				if s == step {
					at = i
				}
			}
			share := float64(at) / float64(len(optimizeSteps))
			if total > 0 {
				share += float64(done) / float64(total) / float64(len(optimizeSteps))
			}
			bar(int64(share*float64(size)), size)
		},
	})
	if err != nil {
		return err
	}
	if err := durable.CommitFile(temporary, target); err != nil {
		return err
	}
	web.verifications().remember(target, switchfs.FileCheck{})
	web.sugarLogger.Infof("Optimized for install %s from %d files", target, len(sources))
	return nil
}

// optimizeNote is the note of an optimization that failed.
func optimizeNote(err error) string {
	var notPair optimize.ErrNotPair
	switch {
	case errors.Is(err, errCompressSpace):
		return NOTE_COMPRESS_SPACE
	case errors.Is(err, errOptimizeExists):
		return NOTE_OPTIMIZE_EXISTS
	case errors.Is(err, optimize.ErrNotBuilt):
		return NOTE_OPTIMIZE_NOT_BUILT
	case errors.As(err, &notPair):
		return NOTE_OPTIMIZE_NO_PAIR
	}
	return NOTE_OPTIMIZE_FAILED
}

func (web *Web) handleOptimize() {
	web.router.HandleFunc("/title/optimize", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		if !titleIdPattern.MatchString(id) {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid Title ID (16 hexadecimal characters)")
			return
		}
		detail, ok := web.getTitleDetail(id, lang)
		if !ok {
			writeGlobalError(w, http.StatusBadRequest, lang, "The game is not in your library.")
			return
		}
		task, err := web.optimizeTask(detail.Id, detail.Name)
		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, err.Error())
			return
		}
		web.startOrQueue(w, task)
	}).Methods("POST")
	web.router.HandleFunc("/optimize/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		paths := web.selectedPaths(r, web.optimizeCandidates())
		if len(paths) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the games to optimize.")
			return
		}
		web.startWriting(w, r, QueuedTask{Kind: tasklog.TASK_OPTIMIZE, Paths: paths})
	}).Methods("POST")
}
