package process

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Undo of Organize: each file it moved or renamed goes back to its path of before, one at a
// time, and is looked at just before: a file that is no longer where Organize left it, that
// changed since (another size or date), or whose path of before another file has now stays where
// it is. Nothing is ever written over, and no file is deleted: only the folders Organize made,
// once they are empty.

// UndoMove is a file Organize moved or renamed: its path of before and of after, and its size and
// date once moved, which tell it is still the same file.
type UndoMove struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
}

// how a file of an undo stands, or how putting it back went
const (
	// it goes back (or went back) to its path of before
	UNDO_BACK = "back"
	// it is at its path of before already, as it was: an undo cut halfway put it back
	UNDO_DONE = "done"
	// another file has its path of before
	UNDO_TAKEN = "taken"
	// it has another size, or another date, than when Organize left it
	UNDO_CHANGED_SIZE = "changedSize"
	UNDO_CHANGED_DATE = "changedDate"
	// it is no longer where Organize left it
	UNDO_GONE = "gone"
	// it could not be moved back (the error says why)
	UNDO_FAILED = "failed"
)

// UndoReturns tells the states of the files that are (or will be) at their path of before.
func UndoReturns(state string) bool {
	return state == UNDO_BACK || state == UNDO_DONE
}

// CheckUndo tells how a file of an undo stands, without changing anything.
func CheckUndo(move UndoMove) string {
	return checkUndo(move, nil)
}

// CheckUndoAll tells how each file of a run stands, without changing anything, as the undo
// goes: the last one moved first, so a file whose path of before another file of the run leaves
// first goes back too, and two files never go back to the same path.
func CheckUndoAll(moves []UndoMove) []string {
	states := make([]string, len(moves))
	// the paths the files put back before leave free (false) or take (true), by pathKey
	taken := map[string]bool{}
	for i := len(moves) - 1; i >= 0; i-- {
		move := moves[i]
		var fromTaken *bool
		if known, ok := taken[pathKey(move.From)]; ok {
			fromTaken = &known
		}
		state := checkUndo(move, fromTaken)
		switch state {
		case UNDO_BACK:
			taken[pathKey(move.To)] = false
			taken[pathKey(move.From)] = true
		case UNDO_DONE:
			taken[pathKey(move.From)] = true
		}
		states[i] = state
	}
	return states
}

// checkUndo is CheckUndo; fromTaken, when not nil, tells whether the path of before is taken
// once the files put back before have gone, instead of the disk.
func checkUndo(move UndoMove, fromTaken *bool) string {
	toInfo, toErr := os.Lstat(move.To)
	fromInfo, fromErr := os.Lstat(move.From)
	if toErr != nil || !toInfo.Mode().IsRegular() {
		if fromTaken == nil && fromErr == nil && fromInfo.Mode().IsRegular() && fromInfo.Size() == move.Size && fromInfo.ModTime().UnixNano() == move.ModTime {
			return UNDO_DONE
		}
		return UNDO_GONE
	}
	switch {
	case fromTaken != nil:
		if *fromTaken {
			return UNDO_TAKEN
		}
	case fromErr == nil:
		// the same file only when its name changed in its case on a disk that takes names in any case
		if !os.SameFile(fromInfo, toInfo) {
			return UNDO_TAKEN
		}
	case !errors.Is(fromErr, fs.ErrNotExist):
		// its path of before cannot be looked at: nothing is moved there
		return UNDO_TAKEN
	}
	if toInfo.Size() != move.Size {
		return UNDO_CHANGED_SIZE
	}
	if toInfo.ModTime().UnixNano() != move.ModTime {
		return UNDO_CHANGED_DATE
	}
	return UNDO_BACK
}

// PutBack moves a file of an undo back to its path of before, after looking at it again just
// before: it gives how it stands (CheckUndo) when it is not moved, UNDO_BACK when it was. Its
// folder of before is made again when it is missing. A file that comes at the path of before
// between the look and the move is never written over (see moveFile); between two disks the
// file is copied, the copy checked and only then the original deleted, as Organize does.
func PutBack(move UndoMove, progress CopyProgress) (string, error) {
	state := CheckUndo(move)
	if state != UNDO_BACK {
		return state, nil
	}
	if err := os.MkdirAll(filepath.Dir(move.From), os.ModePerm); err != nil {
		return UNDO_FAILED, err
	}
	moveTo := func(from string, to string) error { return moveFileTelling(from, to, progress) }
	if fromInfo, err := os.Lstat(move.From); err == nil {
		// only the case of its name changes: the same file, which a rename that refuses a name
		// taken would refuse
		toInfo, err := os.Lstat(move.To)
		if err != nil || !os.SameFile(fromInfo, toInfo) {
			return UNDO_TAKEN, nil
		}
		moveTo = os.Rename
	}
	if err := moveTo(move.To, move.From); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return UNDO_TAKEN, nil
		}
		return UNDO_FAILED, err
	}
	return UNDO_BACK, nil
}

// UndoFolders deletes the folders Organize made that are empty now, the deepest first so a
// folder left empty by the one in it goes too, and gives the ones deleted. A folder with
// anything in it, even a file of the system, stays: os.Remove refuses it.
func UndoFolders(folders []string) []string {
	sorted := append([]string{}, folders...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return strings.Count(filepath.Clean(sorted[i]), string(filepath.Separator)) > strings.Count(filepath.Clean(sorted[j]), string(filepath.Separator))
	})
	deleted := []string{}
	seen := map[string]bool{}
	for _, folder := range sorted {
		folder = filepath.Clean(folder)
		if seen[folder] || folder == filepath.Dir(folder) {
			continue
		}
		seen[folder] = true
		info, err := os.Lstat(folder)
		if err != nil || !info.IsDir() {
			continue
		}
		if os.Remove(folder) == nil {
			deleted = append(deleted, folder)
		}
	}
	return deleted
}

// MadeFolders are the folders an operation of Organize made: the folder it made (To) and, when
// its parents were missing too, each one up to the first of them (From).
func MadeFolders(op Operation) []string {
	if op.Kind != OP_MKDIR || op.Error != "" || op.To == "" {
		return nil
	}
	folder := filepath.Clean(op.To)
	folders := []string{folder}
	if op.From == "" {
		return folders
	}
	top := filepath.Clean(op.From)
	if rel, err := filepath.Rel(top, folder); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return folders
	}
	for folder != top {
		parent := filepath.Dir(folder)
		if parent == folder {
			break
		}
		folder = parent
		folders = append(folders, folder)
	}
	return folders
}

// topMissingFolder is the first folder on the way down to dir that does not exist, nor is made
// by the run before (planned, for a preview): the one os.MkdirAll makes first.
func topMissingFolder(dir string, planned map[string]bool) string {
	top := filepath.Clean(dir)
	for {
		parent := filepath.Dir(top)
		if parent == top || planned[parent] {
			return top
		}
		if _, err := os.Stat(parent); !errors.Is(err, fs.ErrNotExist) {
			return top
		}
		top = parent
	}
}
