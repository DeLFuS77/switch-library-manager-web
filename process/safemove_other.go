//go:build !linux && !darwin && !windows

package process

import (
	"errors"
	"syscall"
)

// errCrossDevice is the error of a rename between two disks.
var errCrossDevice error = syscall.EXDEV

func crossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}

// renameNoReplace gives a file a name no file has, looked at before the rename.
func renameNoReplace(from string, to string) error {
	return renameChecked(from, to)
}
