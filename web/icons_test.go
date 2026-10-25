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

	// "Search covers again" forgets it: the file is read again. Checked by the task that reads
	// it, as the search runs in the background and remembers the file again once it is read.
	web.HandleCovers()
	before := len(web.taskLog().Snapshot())
	if response := postForm(web, "/covers/retry", url.Values{}); response.Code != http.StatusAccepted {
		t.Fatalf("retry: %d", response.Code)
	}
	for deadline := 0; (coversRunning(web) || len(web.taskLog().Snapshot()) == before) && deadline < 200; deadline++ {
		waitShort()
	}
	if len(web.taskLog().Snapshot()) == before {
		t.Fatal("the files without an icon are tried again")
	}
}

func coversRunning(web *Web) bool {
	web.covers.mutex.Lock()
	defer web.covers.mutex.Unlock()
	return web.covers.running
}

func waitShort() { <-time.After(20 * time.Millisecond) }

// The icon read from a game file comes back when a scan makes the library again, without
// reading the file again or a task of covers after every scan.
func TestReadIconsComeBackAfterAScan(t *testing.T) {
	web := newTestWeb(t)
	folder := t.TempDir()
	game := filepath.Join(folder, "Game [0100000000010000][v0].nsp")
	if err := os.WriteFile(game, []byte("game"), 0644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(web.dataFolder, "img"), 0755)
	if err := os.WriteFile(filepath.Join(web.dataFolder, "img", extractedIconName("0100000000010")), []byte("icon"), 0644); err != nil {
		t.Fatal(err)
	}
	// the library as a scan makes it: the game has no cover
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": {BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(game), BaseFolder: folder, Size: 4}}},
	}})
	coverTasks := func() int {
		count := 0
		for _, task := range web.taskLog().Snapshot() {
			if task.Kind == TASK_COVERS {
				count++
			}
		}
		return count
	}

	web.downloadMissingCovers()
	if _, localDB := web.state.get(); localDB.TitlesMap["0100000000010"].Icon != extractedIconName("0100000000010") {
		t.Fatalf("the icon read before comes back: %+v", localDB.TitlesMap["0100000000010"])
	}
	if coverTasks() != 0 {
		t.Fatalf("no task of covers is made for it: %+v", web.taskLog().Snapshot())
	}
}
