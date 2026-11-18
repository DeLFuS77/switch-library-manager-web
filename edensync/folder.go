package edensync

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Emulator is an emulator whose folders the package knows: Eden and Citron, forks of yuzu that keep
// its folders and formats.
type Emulator string

const (
	Eden   Emulator = "eden"
	Citron Emulator = "citron"
)

// Emulators are the emulators the package looks for, in that order.
var Emulators = []Emulator{Eden, Citron}

// Name is the name of the emulator as people write it.
func (e Emulator) Name() string {
	switch e {
	case Eden:
		return "Eden"
	case Citron:
		return "Citron"
	}
	return string(e)
}

// ParseEmulator is the emulator of a name, in any case.
func ParseEmulator(name string) (Emulator, bool) {
	for _, emulator := range Emulators {
		if strings.EqualFold(strings.TrimSpace(name), string(emulator)) {
			return emulator, true
		}
	}
	return "", false
}

// Folder is the data folder of an emulator on this computer.
type Folder struct {
	Emulator Emulator
	// the data folder: %APPDATA%\eden, ~/.local/share/eden, or the portable folder "user"
	Root string
	// the NAND folder, with the saves and the profiles: Root/nand, or the one the settings of the
	// emulator name
	Nand string
	// the folder "user" next to the program of the emulator
	Portable bool
}

// ErrNotFound tells that the data folder of an emulator is not on this computer.
var ErrNotFound = errors.New("the data folder of the emulator was not found")

// environment is what Find reads of the computer, replaced in the tests.
type environment struct {
	goos   string
	getenv func(string) string
	home   string
	// the folder of the program of the emulator, where a portable folder "user" is; "" if unknown
	exeDir string
}

func currentEnvironment(exe string) environment {
	home, _ := os.UserHomeDir()
	env := environment{goos: runtime.GOOS, getenv: os.Getenv, home: home}
	if exe != "" {
		env.exeDir = filepath.Dir(exe)
	}
	return env
}

// candidate is a data folder an emulator may have, and where its settings are.
type candidate struct {
	root, config string
	portable     bool
}

// candidates are the data folders an emulator may have, the first that exists being its own, as
// Eden chooses them (src/common/fs/path_util.cpp, Reinitialize): the folder "user" next to its
// program, else %APPDATA%\eden on Windows and $XDG_DATA_HOME/eden (~/.local/share/eden) elsewhere,
// macOS included. Older builds for macOS used ~/Library/Application Support/eden, looked at last.
// The settings are in config/ of the data folder on Windows and when portable, else in
// $XDG_CONFIG_HOME/eden (~/.config/eden).
func candidates(emulator Emulator, env environment) []candidate {
	name := string(emulator)
	list := []candidate{}
	if env.exeDir != "" {
		root := filepath.Join(env.exeDir, "user")
		list = append(list, candidate{root: root, config: filepath.Join(root, "config"), portable: true})
	}
	if env.goos == "windows" {
		if appData := env.getenv("APPDATA"); appData != "" {
			root := filepath.Join(appData, name)
			list = append(list, candidate{root: root, config: filepath.Join(root, "config")})
		}
		return list
	}
	data := env.getenv("XDG_DATA_HOME")
	if data == "" && env.home != "" {
		data = filepath.Join(env.home, ".local", "share")
	}
	config := env.getenv("XDG_CONFIG_HOME")
	if config == "" && env.home != "" {
		config = filepath.Join(env.home, ".config")
	}
	if data != "" {
		list = append(list, candidate{root: filepath.Join(data, name), config: filepath.Join(config, name)})
	}
	if env.goos == "darwin" && env.home != "" {
		root := filepath.Join(env.home, "Library", "Application Support", name)
		list = append(list, candidate{root: root, config: filepath.Join(root, "config")})
	}
	return list
}

// Find looks for the data folder of an emulator on this computer, only reading. exe, when not
// "", is the program of the emulator (eden.exe): a folder "user" next to it is a portable one.
func Find(emulator Emulator, exe string) (Folder, error) {
	return find(emulator, currentEnvironment(exe))
}

func find(emulator Emulator, env environment) (Folder, error) {
	for _, place := range candidates(emulator, env) {
		nand := filepath.Join(place.root, "nand")
		if other := configNand(filepath.Join(place.config, "qt-config.ini")); other != "" {
			nand = other
		}
		if isDir(nand) {
			return Folder{Emulator: emulator, Root: place.root, Nand: nand, Portable: place.portable}, nil
		}
	}
	return Folder{}, ErrNotFound
}

// FindAll is the data folders of the emulators found on this computer.
func FindAll() []Folder {
	found := []Folder{}
	for _, emulator := range Emulators {
		if folder, err := Find(emulator, ""); err == nil {
			found = append(found, folder)
		}
	}
	return found
}

// Open takes a folder given by hand: the data folder of the emulator (with nand in it), or its
// NAND folder (with user and system in it).
func Open(emulator Emulator, path string) (Folder, error) {
	path = filepath.Clean(path)
	if isDir(filepath.Join(path, "nand")) {
		return Folder{Emulator: emulator, Root: path, Nand: filepath.Join(path, "nand")}, nil
	}
	if isDir(filepath.Join(path, "user")) || isDir(filepath.Join(path, "system")) {
		return Folder{Emulator: emulator, Root: filepath.Dir(path), Nand: path}, nil
	}
	return Folder{}, ErrNotFound
}

// configNand is the NAND folder the settings of the emulator name, or "". Eden writes it as
// nand_directory in its settings (src/frontend_common/config.cpp), with "/" and in quotes when
// it holds a special character.
func configNand(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !ok || strings.TrimSpace(key) != "nand_directory" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if value == "" {
			return ""
		}
		return filepath.Clean(filepath.FromSlash(value))
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
