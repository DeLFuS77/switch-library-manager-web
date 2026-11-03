//go:build !windows

package diskfree

import (
	"strconv"

	"golang.org/x/sys/unix"
)

// Free returns the bytes available to the user on the volume of path.
func Free(path string) (uint64, error) {
	available, _, err := Space(path)
	return available, err
}

// Space returns the bytes available to the user on the volume of path, and its size.
func Space(path string) (uint64, uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	size := blockSize(&stat)
	return uint64(stat.Bavail) * size, uint64(stat.Blocks) * size, nil
}

// Volume names the volume of a folder, to add up what goes to the same disk: its device.
func Volume(folder string) string {
	var stat unix.Stat_t
	if err := unix.Stat(folder, &stat); err != nil {
		return folder
	}
	return strconv.FormatUint(uint64(stat.Dev), 10)
}
