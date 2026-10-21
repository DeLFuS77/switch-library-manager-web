//go:build windows

package switchfs

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx is MEMORYSTATUSEX of Windows.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

var globalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// availableMemory is the physical memory Windows has free.
func availableMemory() (uint64, bool) {
	status := memoryStatusEx{}
	status.length = uint32(unsafe.Sizeof(status))
	if ok, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status))); ok == 0 {
		return 0, false
	}
	return status.availPhys, true
}
