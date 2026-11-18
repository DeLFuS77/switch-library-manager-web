//go:build darwin

package edensync

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// processNames are the names of the programs that run, as the kernel lists them (cut at 16
// characters).
func processNames() ([]string, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, ErrCannotTell
	}
	names := make([]string, 0, len(processes))
	for _, process := range processes {
		name := process.Proc.P_comm[:]
		if end := bytes.IndexByte(name, 0); end >= 0 {
			name = name[:end]
		}
		names = append(names, string(name))
	}
	return names, nil
}
