package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Recompress writes the NSZ files of the library again, in blocks (NCZBLOCK), which emulators
// such as Eden run as they are, or in one piece (solid), the smallest for installers, as
// Recompress of NXPress does (#193). The data each NCZ holds is decompressed and compressed
// again: no keys are needed and no NSP is written in between (switchfs.RecompressGame). The new
// copy is checked whole before it takes the place of the old one, which goes to the quarantine
// when Settings keeps originals there. XCZ files are always in blocks, and the files made with
// the extra compression of NXPress (NCZSECT2 and NCZSECT3) are left out: no emulator opens them.

// notes of a recompression
const (
	NOTE_RECOMPRESS_FAILED  = "The file could not be written again, the NSZ is kept as it was."
	NOTE_RECOMPRESS_PARTIAL = "The new copy could not be checked whole, so the NSZ is kept as it was."
	NOTE_RECOMPRESS_EXTRA   = "Made with the extra compression of NXPress: decompress it with NXPress first."
)

var recompressNoteTexts = []string{NOTE_RECOMPRESS_FAILED, NOTE_RECOMPRESS_PARTIAL, NOTE_RECOMPRESS_EXTRA}

var (
	errRecompressPartial = errors.New("the new copy could not be checked whole")
	errRecompressExtra   = errors.New("made with the extra compression of NXPress")
)

// recompressCandidates are the NSZ files of the library a recompression would change: the solid
// ones to write them in blocks, or the ones in blocks to write them solid; biggest first. A file
// whose storage the library has not noted yet is left out until the next scan notes it.
func (web *Web) recompressCandidates(blocks bool) []CompressCandidate {
	from := switchfs.StorageSolid
	if !blocks {
		from = switchfs.StorageBlocks
	}
	candidates := []CompressCandidate{}
	for _, candidate := range web.decompressCandidates() {
		if candidate.Storage == from {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

// recompressTarget reads the way a request asks the files written: in blocks unless it asks
// for them solid.
func recompressTarget(value string) bool {
	return value != "solid"
}

func (web *Web) startRecompression(paths []string, blocks bool) bool {
	return web.startRecompressionBy(paths, blocks, tasklog.TRIGGER_MANUAL)
}

// startRecompressionBy writes the NSZ files again in blocks, or solid, in the background; it
// goes on after a restart as a compression does. Each file is read twice: written again, then
// its new copy checked.
func (web *Web) startRecompressionBy(paths []string, blocks bool, trigger string) bool {
	return web.runResumableTask(tasklog.TASK_RECOMPRESS, trigger, paths, 2, &tasklog.Resume{Blocks: blocks}, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		return web.recompressFile(ctx, path, blocks, report)
	})
}

// recompressFile writes one NSZ again, in blocks or solid, at the level of Settings, under a
// hidden temporary name next to it; the copy is checked whole and only then takes its name. A
// file that is already so is left as it is. It returns the bytes saved, less than 0 when the
// new file is bigger (blocks are a little bigger than solid).
func (web *Web) recompressFile(ctx context.Context, path string, blocks bool, report func(step int64) func(int64, int64)) (int64, error) {
	appSettings := settings.ReadSettings(web.dataFolder)
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	want := switchfs.StorageSolid
	if blocks {
		want = switchfs.StorageBlocks
	}
	switch storage, err := switchfs.ReadStorage(path); {
	case err != nil:
		return 0, readFailure(path, info, err)
	case storage == want:
		return 0, nil
	case storage != switchfs.StorageSolid && storage != switchfs.StorageBlocks:
		return 0, errRecompressExtra
	}
	// the new file can be a little bigger than the old one, which stays until it is checked
	release, err := web.reserveSpace(filepath.Dir(path), info.Size()+info.Size()/16)
	if err != nil {
		return 0, err
	}
	defer release()

	temporary := temporaryOf(path, path)
	defer os.Remove(temporary)
	level := settingsLevel(appSettings)
	files := filesAtOnce(appSettings.CompressFilesAtOnce)
	result, err := switchfs.RecompressGame(ctx, path, temporary, switchfs.CompressOptions{
		Level: level, Workers: switchfs.LimitThreads(level, compressThreads(appSettings.CompressThreads, files), files),
		Blocks: blocks, Progress: filling(info.Size(), report(0)),
	})
	if err != nil {
		return 0, readFailure(path, info, err)
	}
	check, err := switchfs.VerifyFile(ctx, temporary, filling(info.Size(), report(info.Size())))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0, err
		}
		// the copy failed its check, not the file of the library
		return 0, &notContentError{err}
	}
	if check.Partial {
		return 0, errRecompressPartial
	}
	if err := web.replaceRecompressed(appSettings, temporary, path); err != nil {
		return 0, err
	}
	// what was just checked is the file now in the library
	web.verifications().remember(path, check)
	web.sugarLogger.Infof("Recompressed %s %s: %d -> %d bytes", path, want, result.InputSize, result.OutputSize)
	return result.InputSize - result.OutputSize, nil
}

// replaceRecompressed gives the checked copy the name of the NSZ it was written from. When the
// quarantine keeps originals, the old NSZ goes there first and comes back if the copy cannot
// take its place; otherwise the copy replaces it in one step, so the file is never missing.
func (web *Web) replaceRecompressed(appSettings *settings.AppSettings, temporary string, path string) error {
	if !quarantineOn(appSettings.Quarantine) || !appSettings.Quarantine.Originals {
		return durable.CommitFile(temporary, path)
	}
	if err := web.putInQuarantine(appSettings, path, QUARANTINE_RECOMPRESSED); err != nil {
		return fmt.Errorf("could not delete the original: %w", err)
	}
	err := durable.CommitFile(temporary, path)
	if err == nil {
		return nil
	}
	// the old file back where it was: the newest one the quarantine keeps of it
	items := web.quarantine().list()
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Path == path && items[i].Reason == QUARANTINE_RECOMPRESSED {
			if _, restoreErr := web.quarantine().restore(items[i].Id); restoreErr != nil {
				web.sugarLogger.Errorf("%s could not be put back from the quarantine: %v", path, restoreErr)
			}
			break
		}
	}
	return err
}

// recompressNote is the note of a file a recompression could not write again.
func recompressNote(err error) string {
	switch {
	case errors.Is(err, errCompressSpace):
		return NOTE_COMPRESS_SPACE
	case errors.Is(err, errRecompressPartial):
		return NOTE_RECOMPRESS_PARTIAL
	case errors.Is(err, errRecompressExtra):
		return NOTE_RECOMPRESS_EXTRA
	case strings.Contains(err.Error(), "could not delete"):
		return NOTE_COMPRESS_DELETE
	}
	return NOTE_RECOMPRESS_FAILED
}

// handleRecompress adds the list of the Recompress section, one for each way, and its start.
func (web *Web) handleRecompress(templates templateSet) {
	web.router.HandleFunc("/compress/recompress-list", func(w http.ResponseWriter, r *http.Request) {
		web.writeFileRows(w, r, templates, "recompress", true, web.recompressCandidates(recompressTarget(r.URL.Query().Get("to"))))
	}).Methods("GET")

	web.router.HandleFunc("/recompress/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		blocks := recompressTarget(r.FormValue("to"))
		paths := web.selectedPaths(r, web.recompressCandidates(blocks))
		if len(paths) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the files to recompress.")
			return
		}
		web.startWriting(w, r, QueuedTask{Kind: tasklog.TASK_RECOMPRESS, Paths: paths, Blocks: blocks})
	}).Methods("POST")
}
