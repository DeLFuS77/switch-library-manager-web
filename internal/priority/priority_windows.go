//go:build windows

package priority

import "golang.org/x/sys/windows"

// setLow puts the process in the class below the normal one: the threads of zstd belong to it
// and go down with it.
func setLow(low bool) error {
	class := uint32(windows.NORMAL_PRIORITY_CLASS)
	if low {
		class = windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	return windows.SetPriorityClass(windows.CurrentProcess(), class)
}
