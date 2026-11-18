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

// stillActive is the exit code Windows gives a process that has not ended (STILL_ACTIVE).
const stillActive = 259

// processAlive tells whether a process runs on this computer; true when it cannot tell.
func processAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// no such process; one of another user that may not be opened still runs
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return true
	}
	return code == stillActive
}
