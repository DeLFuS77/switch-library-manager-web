package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// A temporary file of the app left for a while is removed; one being written, and files of
// other programs, stay.
func TestLeftoversAreRemoved(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	old := filepath.Join(dir, ".Game [0100000000010000][v0].nsz.tmp")
	fresh := filepath.Join(dir, ".Other [0100000000020000][v0].nsz.tmp")
	foreign := filepath.Join(dir, "notes.tmp")
	for _, path := range []string{old, fresh, foreign} {
		os.WriteFile(path, make([]byte, 100), 0644)
	}
	past := time.Now().Add(-2 * leftoverAge)
	os.Chtimes(old, past, past)
	os.Chtimes(foreign, past, past)
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{},
		Leftovers: []db.LeftoverFile{{Path: old}, {Path: fresh}},
	})
	web.cleanLeftovers()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("the old temporary file stays")
	}
	for _, path := range []string{fresh, foreign} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s was removed", filepath.Base(path))
		}
	}
	if entries := web.activities().list(); len(entries) != 1 || entries[0].Action != ACTION_LEFTOVERS {
		t.Fatalf("activity: %+v", entries)
	}
}
