//go:build windows

package edensync

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processNames are the names of the programs that run, from a snapshot of the processes.
func processNames() ([]string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	names := []string{}
	err = windows.Process32First(snapshot, &entry)
	for err == nil {
		names = append(names, windows.UTF16ToString(entry.ExeFile[:]))
		err = windows.Process32Next(snapshot, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return names, nil
}
