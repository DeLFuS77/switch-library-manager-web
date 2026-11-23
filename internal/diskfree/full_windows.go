//go:build windows

package diskfree

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Full tells an error of writing to a volume that has no room left.
func Full(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}

// fullError is the error of a full volume, for the tests.
var fullError error = windows.ERROR_DISK_FULL
