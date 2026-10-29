package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A complete pack is one NSP (or NSZ) with the game, its latest update and its DLC, as
// multi-content installers take it: one file to copy and install instead of many. The
// older updates are left out (the latest one replaces them), and so are the delta fragments
// of the update, which installs from a file never use.

const TASK_PACK = "pack"

const (
	NOTE_PACK_FAILED = "The game could not be combined, its files are kept."
	NOTE_PACK_EXISTS = "A combined file with the same name already exists."
)

var (
	errPackExists = errors.New("the pack already exists")
)

// packFiles gives the files a pack of the game is made of: the base game, its latest
// update and its DLC, each file once. A game with only one file, or with files split in a
// folder, has no pack.
func packFiles(local *db.SwitchGameFiles) []db.ExtendedFileInfo {
	if local == nil || !local.BaseExist {
		return nil
	}
	files := []db.ExtendedFileInfo{local.File.ExtendedInfo}
	if update, ok := local.Updates[local.LatestUpdate]; ok {
		files = append(files, update.ExtendedInfo)
	}
	for _, entry := range archiveEntries(local) {
		if strings.HasPrefix(entry.folder, "DLC") {
			files = append(files, entry.file)
		}
	}
	result := []db.ExtendedFileInfo{}
	seen := map[string]bool{}
	for _, file := range files {
		if file.IsDir {
			return nil
		}
		switch strings.ToLower(filepath.Ext(file.FileName)) {
		case ".nsp", ".nsz", ".xci", ".xcz":
		default:
			return nil
		}
		path := filepath.Join(file.BaseFolder, file.FileName)
		if !seen[path] {
			seen[path] = true
			result = append(result, file)
		}
	}
	if len(result) < 2 {
		return nil
	}
	return result
}

// withCompressedCopies swaps each NSP or XCI of a pack for its compressed copy, the NSZ or XCZ
// of the same name the library holds as its duplicate: the copy was checked when it was made,
// and the pack is compressed too, half the size, instead of all the separate NSP added up.
func withCompressedCopies(files []db.ExtendedFileInfo, skipped map[db.ExtendedFileInfo]db.SkippedFile) []db.ExtendedFileInfo {
	if files == nil {
		return nil
	}
	copies := map[string]db.ExtendedFileInfo{}
	for file, reason := range skipped {
		if reason.ReasonCode == db.REASON_DUPLICATE && !file.IsDir {
			copies[filepath.Clean(filepath.Join(file.BaseFolder, file.FileName))] = file
		}
	}
	result := make([]db.ExtendedFileInfo, 0, len(files))
	for _, file := range files {
		path := filepath.Join(file.BaseFolder, file.FileName)
		if compressed, ok := copies[filepath.Clean(switchfs.CompressedPath(path))]; ok && !strings.EqualFold(filepath.Ext(path), filepath.Ext(compressed.FileName)) {
			file = compressed
		}
		result = append(result, file)
	}
	return result
}

// packSources gives the paths of the files of the pack of the game, the compressed copies when
// the library holds them (see withCompressedCopies).
func packSources(local *db.SwitchGameFiles, skipped map[db.ExtendedFileInfo]db.SkippedFile) []string {
	sources := []string{}
	for _, file := range withCompressedCopies(packFiles(local), skipped) {
		sources = append(sources, filepath.Join(file.BaseFolder, file.FileName))
	}
	if len(sources) == 0 {
		return nil
	}
	return sources
}

// packName is the name of the pack of a game, next to its base file.
func packName(name string, titleId string, local *db.SwitchGameFiles, compressed bool) string {
	result := safeFileName(name) + " [" + strings.ToUpper(titleId) + "]"
	if local.LatestUpdate > 0 {
		result += fmt.Sprintf("[v%d]", local.LatestUpdate)
	}
	if len(local.Dlc) > 0 {
		result += fmt.Sprintf("[+%d DLC]", len(local.Dlc))
	}
	result += " (pack)"
	if compressed {
		return result + ".nsz"
	}
	return result + ".nsp"
}

// packContents tells what the pack of a game holds, as Switch Library Manager writes it: the
// game, its latest update and its DLC (1G+1U+2D).
func packContents(local *db.SwitchGameFiles) string {
	contents := "1G"
	if _, ok := local.Updates[local.LatestUpdate]; ok {
		contents += "+1U"
	}
	if dlc := len(local.Dlc); dlc > 0 {
		contents += fmt.Sprintf("+%dD", dlc)
	}
	return contents
}

// dlcNeedsNewer is the version of its game a DLC of the game needs when the newest update of
// the library is older, 0 else: the DLC of the pack would ask for an update it does not hold.
func dlcNeedsNewer(local *db.SwitchGameFiles) int {
	need := 0
	for _, dlc := range local.Dlc {
		if dlc.Metadata != nil && dlc.Metadata.RequiredTitleVersion > local.LatestUpdate && dlc.Metadata.RequiredTitleVersion > need {
			need = dlc.Metadata.RequiredTitleVersion
		}
	}
	return need
}

