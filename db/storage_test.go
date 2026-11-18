package db

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// The scan notes how each file keeps its NCA files (what the page of a game and the filter of the
// Library by emulators tell). A file read by a version before it is not read whole again: only the
// headers of its NCZ, and its data is cached again with it.
func TestScanNotesHowFilesAreStored(t *testing.T) {
	keysDir := t.TempDir()
	if err := testnsp.WriteKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	defer settings.InitSwitchKeys(t.TempDir())

	folder := t.TempDir()
	path := filepath.Join(folder, "Game [0100000000010000][v0].nsp")
	if _, err := testnsp.WriteMultiTitleNsp(path, []testnsp.Title{
		{Id: 0x0100000000010000, Type: switchfs.ContentMetaType_Application, Content: "the game "},
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewLocalSwitchDBManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	scan := func() *switchfs.ContentMetaAttributes {
		t.Helper()
		localDB, err := manager.CreateLocalSwitchFilesDB(nil, t.TempDir(), []string{folder}, nil, true, true)
		if err != nil {
			t.Fatal(err)
		}
		game := localDB.TitlesMap["0100000000010"]
		if game == nil || game.File.Metadata == nil {
			t.Fatalf("the game is not in the library: %+v", localDB.TitlesMap)
		}
		return game.File.Metadata
	}
	if meta := scan(); meta.Storage != switchfs.StoragePlain {
		t.Fatalf("an NSP: %q", meta.Storage)
	}

	// the data cached by a version before: without it, and with a mark that a whole read would lose
	info, _ := os.Stat(path)
	key := path + "|" + filepath.Base(path) + "|" + strconv.Itoa(int(info.Size()))
	var cached map[string]*switchfs.ContentMetaAttributes
	if err := manager.db.GetEntry(DB_TABLE_FILE_SCAN_METADATA, key, &cached); err != nil || len(cached) != 1 {
		t.Fatalf("the cache of the file: %v %+v", err, cached)
	}
	for _, meta := range cached {
		if meta.Storage != switchfs.StoragePlain {
			t.Fatalf("cached without it: %+v", meta)
		}
		meta.Storage = ""
		meta.RequiredTitleVersion = 7
	}
	if err := manager.db.AddEntry(DB_TABLE_FILE_SCAN_METADATA, key, cached); err != nil {
		t.Fatal(err)
	}
	if meta := scan(); meta.Storage != switchfs.StoragePlain || meta.RequiredTitleVersion != 7 {
		t.Fatalf("noted from the headers alone: %+v", meta)
	}
	cached = nil
	if err := manager.db.GetEntry(DB_TABLE_FILE_SCAN_METADATA, key, &cached); err != nil {
		t.Fatal(err)
	}
	for _, meta := range cached {
		if meta.Storage != switchfs.StoragePlain || meta.RequiredTitleVersion != 7 {
			t.Fatalf("cached again with it: %+v", meta)
		}
	}
}
