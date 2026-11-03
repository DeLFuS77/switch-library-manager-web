package activity

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// The log keeps the newest action first, and reads the same again from its file.
func TestLogKeepsTheNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), ACTIVITY_FILENAME)
	log := Load(path)
	log.Add(Entry{Time: time.Now(), User: "ana", Action: ACTION_SETTINGS})
	log.Add(Entry{Time: time.Now(), User: "luis", Action: ACTION_ORGANIZE})
	entries := log.List()
	if len(entries) != 2 || entries[0].User != "luis" || entries[1].User != "ana" {
		t.Fatalf("newest first: %+v", entries)
	}
	if again := Load(path).List(); len(again) != 2 || again[0].Action != ACTION_ORGANIZE {
		t.Fatalf("read again: %+v", again)
	}
	if preview := Preview(entries, 1); len(preview) != 1 || preview[0].User != "luis" {
		t.Fatalf("the preview: %+v", preview)
	}
}

// A flood of failed sign-ins keeps only the newest of them, and pushes no other action out.
func TestFailedSignInsAreKeptFew(t *testing.T) {
	log := Load(filepath.Join(t.TempDir(), ACTIVITY_FILENAME))
	log.Add(Entry{Time: time.Now(), User: "ana", Action: ACTION_SETTINGS})
	for i := 0; i < MaxFailedSignIns+20; i++ {
		log.Add(Entry{Time: time.Now(), User: fmt.Sprint("bot", i), Action: ACTION_LOGIN_FAIL})
	}
	entries := log.List()
	if len(entries) != MaxFailedSignIns+1 || entries[len(entries)-1].Action != ACTION_SETTINGS {
		t.Fatalf("%d entries, the last %+v", len(entries), entries[len(entries)-1])
	}
	if entries[0].User != fmt.Sprint("bot", MaxFailedSignIns+19) {
		t.Fatalf("the newest failure first: %+v", entries[0])
	}
}
