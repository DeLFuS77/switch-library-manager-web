//go:build windows

package web

import "golang.org/x/sys/windows"

// diskFree returns the bytes available to the user on the volume of path.
func diskFree(path string) (uint64, error) {
	available, _, err := diskSpace(path)
	return available, err
}

// diskSpace returns the bytes available to the user on the volume of path, and its size.
func diskSpace(path string) (uint64, uint64, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(pointer, &available, &total, &free); err != nil {
		return 0, 0, err
	}
	return available, total, nil
}
