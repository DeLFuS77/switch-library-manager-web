package edensync

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// a made-up UUID, its bytes from 0x01 to 0x10
var testId = ProfileID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}

// The UUID is written as Eden writes it: formatted and raw the first byte first, and the folder
// of the saves of yuzu the last byte first, in uppercase.
func TestProfileIDTexts(t *testing.T) {
	if got := testId.String(); got != "01020304-0506-0708-090a-0b0c0d0e0f10" {
		t.Errorf("formatted: %s", got)
	}
	if got := testId.Raw(); got != "0102030405060708090a0b0c0d0e0f10" {
		t.Errorf("raw: %s", got)
	}
	if got := testId.UserFolder(); got != "100F0E0D0C0B0A090807060504030201" {
		t.Errorf("folder of the saves: %s", got)
	}
	for _, text := range []string{testId.String(), testId.Raw(), "01020304-0506-0708-090A-0B0C0D0E0F10"} {
		if id, err := ParseProfileID(text); err != nil || id != testId {
			t.Errorf("%s: %v %v", text, id, err)
		}
	}
	for _, text := range []string{"", "nope", "00000000-0000-0000-0000-000000000000", "0102030405060708090a0b0c0d0e0f1", "../../etc"} {
		if _, err := ParseProfileID(text); err == nil {
			t.Errorf("%q is not a profile", text)
		}
	}
}

// profiles.dat is read back as written, without the empty entries, its names cut at 32 bytes
// without breaking a letter; a file too short is refused.
func TestProfilesFile(t *testing.T) {
	other := ProfileID{0xaa, 0xbb}
	written := []Profile{{ID: testId, Name: "Player"}, {ID: other, Name: strings.Repeat("a", 31) + "Ñandú"}}
	data := ProfilesFile(written)
	if len(data) != 0x650 {
		t.Fatalf("size %#x", len(data))
	}
	// the second UUID of the entry and the name where Eden has them
	if data[0x10+0x10] != 0x01 || string(data[0x10+0x28:0x10+0x2e]) != "Player" {
		t.Fatal("not where Eden has them")
	}
	read, err := ParseProfiles(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 || read[0] != written[0] || read[1].ID != other || read[1].Name != strings.Repeat("a", 31) {
		t.Fatalf("read: %+v", read)
	}
	// an empty entry between two is skipped
	data = ProfilesFile([]Profile{{ID: testId, Name: "A"}, {}, {ID: other, Name: "B"}})
	if read, _ := ParseProfiles(data); len(read) != 2 || read[1].Name != "B" {
		t.Fatalf("with a hole: %+v", read)
	}
	if _, err := ParseProfiles(data[:0x600]); err == nil {
		t.Fatal("a file too short")
	}
}

// fakeEden makes a data folder of Eden with these profiles.
func fakeEden(t *testing.T, profiles ...Profile) Folder {
	t.Helper()
	root := t.TempDir()
	folder := Folder{Emulator: Eden, Root: root, Nand: filepath.Join(root, "nand")}
	path := filepath.Join(folder.Nand, filepath.FromSlash(profilesPath))
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, ProfilesFile(profiles), 0o644); err != nil {
		t.Fatal(err)
	}
	return folder
}

// writeSave writes files in a folder of a save.
func writeSave(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(dir, 0o755)
}

// The save of a game of a profile is in the folder of yuzu, or in the newer one of Eden when that
// one is there; the games of a profile are the ones with a folder in either.
func TestSaveFolders(t *testing.T) {
	folder := fakeEden(t, Profile{ID: testId, Name: "Player"})
	profiles, err := folder.Profiles()
	if err != nil || len(profiles) != 1 || profiles[0].Name != "Player" {
		t.Fatalf("profiles: %+v %v", profiles, err)
	}
	legacy := filepath.Join(folder.Nand, "user", "save", "0000000000000000", "100F0E0D0C0B0A090807060504030201", "0100A1B2C3D40000")
	if got := folder.SaveFolder(testId, "0100a1b2c3d40000"); got != legacy {
		t.Fatalf("yuzu: %s", got)
	}
	writeSave(t, legacy, map[string]string{"data.bin": "x"})
	future := filepath.Join(folder.Nand, "user", "save", "account", "0102030405060708090a0b0c0d0e0f10", "0100F0E0D0C0B000", "0")
	writeSave(t, future, map[string]string{"data.bin": "y"})
	if got := folder.SaveFolder(testId, "0100F0E0D0C0B000"); got != future {
		t.Fatalf("newer: %s", got)
	}
	os.MkdirAll(filepath.Join(filepath.Dir(legacy), "not-a-game"), 0o755)
	games, err := folder.Games(testId)
	if err != nil || !reflect.DeepEqual(games, []string{"0100A1B2C3D40000", "0100F0E0D0C0B000"}) {
		t.Fatalf("games: %v %v", games, err)
	}
	if games, err := folder.Games(ProfileID{9}); err != nil || len(games) != 0 {
		t.Fatalf("a profile without saves: %v %v", games, err)
	}
}
