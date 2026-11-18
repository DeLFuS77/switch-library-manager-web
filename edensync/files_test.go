package edensync

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipOf makes a ZIP of these files.
func zipOf(t *testing.T, files map[string]string) *bytes.Reader {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write([]byte(content))
	}
	writer.Close()
	return bytes.NewReader(data.Bytes())
}

// readFolder reads the files of a folder, by their relative paths.
func readFolder(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			data, _ := os.ReadFile(path)
			files[filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
	return files
}

// The hash of a folder is the one of its ZIP, the same for the same files wherever they are,
// without the files of the emulator and of the system, and another for any other byte or name.
func TestHashFolderAndZip(t *testing.T) {
	dir := t.TempDir()
	writeSave(t, dir, map[string]string{"save.dat": "progress", "slot/1.bin": "one", ".yuzu_save_size": "size", "Thumbs.db": "x"})
	hash, err := HashFolder(dir)
	if err != nil || len(hash) != 64 {
		t.Fatalf("hash: %q %v", hash, err)
	}
	var zipped bytes.Buffer
	if err := ZipFolder(dir, &zipped); err != nil {
		t.Fatal(err)
	}
	archive, _ := zip.NewReader(bytes.NewReader(zipped.Bytes()), int64(zipped.Len()))
	names := []string{}
	for _, entry := range archive.File {
		names = append(names, entry.Name)
	}
	if strings.Join(names, ",") != "save.dat,slot/1.bin" {
		t.Fatalf("zipped: %v", names)
	}
	if fromZip, err := HashZip(bytes.NewReader(zipped.Bytes()), int64(zipped.Len())); err != nil || fromZip != hash {
		t.Fatalf("the ZIP: %q %v", fromZip, err)
	}
	other := t.TempDir()
	writeSave(t, other, map[string]string{"save.dat": "progress", "slot/1.bin": "one"})
	if again, _ := HashFolder(other); again != hash {
		t.Fatal("the same files elsewhere")
	}
	writeSave(t, other, map[string]string{"save.dat": "progresS"})
	if changed, _ := HashFolder(other); changed == hash {
		t.Fatal("a byte changed")
	}
	renamed := t.TempDir()
	writeSave(t, renamed, map[string]string{"save.dat": "progress", "slot/2.bin": "one"})
	if changed, _ := HashFolder(renamed); changed == hash {
		t.Fatal("a name changed")
	}
	if empty, err := HashFolder(filepath.Join(dir, "missing")); empty != "" || err != nil {
		t.Fatalf("a missing folder: %q %v", empty, err)
	}
	onlySize := t.TempDir()
	writeSave(t, onlySize, map[string]string{".yuzu_save_size": "x"})
	if empty, _ := HashFolder(onlySize); empty != "" {
		t.Fatal("a folder with only the size of the emulator has no save")
	}
	if err := ZipFolder(onlySize, &bytes.Buffer{}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("nothing to zip: %v", err)
	}
}

// A ZIP replaces a save folder: its files are there and the old ones gone, the size the emulator
// keeps stays, nothing is left next to it; a ZIP that climbs out, is empty or not a ZIP replaces
// nothing.
func TestReplaceFromZip(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "0100A1B2C3D40000")
	writeSave(t, dir, map[string]string{"old.dat": "old", ".yuzu_save_size": "size"})
	if err := ReplaceFromZip(dir, zipOf(t, map[string]string{"save.dat": "new", `sub\x.bin`: "x", "./y.bin": "y"}), int64(zipOf(t, map[string]string{"save.dat": "new", `sub\x.bin`: "x", "./y.bin": "y"}).Len())); err != nil {
		t.Fatal(err)
	}
	if got := readFolder(t, dir); len(got) != 4 || got["save.dat"] != "new" || got["sub/x.bin"] != "x" || got["y.bin"] != "y" || got[".yuzu_save_size"] != "size" {
		t.Fatalf("replaced: %v", got)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 1 {
		t.Fatalf("left next to it: %v", entries)
	}
	for name, bad := range map[string]*bytes.Reader{
		"climbs out": zipOf(t, map[string]string{"../evil.bin": "x"}),
		"absolute":   zipOf(t, map[string]string{"/evil.bin": "x"}),
		"a drive":    zipOf(t, map[string]string{"C:/evil.bin": "x"}),
		"empty":      zipOf(t, map[string]string{"Thumbs.db": "x"}),
		"twice":      zipOf(t, map[string]string{"a.bin": "1", "A.BIN": "2"}),
		"not a zip":  bytes.NewReader([]byte("nope")),
	} {
		if err := ReplaceFromZip(dir, bad, int64(bad.Len())); err == nil {
			t.Errorf("%s replaced the save", name)
		}
	}
	if got := readFolder(t, dir); got["save.dat"] != "new" || len(got) != 4 {
		t.Fatalf("a bad ZIP changed the save: %v", got)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 1 {
		t.Fatalf("a bad ZIP left folders: %v", entries)
	}
	// a folder that was not there is made
	fresh := filepath.Join(parent, "user", "0100F0E0D0C0B000")
	data := zipOf(t, map[string]string{"save.dat": "first"})
	if err := ReplaceFromZip(fresh, data, int64(data.Len())); err != nil || readFolder(t, fresh)["save.dat"] != "first" {
		t.Fatalf("a new folder: %v", err)
	}
}

// A replace cut between its two renames is put back: the save of before takes its place again,
// and the folder half written goes.
func TestRecover(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "0100A1B2C3D40000")
	writeSave(t, dir+oldSuffix+"aaaa", map[string]string{"save.dat": "before"})
	writeSave(t, dir+newSuffix+"bbbb", map[string]string{"save.dat": "half"})
	if err := Recover(dir); err != nil {
		t.Fatal(err)
	}
	if got := readFolder(t, dir); got["save.dat"] != "before" {
		t.Fatalf("put back: %v", got)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 1 {
		t.Fatalf("left: %v", entries)
	}
	if err := Recover(filepath.Join(parent, "missing", "x")); err != nil {
		t.Fatalf("nothing to recover: %v", err)
	}
}
