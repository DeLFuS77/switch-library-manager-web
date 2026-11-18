//go:build linux

package edensync

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// processNames are the names of the programs that run, from /proc: the name of each process (cut
// at 15 characters by Linux) and the program it was started with.
func processNames() ([]string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, ErrCannotTell
	}
	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.Trim(entry.Name(), "0123456789") != "" {
			continue
		}
		folder := filepath.Join("/proc", entry.Name())
		if comm, err := os.ReadFile(filepath.Join(folder, "comm")); err == nil {
			names = append(names, strings.TrimSpace(string(comm)))
		}
		if command, err := os.ReadFile(filepath.Join(folder, "cmdline")); err == nil && len(command) > 0 {
			if first, _, _ := bytes.Cut(command, []byte{0}); len(first) > 0 {
				names = append(names, filepath.Base(string(first)))
			}
		}
	}
	return names, nil
}
