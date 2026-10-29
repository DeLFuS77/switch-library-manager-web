package db

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// A new data folder has no cache of an older version to rebuild: the log said so on the first
// start, and only a cache written by an older version is told.
func TestNewDataFolderIsNoOlderCache(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	defer zap.ReplaceGlobals(zap.New(core))()
	dataDir := t.TempDir()
	manager, err := NewLocalSwitchDBManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if older := logs.FilterMessageSnippet("older version").Len(); older != 0 {
		t.Fatalf("a new data folder: %v", logs.All())
	}
	if err := manager.db.SetInternalValue(DB_KEY_LIBRARY_SCHEMA, "1"); err != nil {
		t.Fatal(err)
	}
	manager.Close()
	if manager, err = NewLocalSwitchDBManager(dataDir); err != nil {
		t.Fatal(err)
	}
	manager.Close()
	if older := logs.FilterMessageSnippet("older version").Len(); older != 1 {
		t.Fatalf("a cache of an older version: %v", logs.All())
	}
}
