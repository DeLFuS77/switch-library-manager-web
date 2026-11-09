package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Set the icon: a NUP that carries no icon (one made from a dump folder without control data)
// gets the cover of its game the library shows (the one chosen in Edit, else the one of titledb),
// cut square at its middle and made a JPEG of 256×256, from the page of its game, as NXPress does
// it (switchfs/nupicon.go): Eglantine and Lichii then show it with its icon. What the NUP has is
// not changed: its metadata of the game is written again after its end with the icon. It is a
// task on files, as Fix for emulators: it waits for the others in the queue, and one the app was
// stopped in the middle of goes on after the restart, which first puts back what it left beside
// the NUP. Only administrators start it.

const (
	NOTE_NUP_ICON_FAILED = "The icon could not be given to the NUP: it is left as it was."
	// the game has no cover yet to make the icon of
	NOTE_NUP_ICON_NO_COVER = "Its game has no cover yet to make the icon of: choose one in Edit, or wait for the covers to download."
	// a file beside the NUP with the name of the copy of its table of contents, which is not of it
	NOTE_NUP_ICON_OTHER_COPY = "Beside the NUP there is a copy of the table of contents of another NUP (.nxpress-icon): remove it to go on."
	// the button on the page of the game, which is also the name of the task
	NUP_ICON_ACTION = "Set the icon"
	// what the button does, in its help (the same text as in title.html)
	NUP_ICON_HELP = "Makes the cover of its game shown on this page (the one chosen in Edit, else the one of titledb) the icon of the NUP: cut square at its middle and made 256×256, as the icons of the console are. Nothing the NUP has changes: the metadata of the game is written again after its end with the icon, and the files of the game are not touched. While it writes, it keeps a copy of the header and the table of contents beside the NUP, and puts it back if anything fails or the work is cut. For another image, change the cover in Edit first: a square one of 256×256 or bigger looks best. It runs as a task."
	// what is said of a NUP without an icon on the page of its game
	NUP_NO_ICON        = "It carries no icon: Eglantine and Lichii show it without one."
	errNupIconNotFound = errorString("This file is not a NUP of the library without an icon.")
)

// errNupIconNoCover is a game without a cover to make the icon of.
var errNupIconNoCover = errors.New("its game has no cover")

// noIconNup is the path the library has for a NUP of it that carries no icon, as the last scan
// read it, and the title ID prefix of its game; "" for any other path. What the page sent is only
// compared with the paths of the library, never looked for on the disk.
func (web *Web) noIconNup(requested string) (string, string) {
	_, localDB := web.state.get()
	if localDB == nil || requested == "" {
		return "", ""
	}
	wanted := web.moves.resolve(filepath.Clean(requested))
	matches := func(info db.SwitchFileInfo) bool {
		if info.Metadata == nil || info.Metadata.Emulators == nil || !info.Metadata.Emulators.NoIcon ||
			!switchfs.IsNupFile(info.ExtendedInfo.FileName) {
			return false
		}
		return web.moves.resolve(filepath.Join(info.ExtendedInfo.BaseFolder, info.ExtendedInfo.FileName)) == wanted
	}
	for prefix, game := range localDB.TitlesMap {
		if matches(game.File) {
			return wanted, prefix
		}
		for _, update := range game.Updates {
			if matches(update) {
				return wanted, prefix
			}
		}
		for _, dlc := range game.Dlc {
			if matches(dlc) {
				return wanted, prefix
			}
		}
	}
	return "", ""
}

// nupIconOf is the icon a NUP of the game of a title ID prefix takes: its cover the library shows,
// else the one of titledb downloaded, made 256×256.
func (web *Web) nupIconOf(prefix string) ([]byte, error) {
	switchDB, localDB := web.state.get()
	var data []byte
	if localDB != nil {
		if game, ok := localDB.TitlesMap[prefix]; ok {
			if path, ok := web.imagePath(gameIcon(switchDB, prefix, game)); ok {
				if info, err := os.Stat(path); err == nil && info.Size() <= switchfs.MaxNupIconFile {
					data, _ = os.ReadFile(path)
				}
			}
		}
	}
	if data == nil && switchDB != nil {
		if title, ok := switchDB.TitlesMap[prefix]; ok && title.Attributes.IconUrl != "" {
			data, _ = db.DownloadImage(title.Attributes.IconUrl, switchfs.MaxNupIconFile)
		}
	}
	if data == nil {
		return nil, errNupIconNoCover
	}
	return switchfs.MakeNupIcon(data)
}

// startNupIconBy gives the NUP files at paths their icon one after the other, as a task on files
// that goes on after a restart.
func (web *Web) startNupIconBy(paths []string, trigger string) bool {
	sizes := make([]int64, len(paths))
	for i := range sizes {
		sizes[i] = 1 << 20
	}
	return web.runFileTaskFull(tasklog.TASK_NUP_ICON, trigger, paths, sizes, 1, &tasklog.Resume{}, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		return 0, web.setNupIcon(path)
	})
}

// setNupIcon gives a NUP without an icon the cover of its game. Its check stands: only its table
// of contents and its metadata changed, and the data of each file and its hash are as they were.
func (web *Web) setNupIcon(path string) error {
	container, err := switchfs.ReadNup(path)
	if err != nil {
		return err
	}
	if container.HasIcon() {
		// given one by NXPress since the scan: nothing to do
		return nil
	}
	pkg := container.Packages[container.IconPackage()]
	prefix, err := db.TitleIDPrefix(switchfs.NupTitleId(pkg))
	if err != nil {
		return err
	}
	icon, err := web.nupIconOf(strings.ToLower(prefix))
	if err != nil {
		return err
	}
	store := web.verifications()
	var checked *verifyRecord
	if info, err := os.Stat(path); err == nil {
		if record, ok := store.current(path, info); ok {
			checked = &record
		}
	}
	set, err := switchfs.SetNupIcon(path, icon)
	if err != nil || !set {
		return err
	}
	if info, err := os.Stat(path); err == nil && checked != nil {
		record := *checked
		record.Size, record.ModTime = info.Size(), info.ModTime().UnixNano()
		store.set(path, record)
		store.save()
	}
	web.sugarLogger.Infof("Gave %s the cover of its game as its icon, 256×256", path)
	return nil
}

// nupIconNote is the note of an icon that could not be given.
func nupIconNote(err error) string {
	switch {
	case fileGone(err):
		return NOTE_FILE_GONE
	case errors.Is(err, errNupIconNoCover):
		return NOTE_NUP_ICON_NO_COVER
	case errors.Is(err, switchfs.ErrNupIconOtherCopy), errors.Is(err, switchfs.ErrNupMetaOtherCopy):
		return NOTE_NUP_ICON_OTHER_COPY
	}
	return NOTE_NUP_ICON_FAILED
}

func (web *Web) handleNupIcon() {
	web.router.HandleFunc("/title/nup-icon", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		path, _ := web.noIconNup(r.FormValue("path"))
		if path == "" {
			writeGlobalError(w, http.StatusBadRequest, lang, string(errNupIconNotFound))
			return
		}
		web.startOrQueue(w, QueuedTask{Kind: tasklog.TASK_NUP_ICON, Paths: []string{path}})
	}).Methods("POST")
}
