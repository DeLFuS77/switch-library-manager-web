package web

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// quarantineWeb is an app with a library folder and the quarantine kept as asked.
func quarantineWeb(t *testing.T, options settings.QuarantineOptions) (*Web, string) {
	t.Helper()
	web := newTestWeb(t)
	library := t.TempDir()
	appSettings := settings.ReadSettings(web.dataFolder)
	appSettings.Folder = library
	appSettings.Quarantine = options
	settings.SaveSettings(appSettings, web.dataFolder)
	return web, library
}

func writeGame(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("game"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestQuarantineOffDeletesAtOnce(t *testing.T) {
	web, library := quarantineWeb(t, settings.QuarantineOptions{})
	path := filepath.Join(library, "Zelda", "zelda [v1].nsp")
	writeGame(t, path)
	if err := web.discard(path, SPACE_OLD_UPDATES); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the file must be deleted")
	}
	if len(web.quarantine().list()) != 0 {
		t.Fatal("nothing goes to the quarantine when it is off")
	}
}

func TestQuarantineKeepsAndPutsBack(t *testing.T) {
	web, library := quarantineWeb(t, settings.QuarantineOptions{Keep: QUARANTINE_WEEK, LimitGB: 100})
	path := filepath.Join(library, "Zelda", "zelda [v1].nsp")
	writeGame(t, path)
	if err := web.discard(path, "old update file, newer update exist locally (x)"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the file must leave its folder")
	}
	items := web.quarantine().list()
	if len(items) != 1 || items[0].Reason != QUARANTINE_OLD_UPDATE || items[0].Path != path || items[0].Root != library {
		t.Fatalf("the quarantine: %+v", items)
	}
	if filepath.Dir(filepath.Dir(items[0].Stored)) != filepath.Join(library, QUARANTINE_FOLDER) {
		t.Fatalf("the file must wait in the hidden folder of its library folder: %s", items[0].Stored)
	}
	// kept by a later start of the app
	if loaded := loadQuarantine(web.dataFolder); len(loaded.items) != 1 {
		t.Fatal("the quarantine must be kept on disk")
	}

	// another file took its name meanwhile
	writeGame(t, path)
	if _, err := web.quarantine().restore(items[0].Id); !errors.Is(err, errQuarantineTaken) {
		t.Fatalf("a file with its name must not be written over: %v", err)
	}
	os.Remove(path)
	if _, err := web.quarantine().restore(items[0].Id); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "game" {
		t.Fatal("the file must be back in its folder")
	}
	if len(web.quarantine().list()) != 0 {
		t.Fatal("a file put back leaves the quarantine")
	}
	if _, err := os.Stat(filepath.Join(library, QUARANTINE_FOLDER)); !os.IsNotExist(err) {
		t.Fatal("the hidden folder goes once it is empty")
	}
}

func TestQuarantineExpiresDeletesAndEmpties(t *testing.T) {
	options := settings.QuarantineOptions{Keep: QUARANTINE_48H}
	web, library := quarantineWeb(t, options)
	paths := []string{filepath.Join(library, "a.nsp"), filepath.Join(library, "b.nsp"), filepath.Join(library, "c.nsp")}
	for _, path := range paths {
		writeGame(t, path)
		if err := web.discard(path, SPACE_DUPLICATES); err != nil {
			t.Fatal(err)
		}
	}
	items := web.quarantine().list()
	if len(items) != 3 {
		t.Fatalf("three files: %+v", items)
	}
	// the oldest was kept for longer than 48 hours
	q := web.quarantine()
	q.mutex.Lock()
	q.items[0].Time = time.Now().Add(-49 * time.Hour)
	expired := q.items[0]
	q.mutex.Unlock()
	if gone := q.expire(options, time.Now()); gone != 1 {
		t.Fatalf("one file expires: %d", gone)
	}
	if _, err := os.Stat(expired.Stored); !os.IsNotExist(err) {
		t.Fatal("an expired file is deleted")
	}
	left := q.list()
	if err := q.remove(left[0].Id); err != nil {
		t.Fatal(err)
	}
	if count, _ := q.empty(); count != 1 || len(q.list()) != 0 {
		t.Fatalf("emptied: %d", count)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("nothing goes back by itself")
		}
	}
}

