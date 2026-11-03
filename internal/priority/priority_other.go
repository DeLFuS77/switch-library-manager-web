//go:build !windows

package priority

import (
	"os"
	"runtime"
	"strconv"
	"syscall"
)

// setLow runs the app at nice 10; false gives back nice 0. On Linux each thread has a nice
// value of its own, and a new one takes the value of the thread that makes it: every thread of
// the process is set. Linux lets a user raise it back only within the limit of the user
// (RLIMIT_NICE, usually none, and none in a container): when that fails, the app stays low
// until it starts again.
func setLow(low bool) error {
	nice := 0
	if low {
		nice = 10
	}
	if runtime.GOOS == "linux" {
		if tasks, err := os.ReadDir("/proc/self/task"); err == nil {
			var first error
			for _, task := range tasks {
				id, err := strconv.Atoi(task.Name())
				if err != nil {
					continue
				}
				if err := syscall.Setpriority(syscall.PRIO_PROCESS, id, nice); err != nil && first == nil {
					first = err
				}
			}
			return first
		}
	}
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, nice)
}
