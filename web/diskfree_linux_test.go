package web

import (
	"testing"

	"golang.org/x/sys/unix"
)

// The space of a disk is counted in the fragments statfs counts its blocks in: a FUSE file
// system (shfs of Unraid, mergerfs, rclone) gives the size of its reads as Bsize.
func TestBlockSizeIsTheFragmentSize(t *testing.T) {
	if got := blockSize(&unix.Statfs_t{Bsize: 131072, Frsize: 4096}); got != 4096 {
		t.Fatalf("the fragment size: %d", got)
	}
	if got := blockSize(&unix.Statfs_t{Bsize: 4096}); got != 4096 {
		t.Fatalf("Bsize without a fragment size: %d", got)
	}
}
