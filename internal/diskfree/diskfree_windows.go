//go:build windows

package diskfree

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Free returns the bytes available to the user on the volume of path.
func Free(path string) (uint64, error) {
	available, _, err := Space(path)
	return available, err
}

// Space returns the bytes available to the user on the volume of path, and its size.
func Space(path string) (uint64, uint64, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(pointer, &available, &total, &free); err != nil {
		return 0, 0, err
	}
	return available, total, nil
}

// Volume names the volume of a folder, to add up what goes to the same disk: its letter or share.
func Volume(folder string) string {
	if volume := filepath.VolumeName(folder); volume != "" {
		return strings.ToLower(volume)
	}
	return folder
}
