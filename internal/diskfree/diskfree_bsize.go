//go:build !windows && !linux

package diskfree

import "golang.org/x/sys/unix"

// blockSize is the size of the blocks statfs counts.
func blockSize(stat *unix.Statfs_t) uint64 {
	return uint64(stat.Bsize)
}
