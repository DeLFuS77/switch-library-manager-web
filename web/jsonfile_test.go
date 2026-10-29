package web

import (
	"encoding/json"
	"errors"
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

// A store whose file is there but cannot be read starts empty, and is not saved over the file:
// once it reads again, it is saved as before. A file that does not exist is an empty store.
func TestStoreThatCannotBeReadIsNotWrittenOver(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "collections.json")
	if _, ok := readStore(missing); ok {
		t.Fatal("nothing to read")
	}
	if err := writeFileAtomic(missing, []byte("{}")); err != nil {
		t.Fatalf("a store that did not exist is saved: %v", err)
	}
	// a folder where the file should be cannot be read
	unread := filepath.Join(dir, "favorites.json")
	os.Mkdir(unread, 0o755)
	if _, ok := readStore(unread); ok {
		t.Fatal("a folder is not read")
	}
	if err := writeFileAtomic(unread, []byte("{}")); !errors.Is(err, errStoreUnread) {
		t.Fatalf("not saved over what could not be read: %v", err)
	}
	os.Remove(unread)
	os.WriteFile(unread, []byte("{}"), 0o600)
	if _, ok := readStore(unread); !ok {
		t.Fatal("read once it can be")
	}
	if err := writeFileAtomic(unread, []byte(`{"0100000000010000":"2026-10-29T00:00:00Z"}`)); err != nil {
		t.Fatalf("saved again once read: %v", err)
	}
}
