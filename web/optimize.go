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

// mergedUpdate is the version of the update a game merged with it holds (optimized for install, or
// merged by another tool): its base file is of that version, and no update file of the library is
// the same file (a NUP or a SuperNSP holds the game and its update apart). 0 for any other game.
func mergedUpdate(local *db.SwitchGameFiles) int {
	if local == nil || !local.BaseExist || local.File.Metadata == nil || local.File.Metadata.Version <= 0 {
		return 0
	}
	for _, update := range local.Updates {
		if update.ExtendedInfo == local.File.ExtendedInfo {
			return 0
		}
	}
	return local.File.Metadata.Version
}

// optimizeFiles gives the base file of the game as it was released (version 0) and the file of
// its newest update, when both are single files. The base is the one of the game, or, once the game
// was optimized (or merged) before, the original kept beside it as a base of another version: a
// newer update is optimized again from it.
func (web *Web) optimizeFiles(local *db.SwitchGameFiles) []db.ExtendedFileInfo {
	base, ok := web.originalBase(local)
	if !ok {
		return nil
	}
	return optimizeFilesFrom(local, base)
}

// originalBase is the base file of version 0 of the game: its own, or a copy of another version
// the library keeps beside it.
func (web *Web) originalBase(local *db.SwitchGameFiles) (db.ExtendedFileInfo, bool) {
	if local == nil || !local.BaseExist || local.File.Metadata == nil {
		return db.ExtendedFileInfo{}, false
	}
	if local.File.Metadata.Version == 0 {
		return local.File.ExtendedInfo, true
	}
	_, localDB := web.state.get()
	if localDB == nil {
		return db.ExtendedFileInfo{}, false
	}
	held := filepath.Join(local.File.ExtendedInfo.BaseFolder, local.File.ExtendedInfo.FileName)
	id := strings.ToUpper(local.File.Metadata.TitleId)
	found := []db.ExtendedFileInfo{}
	for file, skipped := range localDB.Skipped {
		if !skipped.OtherVersion || !samePath(db.KeptBy(skipped.ReasonText), held) || file.IsDir {
			continue
		}
		titles, ok := web.heldTitles(filepath.Join(file.BaseFolder, file.FileName))
		if ok && len(titles) == 1 && strings.EqualFold(titles[0].id, id) && titles[0].version == 0 {
			found = append(found, file)
		}
	}
	if len(found) == 0 {
		return db.ExtendedFileInfo{}, false
	}
	// several copies of the original: the one of the first path, always the same
	sort.Slice(found, func(i, j int) bool {
		return filepath.Join(found[i].BaseFolder, found[i].FileName) < filepath.Join(found[j].BaseFolder, found[j].FileName)
	})
	return found[0], true
}

func samePath(a string, b string) bool {
	return a != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// optimizeFilesFrom gives the base file given and the file of the newest update of the game, when
// both are single game files and not one SuperNSP.
func optimizeFilesFrom(local *db.SwitchGameFiles, base db.ExtendedFileInfo) []db.ExtendedFileInfo {
	update, ok := local.Updates[local.LatestUpdate]
	if !ok || local.LatestUpdate <= 0 {
		return nil
	}
	files := []db.ExtendedFileInfo{base, update.ExtendedInfo}
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

func (web *Web) optimizeSources(local *db.SwitchGameFiles) []string {
	sources := []string{}
	for _, file := range web.optimizeFiles(local) {
		sources = append(sources, filepath.Join(file.BaseFolder, file.FileName))
	}
	if len(sources) == 0 {
		return nil
	}
	return sources
}

// optimizeGame is the game of the library whose original base file is at path: the base of the
// game, or a copy of another version the library keeps beside it.
func (web *Web) optimizeGame(path string) (*db.SwitchGameFiles, string) {
	if local, id := web.packGame(path); local != nil {
		return local, id
	}
	_, localDB := web.state.get()
	if localDB == nil {
		return nil, ""
	}
	path = web.moves.resolve(path)
	for file, skipped := range localDB.Skipped {
		if skipped.OtherVersion && samePath(web.moves.resolve(filepath.Join(file.BaseFolder, file.FileName)), path) {
			return web.packGame(db.KeptBy(skipped.ReasonText))
		}
	}
	return nil, ""
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
			files := web.optimizeFiles(local)
			if files == nil {
				continue
			}
			base := filepath.Join(files[0].BaseFolder, files[0].FileName)
			name := web.gameName(switchDB, local, filepath.Join(local.File.ExtendedInfo.BaseFolder, local.File.ExtendedInfo.FileName))
			if scanned[filepath.Clean(filepath.Join(files[0].BaseFolder, optimizeName(name, local.File.Metadata.TitleId, local)))] {
				continue
			}
			var size int64
			for _, file := range files {
				size += file.Size
			}
			candidates = append(candidates, CompressCandidate{Path: base, Name: name, Kind: "Base", Size: size, Files: len(files), Contents: "1G+1U → v" + strconv.Itoa(local.LatestUpdate), Latest: local.LatestUpdate})
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
	local, titleId := web.optimizeGame(path)
	if local == nil {
		return "", nil, 0, errorString("The game is not in your library.")
	}
	sources = web.moves.resolveAll(web.optimizeSources(local))
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
	sources := web.optimizeSources(localDB.TitlesMap[prefix])
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
