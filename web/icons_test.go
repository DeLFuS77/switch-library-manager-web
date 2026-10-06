package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestFilesWithoutAnIconAreNotReadAgain(t *testing.T) {
	web := newTestWeb(t)
	testnsp.WriteKeys(web.dataFolder)
	if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { settings.InitSwitchKeys(t.TempDir()) })

	folder := t.TempDir()
	// a game file without control content, so without an icon
	game := filepath.Join(folder, "Game [0100000000010000][v0].nsp")
	if _, err := testnsp.WriteNsp(game); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(game)
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": {BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(game), BaseFolder: folder, Size: info.Size()}}},
	}})
	if web.missingCovers() != 1 {
		t.Fatal("the game has no cover")
	}

	if found := web.extractMissingIcons(0); found != 0 {
		t.Fatalf("no icon can be found: %d", found)
	}
	if web.readNoIcon()[game] == "" {
		t.Fatal("the file without an icon is remembered")
	}
	tasks := len(web.taskLog().Snapshot())
	web.extractMissingIcons(0)
	if len(web.taskLog().Snapshot()) != tasks {
		t.Fatal("a file without an icon is not read again after every scan")
	}

	// "Search covers again" forgets it
	web.HandleCovers()
	if response := postForm(web, "/covers/retry", url.Values{}); response.Code != http.StatusAccepted {
		t.Fatalf("retry: %d", response.Code)
	}
	if len(web.readNoIcon()) != 0 {
		t.Fatal("the files without an icon are tried again")
	}
	for deadline := 0; web.covers.running && deadline < 200; deadline++ {
		waitShort()
	}
}

func waitShort() { <-time.After(20 * time.Millisecond) }
