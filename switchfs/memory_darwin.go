//go:build darwin

package switchfs

import (
	"encoding/binary"
	"syscall"
)

// availableMemory is half the memory of the Mac: macOS keeps most of it in use as cache, so
// the memory it calls free says little.
func availableMemory() (uint64, bool) {
	value, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0, false
	}
	// the value comes as the bytes of a 64 bit number, the last one lost when it is zero
	data := make([]byte, 8)
	copy(data, value)
	return binary.LittleEndian.Uint64(data) / 2, true
}
