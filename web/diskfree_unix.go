//go:build !windows

package web

import "golang.org/x/sys/unix"

// diskFree returns the bytes available to the user on the volume of path.
func diskFree(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
