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
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Splitting turns a file that holds several titles (a "SuperNSP", or a complete pack) back
// into one file per title: the game, each update and each DLC, as installers and tools that
// take one title per file expect. Each part is written under a hidden name, checked whole,
// and only then gets its name; the original goes last, if asked, and only when every NCA,
// ticket and certificate it holds is in a part that was checked.

const TASK_SPLIT = "split"

const (
	NOTE_SPLIT_FAILED = "The file could not be split, the original is kept."
	NOTE_SPLIT_EXISTS = "A file with the name of a part already exists."
	NOTE_SPLIT_KEPT   = "Some files of the original are in no part: the original is kept."
	PHASE_SPLITTING   = "Splitting"
	// a part found already written that is not sound as a whole
	NOTE_SPLIT_PART_UNSOUND = "A part already there is not sound as a whole: the original is kept."
)

var errSplitExists = errors.New("a file with the name of a part already exists")

// splitCandidates are the NSP and NSZ files of the library that hold several titles, and the
// game card dumps (XCI, XCZ) that hold several games, as the scan found them, biggest first.
func (web *Web) splitCandidates() []CompressCandidate {
	return web.derived("splitCandidates", func() any {
		switchDB, localDB := web.state.get()
		candidates := []CompressCandidate{}
		if localDB == nil {
			return candidates
		}
		titles := map[string]int{}
		games := map[string]map[*db.SwitchGameFiles]bool{}
		owner := map[string]*db.SwitchGameFiles{}
		sizes := map[string]int64{}
		add := func(local *db.SwitchGameFiles, file db.ExtendedFileInfo) {
			switch strings.ToLower(filepath.Ext(file.FileName)) {
			case ".nsp", ".nsz", ".xci", ".xcz":
			default:
				return
			}
			if file.IsDir {
				return
			}
			path := filepath.Join(file.BaseFolder, file.FileName)
			titles[path]++
			if games[path] == nil {
				games[path] = map[*db.SwitchGameFiles]bool{}
			}
			games[path][local] = true
			sizes[path] = file.Size
			// the name of the game, when the file holds it
			if owner[path] == nil || local.BaseExist && local.File.ExtendedInfo == file {
				owner[path] = local
			}
		}
		for _, local := range localDB.TitlesMap {
			if local.BaseExist {
				add(local, local.File.ExtendedInfo)
			}
			for _, update := range local.Updates {
				add(local, update.ExtendedInfo)
			}
			for _, dlc := range local.Dlc {
				add(local, dlc.ExtendedInfo)
			}
		}
		for path, count := range titles {
			if count < 2 {
				continue
			}
			// a game card with one game and its update is the usual card: it splits by game
			if isCardDump(path) && len(games[path]) < 2 {
				continue
			}
			candidate := CompressCandidate{Path: path, Name: web.gameName(switchDB, owner[path], path), Kind: "Base", Size: sizes[path], Titles: count}
			if isCardDump(path) {
				candidate.Games = len(games[path])
			}
			candidates = append(candidates, candidate)
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

// isCardDump tells whether a file is a game card dump (XCI or XCZ).
func isCardDump(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".xci" || extension == ".xcz"
}

// gameName is the name of a game of the library, or the one in the name of its file.
func (web *Web) gameName(switchDB *db.SwitchTitlesDB, local *db.SwitchGameFiles, path string) string {
	name := ""
	if local != nil && local.File.Metadata != nil {
		var known *db.SwitchTitle
		if prefix, err := db.TitleIDPrefix(local.File.Metadata.TitleId); err == nil && switchDB != nil {
			known = switchDB.TitlesMap[prefix]
		}
		name = getLocalTitleName(known, local)
	}
	if name == "" {
		name = strings.TrimSpace(db.ParseTitleNameFromFileName(filepath.Base(path)))
	}
	return name
}

// splitPartName is the name of the file of a part: the name of the title, its ID and its
// version, like the files of the game shops.
func (web *Web) splitPartName(part switchfs.SplitPart, game string) string {
	switchDB, localDB := web.state.get()
	name := game
	// each game of a game card has its own name
	if part.Cartridge && localDB != nil {
		if prefix, err := db.TitleIDPrefix(part.TitleId); err == nil {
			if own := web.gameName(switchDB, localDB.TitlesMap[prefix], ""); own != "" {
				name = own
			}
		}
	}
	if part.Type == "DLC" && switchDB != nil {
		if prefix, err := db.TitleIDPrefix(part.TitleId); err == nil && switchDB.TitlesMap[prefix] != nil {
			dlcs := switchDB.TitlesMap[prefix].Dlc
			for _, id := range []string{strings.ToUpper(part.TitleId), strings.ToLower(part.TitleId)} {
				if dlc, ok := dlcs[id]; ok && usableName(dlc.Name) {
					name = dlc.Name
					break
				}
			}
		}
	}
	extension := ".nsp"
	switch {
	case part.Cartridge && part.Compressed:
		extension = ".xcz"
	case part.Cartridge:
		extension = ".xci"
	case part.Compressed:
		extension = ".nsz"
	}
	return fmt.Sprintf("%s [%s][v%d]%s", safeFileName(name), strings.ToUpper(part.TitleId), part.Version, extension)
}

// startSplit splits the files in the background, one after the other.
func (web *Web) startSplit(paths []string, deleteOriginals bool) bool {
	return web.startSplitBy(paths, deleteOriginals, TRIGGER_MANUAL)
}

func (web *Web) startSplitBy(paths []string, deleteOriginals bool, trigger string) bool {
	names := map[string]string{}
	for _, candidate := range web.splitCandidates() {
		names[candidate.Path] = candidate.Name
	}
	names = web.moves.resolveKeys(names)
	// every file is copied into its parts, then the parts are checked
	// read once: split, and each part checked while it is written
	return web.runResumableTask(TASK_SPLIT, trigger, paths, 1, &TaskResume{Delete: deleteOriginals}, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		name := names[path]
		if name == "" {
			name = strings.TrimSpace(db.ParseTitleNameFromFileName(filepath.Base(path)))
		}
		return 0, web.splitFile(ctx, path, name, deleteOriginals, report)
	})
}

// splitFile writes the parts of a file next to it. A part already there with the size it
// would have was written before the app stopped: it is kept as it is, and counts for deleting
// the original only once it is found sound.
func (web *Web) splitFile(ctx context.Context, path string, game string, deleteOriginal bool, report func(step int64) func(int64, int64)) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	plan, err := switchfs.PlanSplit(path)
	if err != nil {
		return err
	}
	folder := filepath.Dir(path)
	targets := make([]string, len(plan.Parts))
	var need int64
	for i, part := range plan.Parts {
		targets[i] = filepath.Join(folder, web.splitPartName(part, game))
		if existing, err := os.Stat(targets[i]); err == nil {
			if existing.Size() != part.Size {
				return fmt.Errorf("%w: %s", errSplitExists, filepath.Base(targets[i]))
			}
			continue
		}
		need += part.Size
	}
	release, err := web.reserveSpace(folder, need)
	if err != nil {
		return err
	}
	defer release()

	checked := true
	var done int64
	for i, part := range plan.Parts {
		target := targets[i]
		if existing, err := os.Stat(target); err == nil {
			sound, err := web.soundPart(ctx, target, existing, report(done))
			if err != nil {
				return err
			}
			checked = checked && sound
			done += part.Size
			continue
		}
		temporary := temporaryOf(path, target)
		// left half written when the app stopped
		os.Remove(temporary)
		// every NCA must match its content ID: checked while the part is written
		written, err := switchfs.WriteSplitPartAndCheck(ctx, path, part, temporary, report(done))
		if err == nil {
			checked = checked && fullyChecked(written)
			if err = commitFile(temporary, target); err == nil {
				web.verifications().remember(target, written)
			}
		}
		if err != nil {
			os.Remove(temporary)
			return err
		}
		done += part.Size
	}
	web.sugarLogger.Infof("Split %s into %d files", path, len(plan.Parts))
	if !plan.Complete() {
		web.warnRunningTask(NOTE_SPLIT_KEPT, filepath.Base(path)+": "+strings.Join(plan.Unclaimed, ", "))
		return nil
	}
	if deleteOriginal && checked {
		if err := web.discardOriginal(path, QUARANTINE_SPLIT); err != nil {
			return fmt.Errorf("could not delete the original: %w", err)
		}
	}
	return nil
}

// soundPart tells whether a part found already written is sound as a whole: from its last check
// while it did not change, or checked now. Its size alone does not tell a part cut short by a
// failed disk, or another file of the same name.
func (web *Web) soundPart(ctx context.Context, path string, info os.FileInfo, progress func(int64, int64)) (bool, error) {
	detail := "the last check of the file did not find it sound"
	if record, ok := web.verifications().current(path, info); ok {
		if record.sound() && !record.Modified {
			return true, nil
		}
	} else {
		check, err := verifyLibraryFile(ctx, path, progress)
		if errors.Is(err, context.Canceled) {
			return false, err
		}
		if err == nil {
			web.verifications().remember(path, check)
			if fullyChecked(check) {
				return true, nil
			}
			detail = "it could not be checked whole"
		} else {
			detail = err.Error()
		}
	}
	web.warnRunningTask(NOTE_SPLIT_PART_UNSOUND, filepath.Base(path)+": "+detail)
	return false, nil
}

// warnRunningTask adds a note to the task on files that is running.
func (web *Web) warnRunningTask(text string, detail string) {
	web.compressor.mutex.Lock()
	id := web.compressor.taskId
	web.compressor.mutex.Unlock()
	web.taskLog().Warn(id, text, detail)
}

func (web *Web) handleSplit() {
	web.router.HandleFunc("/split/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		if keys, _ := settings.SwitchKeys(); keys == nil || keys.GetKey("header_key") == "" {
			writeGlobalError(w, http.StatusBadRequest, lang, "prod.keys is needed to split files.")
			return
		}
		paths := web.selectedPaths(r, web.splitCandidates())
		if len(paths) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the files to split.")
			return
		}
		web.startWriting(w, r, QueuedTask{Kind: TASK_SPLIT, Paths: paths, Delete: r.FormValue("delete_originals") == "true"})
	}).Methods("POST")
}

// selectedPaths are the paths of the request among the candidates, each one once; with
// rest=all, every candidate, also those the list does not show.
func (web *Web) selectedPaths(r *http.Request, candidates []CompressCandidate) []string {
	requested := r.Form["path"]
	if r.FormValue("rest") == "all" {
		for _, candidate := range candidates {
			requested = append(requested, candidate.Path)
		}
	}
	return web.libraryPaths(requested, candidates)
}
