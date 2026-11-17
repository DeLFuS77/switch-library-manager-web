package process

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// renameSys is the rename of the system, which a test replaces.
var renameSys = os.Rename

// renameCase gives a file or a folder the case of to, where to is the same one with another case.
// Linux, on a file system that does not tell cases apart (a share of a NAS, a dataset that ignores
// case, in Docker), sees that the new name is the same file and does nothing, saying it did (#179):
// the name goes through a free one in the same folder, so the case changes everywhere.
func renameCase(from string, to string) error {
	if from == to {
		return nil
	}
	dir := filepath.Dir(from)
	var free string
	for i := 0; ; i++ {
		free = filepath.Join(dir, ".slm-case-"+strconv.FormatInt(time.Now().UnixNano(), 36)+strconv.Itoa(i))
		if _, err := os.Lstat(free); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return err
		}
	}
	if err := renameSys(from, free); err != nil {
		return err
	}
	if err := renameSys(free, to); err != nil {
		// back to its name, as it was
		if back := renameSys(free, from); back != nil {
			return errors.Join(err, back)
		}
		return err
	}
	return nil
}
