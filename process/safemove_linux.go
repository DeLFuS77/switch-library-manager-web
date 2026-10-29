package process

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// errCrossDevice is the error of a rename between two disks.
var errCrossDevice error = unix.EXDEV

func crossDevice(err error) bool {
	return errors.Is(err, unix.EXDEV)
}

// renameNoReplace gives a file a name no file has, in one step: the system refuses the rename
// when a file has the name (RENAME_NOREPLACE). A file system that cannot (some shares, FUSE)
// is looked at before.
func renameNoReplace(from string, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.ENOTSUP) {
		return renameChecked(from, to)
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
