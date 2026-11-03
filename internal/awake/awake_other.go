//go:build !windows

package awake

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// preventSleep keeps the computer from going to sleep until the function it gives is called:
// caffeinate on macOS, systemd-inhibit on Linux, when there (a container has none, and the
// computer is the host's to keep awake). Both end with the app too, if it ends without
// calling it: caffeinate waits for it, and on Linux tail, which systemd-inhibit holds the
// computer awake for.
func preventSleep() func() {
	var command *exec.Cmd
	if runtime.GOOS == "darwin" {
		command = exec.Command("caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	} else {
		command = exec.Command("systemd-inhibit", "--what=sleep:idle", "--who=Switch Library Manager", "--why=A task works on the files", "--mode=block",
			"tail", "--pid="+strconv.Itoa(os.Getpid()), "-f", "/dev/null")
	}
	if command.Start() != nil {
		return func() {}
	}
	return func() {
		command.Process.Kill()
		command.Wait()
	}
}
