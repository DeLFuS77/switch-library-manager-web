//go:build linux

package switchfs

import "os"

// availableMemory is the memory the app can still take: the least of what the system has
// available and what its container (cgroup v2 or v1) allows beyond what it uses.
func availableMemory() (uint64, bool) {
	return linuxAvailableMemory(os.ReadFile)
}
