//go:build !linux && !windows && !darwin

package switchfs

// availableMemory is unknown on this system: the threads are left as set.
func availableMemory() (uint64, bool) {
	return 0, false
}
