package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Fix for emulators: a NUP whose metadata has its types counted from 0, as a build of the packer
// of the format wrote them, gets them written again where the format puts them, from the page of
// its game, as NXPress does it (switchfs/nupfixmeta.go): Eglantine and Lichii then show its name,
// version and icon right. Only those numbers change, in the NUP itself. It is a task on files: it
// waits for the others in the queue, and one the app was stopped in the middle of goes on after
// the restart, which first puts back what it left beside the NUP. The library reads the NUP again
// once it is done. Only administrators start it.

const (
	NOTE_NUP_FIX_FAILED = "The NUP could not be fixed for the emulators: it is left as it was."
	// a file beside the NUP with the name of the copy of its metadata, which is not of it
	NOTE_NUP_FIX_OTHER_COPY = "Beside the NUP there is a copy of the metadata of another NUP (.nxpress-meta): remove it to go on."
	// the button on the page of the game, which is also the name of the task
	NUP_FIX_ACTION = "Fix for emulators"
	// what the button does, in its help (the same text as in title.html)
	NUP_FIX_HELP      = "Writes again, in the NUP itself, only the number of the type of each record of its metadata, so they are counted from 1 as the format says: Eglantine and Lichii then show its name, version and icon right. The files of the game are not touched. While it writes, it keeps a copy of the old blocks beside the NUP, and puts it back if anything fails or the work is cut; then it reads the NUP again, and the library reads it again. It runs as a task."
	errNupFixNotFound = errorString("This file is not a NUP of the library whose metadata is counted from 0.")
)

// zeroBasedNup is the path the library has for a NUP of it whose metadata is counted from 0, as the
// last scan read it, sent as requested by the page of its game; "" for any other path. What the
// page sent is only compared with the paths of the library, never looked for on the disk.
func (web *Web) zeroBasedNup(requested string) string {
	_, localDB := web.state.get()
	if localDB == nil || requested == "" {
		return ""
	}
	// a page drawn before Organize moved the file sends its old path
	wanted := web.moves.resolve(filepath.Clean(requested))
	matches := func(info db.SwitchFileInfo) bool {
		if info.Metadata == nil || info.Metadata.Emulators == nil || !info.Metadata.Emulators.ZeroBased ||
			!switchfs.IsNupFile(info.ExtendedInfo.FileName) {
			return false
		}
		return web.moves.resolve(filepath.Join(info.ExtendedInfo.BaseFolder, info.ExtendedInfo.FileName)) == wanted
	}
	for _, game := range localDB.TitlesMap {
		if matches(game.File) {
			return wanted
		}
		for _, update := range game.Updates {
			if matches(update) {
				return wanted
			}
		}
	}
	return ""
}

// startNupFixBy fixes the NUP files at paths one after the other, as a task on files that goes on
// after a restart.
func (web *Web) startNupFixBy(paths []string, trigger string) bool {
	// the work is on each file, not on its bytes: a few kilobytes are read and written
	sizes := make([]int64, len(paths))
	for i := range sizes {
		sizes[i] = 1 << 20
	}
	return web.runFileTaskFull(tasklog.TASK_NUP_FIX, trigger, paths, sizes, 1, &tasklog.Resume{}, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		return 0, web.fixNup(path)
	})
}

// fixNup writes again the metadata of a NUP counted from 0. Its check stands: only its metadata
// changed, which the check does not read (it reads the data of each file, by the hash its table of
// contents keeps, and neither changes).
func (web *Web) fixNup(path string) error {
	store := web.verifications()
	var checked *verifyRecord
	if info, err := os.Stat(path); err == nil {
		if record, ok := store.current(path, info); ok {
			checked = &record
		}
	}
	fixed, err := switchfs.FixNupZeroBased(path)
	if err != nil {
		return err
	}
	if fixed == 0 {
		// counted from 1 already (fixed by NXPress since the scan): nothing to do
		return nil
	}
	if info, err := os.Stat(path); err == nil && checked != nil {
		record := *checked
		record.Size, record.ModTime = info.Size(), info.ModTime().UnixNano()
		store.set(path, record)
		store.save()
	}
	web.sugarLogger.Infof("Wrote again the metadata of %s counted from 1, for Eglantine and Lichii: %d packages", path, fixed)
	return nil
}

// nupFixNote is the note of a fix that failed.
func nupFixNote(err error) string {
	switch {
	case fileGone(err):
		return NOTE_FILE_GONE
	case errors.Is(err, switchfs.ErrNupMetaOtherCopy):
		return NOTE_NUP_FIX_OTHER_COPY
	}
	return NOTE_NUP_FIX_FAILED
}

func (web *Web) handleNupFix() {
	web.router.HandleFunc("/title/nup-fix", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		path := web.zeroBasedNup(r.FormValue("path"))
		if path == "" {
			writeGlobalError(w, http.StatusBadRequest, lang, string(errNupFixNotFound))
			return
		}
		web.startOrQueue(w, QueuedTask{Kind: tasklog.TASK_NUP_FIX, Paths: []string{path}})
	}).Methods("POST")
}
