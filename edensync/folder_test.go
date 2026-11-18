package edensync

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeEnv(goos string, vars map[string]string, home, exeDir string) environment {
	return environment{goos: goos, getenv: func(name string) string { return vars[name] }, home: home, exeDir: exeDir}
}

// The places Eden keeps its data, as it chooses them: portable next to the program first, then
// the folder of the system.
func TestCandidates(t *testing.T) {
	roots := func(list []candidate) []string {
		out := []string{}
		for _, c := range list {
			out = append(out, c.root)
		}
		return out
	}
	windows := candidates(Eden, fakeEnv("windows", map[string]string{"APPDATA": `C:\Users\Ana\AppData\Roaming`}, `C:\Users\Ana`, `D:\Emu`))
	if got := roots(windows); len(got) != 2 || got[0] != filepath.Join(`D:\Emu`, "user") || got[1] != filepath.Join(`C:\Users\Ana\AppData\Roaming`, "eden") || !windows[0].portable {
		t.Errorf("windows: %v", got)
	}
	linux := candidates(Citron, fakeEnv("linux", map[string]string{}, "/home/ana", ""))
	if len(linux) != 1 || linux[0].root != filepath.Join("/home/ana", ".local", "share", "citron") || linux[0].config != filepath.Join("/home/ana", ".config", "citron") {
		t.Errorf("linux: %+v", linux)
	}
	xdg := candidates(Eden, fakeEnv("linux", map[string]string{"XDG_DATA_HOME": "/data", "XDG_CONFIG_HOME": "/conf"}, "/home/ana", ""))
	if xdg[0].root != filepath.Join("/data", "eden") || xdg[0].config != filepath.Join("/conf", "eden") {
		t.Errorf("xdg: %+v", xdg)
	}
	mac := candidates(Eden, fakeEnv("darwin", map[string]string{}, "/Users/ana", ""))
	if got := roots(mac); len(got) != 2 || got[0] != filepath.Join("/Users/ana", ".local", "share", "eden") || got[1] != filepath.Join("/Users/ana", "Library", "Application Support", "eden") {
		t.Errorf("macOS: %v", got)
	}
}

// Find takes the first place with a NAND folder, a portable folder before the one of the system,
// and the NAND folder the settings name; nothing found is ErrNotFound.
func TestFind(t *testing.T) {
	appData, exe := t.TempDir(), t.TempDir()
	env := fakeEnv("windows", map[string]string{"APPDATA": appData}, "", exe)
	if _, err := find(Eden, env); err != ErrNotFound {
		t.Fatalf("nothing there: %v", err)
	}
	os.MkdirAll(filepath.Join(appData, "eden", "nand"), 0o755)
	found, err := find(Eden, env)
	if err != nil || found.Root != filepath.Join(appData, "eden") || found.Portable || found.Emulator != Eden {
		t.Fatalf("the folder of the system: %+v %v", found, err)
	}
	os.MkdirAll(filepath.Join(exe, "user", "nand"), 0o755)
	if found, _ := find(Eden, env); found.Root != filepath.Join(exe, "user") || !found.Portable {
		t.Fatalf("portable first: %+v", found)
	}
	// the NAND folder elsewhere, as the settings write it
	elsewhere := filepath.Join(t.TempDir(), "my nand")
	os.MkdirAll(elsewhere, 0o755)
	os.MkdirAll(filepath.Join(exe, "user", "config"), 0o755)
	os.WriteFile(filepath.Join(exe, "user", "config", "qt-config.ini"), []byte("[Data%20Storage]\nnand_directory\\default=false\nnand_directory=\""+filepath.ToSlash(elsewhere)+"\"\n"), 0o644)
	if found, _ := find(Eden, env); found.Nand != elsewhere {
		t.Fatalf("the NAND of the settings: %+v", found)
	}
	env.exeDir = ""
	if _, err := find(Citron, env); err != ErrNotFound {
		t.Fatalf("Citron is not there: %v", err)
	}
}

// Open takes the data folder or the NAND folder.
func TestOpen(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "nand", "user"), 0o755)
	if folder, err := Open(Eden, root); err != nil || folder.Nand != filepath.Join(root, "nand") {
		t.Fatalf("the data folder: %+v %v", folder, err)
	}
	if folder, err := Open(Eden, filepath.Join(root, "nand")); err != nil || folder.Root != root {
		t.Fatalf("the NAND folder: %+v %v", folder, err)
	}
	if _, err := Open(Eden, t.TempDir()); err == nil {
		t.Fatal("an empty folder")
	}
	if emulator, ok := ParseEmulator(" Citron "); !ok || emulator != Citron || emulator.Name() != "Citron" {
		t.Fatal("Citron")
	}
	if _, ok := ParseEmulator("ryujinx"); ok {
		t.Fatal("not a fork of yuzu")
	}
}
