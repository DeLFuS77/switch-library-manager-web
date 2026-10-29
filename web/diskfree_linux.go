package web

import "golang.org/x/sys/unix"

// blockSize is the size of the blocks statfs counts. Linux counts them in fragments (Frsize):
// FUSE file systems (shfs of Unraid, mergerfs, rclone) give a Bsize that is the size of their
// reads instead, and the space was counted many times too large or too small.
func blockSize(stat *unix.Statfs_t) uint64 {
	if stat.Frsize > 0 {
		return uint64(stat.Frsize)
	}
	return uint64(stat.Bsize)
}