func TestQuarantineNeverExpiresWhenKeptUntilEmptied(t *testing.T) {
	options := settings.QuarantineOptions{Keep: QUARANTINE_NEVER}
	web, library := quarantineWeb(t, options)
	path := filepath.Join(library, "a.nsp")
	writeGame(t, path)
	web.discard(path, SPACE_DUPLICATES)
	q := web.quarantine()
	q.mutex.Lock()
	q.items[0].Time = time.Now().AddDate(-2, 0, 0)
	q.mutex.Unlock()
	if gone := q.expire(options, time.Now()); gone != 0 {
		t.Fatal("kept until emptied: nothing expires")
	}
}

func TestQuarantineLimitDeletesTheOldest(t *testing.T) {
	q := &quarantineStore{path: filepath.Join(t.TempDir(), QUARANTINE_FILENAME)}
	now := time.Now()
	q.items = []QuarantineItem{
		{Id: "new", Size: 30 << 30, Time: now, Stored: filepath.Join(t.TempDir(), "x", "new.nsp")},
		{Id: "old", Size: 40 << 30, Time: now.Add(-time.Hour), Stored: filepath.Join(t.TempDir(), "x", "old.nsp")},
	}
	q.trim(50)
	if len(q.items) != 1 || q.items[0].Id != "new" {
		t.Fatalf("over 50 GB the oldest goes: %+v", q.items)
	}
}

// the originals of a compression go to the quarantine only when asked
func TestQuarantineOriginalsOnlyWhenAsked(t *testing.T) {
	web, library := quarantineWeb(t, settings.QuarantineOptions{Keep: QUARANTINE_WEEK})
	path := filepath.Join(library, "game.nsp")
	writeGame(t, path)
	if err := web.discardOriginal(path, QUARANTINE_COMPRESSED); err != nil {
		t.Fatal(err)
	}
	if len(web.quarantine().list()) != 0 {
		t.Fatal("an original goes at once unless the quarantine keeps originals")
	}

	appSettings := settings.ReadSettings(web.dataFolder)
	appSettings.Quarantine.Originals = true
	settings.SaveSettings(appSettings, web.dataFolder)
	writeGame(t, path)
	if err := web.discardOriginal(path, QUARANTINE_COMPRESSED); err != nil {
		t.Fatal(err)
	}
	if items := web.quarantine().list(); len(items) != 1 || items[0].Reason != QUARANTINE_COMPRESSED {
		t.Fatalf("the original is kept: %+v", items)
	}
}

// the watcher never sees the files of the quarantine (the scan neither, see the db package)
func TestWatcherSkipsTheQuarantine(t *testing.T) {
	web, library := quarantineWeb(t, settings.QuarantineOptions{Keep: QUARANTINE_WEEK})
	path := filepath.Join(library, "Zelda [01007EF00011E000][v0].nsp")
	writeGame(t, path)
	web.discard(path, SPACE_DUPLICATES)
	if _, err := os.Stat(filepath.Join(library, db.QUARANTINE_FOLDER)); err != nil {
		t.Fatal(err)
	}
	if fingerprint := folderFingerprint([]string{library}); fingerprint != folderFingerprint([]string{t.TempDir()}) {
		t.Fatal("the watcher must not see the files of the quarantine")
	}
}

func TestQuarantineReasons(t *testing.T) {
	for reason, want := range map[string]string{
		SPACE_OLD_UPDATES: QUARANTINE_OLD_UPDATE, SPACE_DUPLICATES: QUARANTINE_DUPLICATE, SPACE_COMPRESSED: QUARANTINE_COMPRESSED,
		"other region": QUARANTINE_REGION, "duplicate update file (x)": QUARANTINE_DUPLICATE, "something else": QUARANTINE_DELETED,
	} {
		if got := quarantineReason(reason); got != want {
			t.Errorf("%q: %s, want %s", reason, got, want)
		}
	}
	for reason := range quarantineReasons {
		if translate("es", quarantineReasons[reason]) == quarantineReasons[reason] {
			t.Errorf("%s: no Spanish text", reason)
		}
	}
}
