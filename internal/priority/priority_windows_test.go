//go:build windows

package priority

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Low priority puts the app below the normal class with its memory low, and back.
func TestSetLow(t *testing.T) {
	process := windows.CurrentProcess()
	class := func() uint32 {
		value, _ := windows.GetPriorityClass(process)
		return value
	}
	memory := func() uint32 {
		var value uint32
		windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessInformation").Call(uintptr(process), processMemoryPriority, uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
		return value
	}
	defer SetLow(false)
	if err := SetLow(true); err != nil || class() != windows.BELOW_NORMAL_PRIORITY_CLASS || memory() != memoryPriorityLow {
		t.Fatalf("low: class %#x, memory %d, %v", class(), memory(), err)
	}
	if err := SetLow(false); err != nil || class() != windows.NORMAL_PRIORITY_CLASS || memory() != memoryPriorityNormal {
		t.Fatalf("back: class %#x, memory %d, %v", class(), memory(), err)
	}
}
