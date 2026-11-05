//go:build windows

package priority

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// the memory priority of a process, as SetProcessInformation takes it
const (
	processMemoryPriority = 0
	memoryPriorityLow     = 2
	memoryPriorityNormal  = 5
)

var setProcessInformation = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetProcessInformation")

// setLow puts the process in the class below the normal one: the threads of zstd belong to it
// and go down with it. Its memory goes low too: when memory runs short, the pages of the app are
// the first given to the other programs, as in NXPress.
func setLow(low bool) error {
	class, memory := uint32(windows.NORMAL_PRIORITY_CLASS), uint32(memoryPriorityNormal)
	if low {
		class, memory = windows.BELOW_NORMAL_PRIORITY_CLASS, memoryPriorityLow
	}
	process := windows.CurrentProcess()
	if err := windows.SetPriorityClass(process, class); err != nil {
		return err
	}
	setProcessInformation.Call(uintptr(process), processMemoryPriority, uintptr(unsafe.Pointer(&memory)), unsafe.Sizeof(memory))
	return nil
}
