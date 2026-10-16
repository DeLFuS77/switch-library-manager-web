//go:build !windows

package web

import "golang.org/x/sys/unix"

// diskFree returns the bytes available to the user on the volume of path.
func diskFree(path string) (uint64, error) {
	available, _, err := diskSpace(path)
	return available, err
}

// diskSpace returns the bytes available to the user on the volume of path, and its size.
func diskSpace(path string) (uint64, uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), uint64(stat.Blocks) * uint64(stat.Bsize), nil
}
