package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// The keys a tool is given on its command line: a keys.txt, or a folder holding one, with the
// title.keys next to it.
func TestUseSwitchKeys(t *testing.T) {
	folder := t.TempDir()
	os.WriteFile(filepath.Join(folder, "keys.txt"), []byte("header_key = 00112233\n"), 0o600)
	os.WriteFile(filepath.Join(folder, "title.keys"), []byte("0100ABCD000000000000000000000000 = 99\n"), 0o600)
	for _, path := range []string{folder, filepath.Join(folder, "keys.txt")} {
		keysInstance.Store(nil)
		if err := UseSwitchKeys(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		keys, _ := SwitchKeys()
		if keys == nil || keys.GetKey("header_key") != "00112233" {
			t.Fatalf("%s: the keys are not active", path)
		}
		if key, ok := keys.TitleKey("0100abcd000000000000000000000000"); !ok || key != "99" {
			t.Fatalf("%s: the title key: %q %v", path, key, ok)
		}
	}
	if err := UseSwitchKeys(filepath.Join(folder, "missing.keys")); err == nil {
		t.Fatal("a missing file is an error")
	}
}