// packedGames are the games of the library whose pack is made already, by name, with the name
// of their pack.
func (web *Web) packedGames() []CompressCandidate {
	return web.derived("packedGames", func() any {
		switchDB, localDB := web.state.get()
		games := []CompressCandidate{}
		if localDB == nil {
			return games
		}
		scanned := web.scannedPaths()
		for _, local := range localDB.TitlesMap {
			files := withCompressedCopies(packFiles(local), localDB.Skipped)
			if files == nil || local.File.Metadata == nil {
				continue
			}
			base := filepath.Join(files[0].BaseFolder, files[0].FileName)
			name := web.gameName(switchDB, local, base)
			for _, compressed := range []bool{false, true} {
				pack := packName(name, local.File.Metadata.TitleId, local, compressed)
				if scanned[filepath.Clean(filepath.Join(files[0].BaseFolder, pack))] {
					games = append(games, CompressCandidate{Path: base, Name: name, Kind: "Base", Files: len(files), Contents: packContents(local), Packed: pack})
					break
				}
			}
		}
		sort.Slice(games, func(i, j int) bool { return strings.ToLower(games[i].Name) < strings.ToLower(games[j].Name) })
		return games
	}).([]CompressCandidate)
}

// packCandidates are the games of the library with a pack to make and no pack yet, biggest
// first: the path of a candidate is the one of the base file of the game.
func (web *Web) packCandidates() []CompressCandidate {
	return web.derived("packCandidates", func() any {
		switchDB, localDB := web.state.get()
		candidates := []CompressCandidate{}
		if localDB == nil {
			return candidates
		}
		scanned := web.scannedPaths()
		for _, local := range localDB.TitlesMap {
			files := withCompressedCopies(packFiles(local), localDB.Skipped)
			if files == nil || local.File.Metadata == nil {
				continue
			}
			base := filepath.Join(files[0].BaseFolder, files[0].FileName)
			name := web.gameName(switchDB, local, base)
			made := false
			for _, compressed := range []bool{false, true} {
				if scanned[filepath.Clean(filepath.Join(files[0].BaseFolder, packName(name, local.File.Metadata.TitleId, local, compressed)))] {
					made = true
				}
			}
			if made {
				continue
			}
			var size int64
			for _, file := range files {
				size += file.Size
			}
			candidates = append(candidates, CompressCandidate{Path: base, Name: name, Kind: "Base", Size: size, Files: len(files), Contents: packContents(local), NeedsGame: dlcNeedsNewer(local), Latest: local.LatestUpdate})
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

// skippedFiles are the files the library left aside (duplicates, old updates), or nil.
func (web *Web) skippedFiles() map[db.ExtendedFileInfo]db.SkippedFile {
	if _, localDB := web.state.get(); localDB != nil {
		return localDB.Skipped
	}
	return nil
}

// packGame is the game of the library whose base file is at path, with its title ID.
func (web *Web) packGame(path string) (*db.SwitchGameFiles, string) {
	_, localDB := web.state.get()
	if localDB == nil {
		return nil, ""
	}
	// the library may still have the paths from before Organize moved the files, and path the
	// new ones, or the other way round: both where the files are now
	path = web.moves.resolve(path)
	// the game compressed (or decompressed, converted) since, its original kept: the library may
	// have the copy as the game now
	for _, wanted := range []string{path, switchfs.CompressedPath(path), switchfs.DecompressedPath(path), switchfs.ConvertedPath(path)} {
		for _, local := range localDB.TitlesMap {
			if !local.BaseExist || local.File.Metadata == nil {
				continue
			}
			if web.moves.resolve(filepath.Join(local.File.ExtendedInfo.BaseFolder, local.File.ExtendedInfo.FileName)) == wanted {
				return local, local.File.Metadata.TitleId
			}
		}
	}
	return nil, ""
}

// packTarget is where the pack of the game whose base file is at path goes, with the files
// it is made of and their size.
func (web *Web) packTarget(path string, name string) (target string, sources []string, size int64, err error) {
	local, titleId := web.packGame(path)
	if local == nil {
		return "", nil, 0, errorString("The game is not in your library.")
	}
	sources = web.moves.resolveAll(packSources(local, web.skippedFiles()))
	if len(sources) == 0 {
		return "", nil, 0, errorString("This game has nothing to combine.")
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
	compressed, err := switchfs.PackHasCompressedFiles(sources)
	if err != nil {
		return "", nil, 0, errorString(NOTE_PACK_FAILED)
	}
	return filepath.Join(filepath.Dir(sources[0]), packName(name, titleId, local, compressed)), sources, size, nil
}

// packTask is the pack of a game to make in the background, from its page: the files are merged
// into a hidden temporary file, every NCA of it is checked, and only then it gets its name. The
// files it is made of are deleted last, if asked.
func (web *Web) packTask(titleId string, name string, deleteOriginals bool) (QueuedTask, error) {
	_, localDB := web.state.get()
	prefix, err := db.TitleIDPrefix(titleId)
	if err != nil || localDB == nil || localDB.TitlesMap[prefix] == nil {
		return QueuedTask{}, errorString("The game is not in your library.")
	}
	sources := packSources(localDB.TitlesMap[prefix], localDB.Skipped)
	if len(sources) == 0 {
		return QueuedTask{}, errorString("This game has nothing to combine.")
	}
	return QueuedTask{Kind: TASK_PACK, Paths: []string{sources[0]}, Names: map[string]string{sources[0]: name}, Delete: deleteOriginals}, nil
}

// startPacks makes the packs of the games whose base files are at paths, one after the other,
// as one task that goes on after a restart. names gives the name of a pack, when known.
func (web *Web) startPacks(paths []string, names map[string]string, deleteOriginals bool, trigger string) bool {
	// a pack reads every file of its game
	sizes := make([]int64, len(paths))
	for i, path := range paths {
		if local, _ := web.packGame(path); local != nil {
			for _, source := range web.moves.resolveAll(packSources(local, web.skippedFiles())) {
				if info, err := os.Stat(source); err == nil {
					sizes[i] += info.Size()
				}
			}
		}
	}
	names = web.moves.resolveKeys(names)
	// the files are read once: merged, and checked while the pack is written
	return web.runFileTaskFull(TASK_PACK, trigger, paths, sizes, 1, &TaskResume{Delete: deleteOriginals}, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		target, sources, size, err := web.packTarget(path, names[path])
		if err != nil {
			return 0, err
		}
		return web.makePack(ctx, sources, target, size, deleteOriginals, report)
	})
}

// makePack writes the pack and gives the bytes of delta fragments left out.
func (web *Web) makePack(ctx context.Context, sources []string, target string, size int64, deleteOriginals bool, report func(step int64) func(int64, int64)) (int64, error) {
	if _, err := os.Stat(target); err == nil {
		return 0, errPackExists
	}
	if free, err := diskFree(filepath.Dir(target)); err == nil && free < uint64(size)+(64<<20) {
		if !web.makeRoom(filepath.Dir(target), size+(64<<20)) {
			return 0, errCompressSpace
		}
		if free, err := diskFree(filepath.Dir(target)); err == nil && free < uint64(size)+(64<<20) {
			return 0, errCompressSpace
		}
	}
	temporary := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".tmp")
	defer os.Remove(temporary)
	// every NCA must match its content ID: checked while the pack is written, its NCZ files
	// decompressed from the disk
	// the bar of the pack is the size of its sources, the delta fragments left out included
	var read int64
	for _, source := range sources {
		if info, err := os.Stat(source); err == nil {
			read += info.Size()
		}
	}
	skipped, written, err := switchfs.MergeAndCheck(ctx, sources, temporary, true, filling(read, report(0)))
	if err != nil {
		return 0, err
	}
	if err := commitFile(temporary, target); err != nil {
		return 0, err
	}
	web.verifications().remember(target, written)
	web.sugarLogger.Infof("Made the pack %s of %d files", target, len(sources))
	if deleteOriginals && fullyChecked(written) {
		folders := []string{}
		for _, source := range sources {
			if err := web.discardOriginal(source, QUARANTINE_PACK); err != nil {
				return skipped, fmt.Errorf("could not delete the original: %w", err)
			}
			folders = append(folders, filepath.Dir(source))
		}
		// the folders of the update and the DLC left empty go too, when Organize is set so
		appSettings := settings.ReadSettings(web.dataFolder)
		if appSettings.OrganizeOptions.DeleteEmptyFolders {
			if removed := process.RemoveEmptyFolders(folders, scanFolders(appSettings)); len(removed) > 0 {
				web.sugarLogger.Infof("Deleted %d empty folders left by the pack %s", len(removed), target)
			}
		}
	}
	return skipped, nil
}

func (web *Web) handlePack() {
	web.router.HandleFunc("/title/pack", func(w http.ResponseWriter, r *http.Request) {
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
		// the user always says whether the separate files are kept or deleted
		choice := r.FormValue("delete_originals")
		if choice != "true" && choice != "false" {
			writeGlobalError(w, http.StatusBadRequest, lang, "Choose whether to keep or delete the separate files.")
			return
		}
		task, err := web.packTask(detail.Id, detail.Name, choice == "true")
		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, err.Error())
			return
		}
		web.startOrQueue(w, task)
	}).Methods("POST")
}

func (web *Web) handlePacks() {
	web.router.HandleFunc("/packs/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		paths := web.selectedPaths(r, web.packCandidates())
		if len(paths) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the games to combine.")
			return
		}
		web.startWriting(w, r, QueuedTask{Kind: TASK_PACK, Paths: paths, Delete: r.FormValue("delete_originals") == "true"})
	}).Methods("POST")
}
