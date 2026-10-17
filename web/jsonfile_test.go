package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A file of the data folder that cannot be read is set aside, not written over empty.
func TestCorruptStoreIsKeptAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "favorites.json")
	os.WriteFile(path, []byte(`{"0100000000010000": "2026-`), 0600)
	games := map[string]string{}
	if err := parseStore(path, []byte(`{"0100000000010000": "2026-`), &games); err == nil {
		t.Fatal("the file cannot be read")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "favorites.json.corrupt-") {
		t.Fatalf("kept aside: %v", entries)
	}
}

// Saves at the same time never mix: the file is always one of them, whole.
func TestAtomicWritesDoNotMix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verify.json")
	var wait sync.WaitGroup
	for i := 0; i < 20; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			data, _ := json.Marshal(map[string]string{"value": strings.Repeat(string(rune('a'+i)), 100000)})
			if err := writeFileAtomic(path, data); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wait.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]string{}
	if err := json.Unmarshal(data, &value); err != nil || len(value["value"]) != 100000 {
		t.Fatalf("one whole save: %v %d", err, len(value["value"]))
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("no temporary file left: %v", entries)
	}
}
