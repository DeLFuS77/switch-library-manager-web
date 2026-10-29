package process

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// errCrossDevice is the error of a rename between two disks.
var errCrossDevice error = windows.ERROR_NOT_SAME_DEVICE

func crossDevice(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_SAME_DEVICE)
}

// renameNoReplace gives a file a name no file has, in one step: MoveFileEx without
// MOVEFILE_REPLACE_EXISTING refuses the rename when a file has the name, and goes to no other
// drive. Changing the case of the name only is a rename of the same file, which it lets go.
func renameNoReplace(from string, to string) error {
	fromName, err := windows.UTF16PtrFromString(longPath(from))
	if err == nil {
		var toName *uint16
		if toName, err = windows.UTF16PtrFromString(longPath(to)); err == nil {
			err = windows.MoveFileEx(fromName, toName, 0)
		}
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// longPath is a path Windows takes longer than 260 characters, as Go gives its own calls: an
// absolute path with \\?\ in front.
func longPath(path string) string {
	if len(path) < 248 || !filepath.IsAbs(path) || strings.HasPrefix(path, `\\?\`) {
		return path
	}
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}
