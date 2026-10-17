package web

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A task the app was stopped in the middle of leaves the file it was writing: hidden, next to
// the game (".<game>.nsz.tmp"). A task that goes on after the restart removes it first; one
// that does not (cancelled, from an older version, its game moved) would leave it for good,
// taking gigabytes nobody sees. The walk of a scan notes them, and they are removed once no
// task changes files and they have not been written for a while.

// leftoverAge is how long a temporary file must be left untouched before it is removed: a
// task writing one changes it all the time.
const leftoverAge = 30 * time.Minute

const ACTION_LEFTOVERS = "Removed temporary files left halfway"

// cleanLeftovers removes the temporary files of the app the last scan found and no task writes.
func (web *Web) cleanLeftovers() {
	_, localDB := web.state.get()
	if localDB == nil || len(localDB.Leftovers) == 0 || isDemoMode() || web.filesBusy() {
		return
	}
	removed, freed := 0, int64(0)
	for _, leftover := range localDB.Leftovers {
		info, err := os.Stat(leftover.Path)
		// gone already, or written since the scan: a task is on it
		if err != nil || info.IsDir() || time.Since(info.ModTime()) < leftoverAge {
			continue
		}
		if web.filesBusy() {
			break
		}
		if os.Remove(leftover.Path) == nil {
			removed++
			freed += info.Size()
		}
	}
	freed += web.cleanVaultLeftovers()
	if removed == 0 {
		return
	}
	detail := fmt.Sprintf("%d (%s)", removed, formatSize(freed))
	web.sugarLogger.Infof("[%s: %s]", ACTION_LEFTOVERS, detail)
	web.activities().add(Activity{Time: time.Now(), Action: ACTION_LEFTOVERS, Detail: detail})
}

// cleanVaultLeftovers removes the uploads to the save vault that were cut and left behind
// (".<save>.zip.*.part", see vaultfs.go), and returns the space freed.
func (web *Web) cleanVaultLeftovers() int64 {
	root := web.vaultRoot()
	freed := int64(0)
	filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".part") {
			return nil
		}
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) >= leftoverAge && os.Remove(path) == nil {
			freed += info.Size()
		}
		return nil
	})
	return freed
}
