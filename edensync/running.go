package edensync

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrCannotTell tells a system where the package cannot tell whether the emulator is open: a pass
// then does nothing, as a save is never replaced while the emulator may be using it.
var ErrCannotTell = errors.New("cannot tell whether the emulator is open on this system")

// Running tells whether the emulator is open on this computer, by the names of the programs that
// run: eden, eden.exe, eden-cli, an AppImage named Eden-…; not its server of rooms.
func Running(emulator Emulator) (bool, error) {
	names, err := processNames()
	if err != nil {
		return false, err
	}
	for _, name := range names {
		if IsEmulatorProcess(emulator, name) {
			return true, nil
		}
	}
	return false, nil
}

// IsEmulatorProcess tells whether the name of a program that runs is one of the emulator.
func IsEmulatorProcess(emulator Emulator, name string) bool {
	name = strings.ToLower(filepath.Base(filepath.ToSlash(strings.TrimSpace(name))))
	name = strings.TrimSuffix(name, ".exe")
	name = strings.TrimSuffix(name, ".appimage")
	base := string(emulator)
	if name == base {
		return true
	}
	if !strings.HasPrefix(name, base+"-") && !strings.HasPrefix(name, base+"_") && !strings.HasPrefix(name, base+".") {
		return false
	}
	// the server of the rooms of the emulator does not use the saves
	return !strings.HasPrefix(name, base+"-room")
}
