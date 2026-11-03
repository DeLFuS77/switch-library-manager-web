//go:build windows

package awake

import (
	"runtime"

	"golang.org/x/sys/windows"
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// preventSleep keeps the computer from going to sleep until the function it gives is called.
func preventSleep() func() {
	if setThreadExecutionState.Find() != nil {
		return func() {}
	}
	done := make(chan struct{})
	released := make(chan struct{})
	// the state belongs to a thread: one goroutine, locked to its thread, holds it
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		setThreadExecutionState.Call(esContinuous | esSystemRequired)
		<-done
		setThreadExecutionState.Call(esContinuous)
		close(released)
	}()
	return func() {
		close(done)
		<-released
	}
}
