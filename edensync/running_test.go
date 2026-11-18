package edensync

import (
	"runtime"
	"testing"
)

func TestIsEmulatorProcess(t *testing.T) {
	for name, want := range map[string]bool{
		"eden.exe": true, "Eden.exe": true, "eden": true, "eden-cli": true, "eden-cmd.exe": true,
		"Eden-Linux-v0.0.3-amd64.AppImage": true, `C:\Emu\eden.exe`: true, "/usr/bin/eden": true,
		"eden-room": false, "eden-room.exe": false, "edenia.exe": false, "garden.exe": false, "citron.exe": false, "": false,
	} {
		if got := IsEmulatorProcess(Eden, name); got != want {
			t.Errorf("%q: %v", name, got)
		}
	}
	if !IsEmulatorProcess(Citron, "citron-cmd") || IsEmulatorProcess(Citron, "eden") {
		t.Error("Citron")
	}
}

// The programs that run can be read where the package works, and the test itself is one of them.
func TestProcessNames(t *testing.T) {
	names, err := processNames()
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		if err != ErrCannotTell {
			t.Fatalf("an unknown system: %v", err)
		}
		return
	}
	if err != nil || len(names) == 0 {
		t.Fatalf("names: %d %v", len(names), err)
	}
	if _, err := Running(Eden); err != nil {
		t.Fatal(err)
	}
}
