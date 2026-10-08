package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A complete pack is one NSP (or NSZ) with the game, its latest update and its DLC, as
// multi-content installers take it: one file to copy and install instead of many. The
// older updates are left out (the latest one replaces them), and so are the delta fragments
// of the update, which installs from a file never use.

const TASK_PACK = "pack"

const (
	NOTE_PACK_FAILED = "The pack could not be made, the files of the game are kept."
	NOTE_PACK_EXISTS = "A pack with the same name already exists."
)

var (
	errPackExists = errors.New("the pack already exists")
	errPackBusy   = errorString("A compression is already running.")
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

// packSources gives the paths of the files of the pack of the game.
func packSources(local *db.SwitchGameFiles) []string {
	sources := []string{}
	for _, file := range packFiles(local) {
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

// startPack makes the pack of a game in the background: the files are merged into a hidden
// temporary file, every NCA of it is checked, and only then it gets its name. The files it
// is made of are deleted last, if asked.
func (web *Web) startPack(titleId string, name string, deleteOriginals bool) error {
	_, localDB := web.state.get()
	prefix, err := db.TitleIDPrefix(titleId)
	if err != nil || localDB == nil || localDB.TitlesMap[prefix] == nil {
		return errorString("The game is not in your library.")
	}
	local := localDB.TitlesMap[prefix]
	sources := packSources(local)
	if len(sources) == 0 {
		return errorString("This game has nothing to put in a pack.")
	}
	var size int64
	for _, source := range sources {
		if info, err := os.Stat(source); err == nil {
			size += info.Size()
		}
	}
	compressed, err := switchfs.PackHasCompressedFiles(sources)
	if err != nil {
		return errorString(NOTE_PACK_FAILED)
	}
	target := filepath.Join(filepath.Dir(sources[0]), packName(name, titleId, local, compressed))

	// the files are read twice: merged, then the pack is checked
	started := web.runFileTaskSized(TASK_PACK, TRIGGER_MANUAL, []string{target}, []int64{size}, 2, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		return web.makePack(ctx, sources, path, size, deleteOriginals, report)
	})
	if !started {
		return errPackBusy
	}
	return nil
}

// makePack writes the pack and gives the bytes of delta fragments left out.
func (web *Web) makePack(ctx context.Context, sources []string, target string, size int64, deleteOriginals bool, report func(step int64) func(int64, int64)) (int64, error) {
	if _, err := os.Stat(target); err == nil {
		return 0, errPackExists
	}
	if free, err := diskFree(filepath.Dir(target)); err == nil && free < uint64(size)+(64<<20) {
		return 0, errCompressSpace
	}
	temporary := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".tmp")
	defer os.Remove(temporary)
	skipped, err := switchfs.MergePackages(ctx, sources, temporary, true, report(0))
	if err != nil {
		return 0, err
	}
	// every NCA must match its content ID
	if err := switchfs.VerifyCompressed(ctx, temporary, nil, report(size)); err != nil {
		return 0, err
	}
	if err := os.Rename(temporary, target); err != nil {
		return 0, err
	}
	web.sugarLogger.Infof("Made the pack %s of %d files", target, len(sources))
	if deleteOriginals {
		for _, source := range sources {
			if err := os.Remove(source); err != nil {
				return skipped, fmt.Errorf("could not delete the original: %w", err)
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
		if err := web.startPack(detail.Id, detail.Name, choice == "true"); err != nil {
			status := http.StatusBadRequest
			if err == errPackBusy {
				status = http.StatusConflict
			}
			writeGlobalError(w, status, lang, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
	}).Methods("POST")
}
