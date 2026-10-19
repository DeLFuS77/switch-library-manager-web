package web

import (
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A running task on files shows the cover of the game of each file and the next file.
func TestTaskCoversAndNextFile(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	game := func(name string, icon string) *db.SwitchGameFiles {
		return &db.SwitchGameFiles{BaseExist: true, Icon: icon, File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: name, BaseFolder: dir}, Metadata: &switchfs.ContentMetaAttributes{}},
			Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": game("First.nsp", "first.jpg"),
		"0100000000020": game("Second.nsp", "second.jpg"),
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})

	lanes := []TaskLane{{Name: "First.nsp"}}
	task := Task{Lanes: lanes, Resume: &TaskResume{Paths: []string{filepath.Join(dir, "First.nsp"), filepath.Join(dir, "Second.nsp")}}}
	shown := web.withCovers(task)
	if shown.Lanes[0].Cover != "/t/first.jpg" {
		t.Fatalf("the cover of the file worked on: %+v", shown.Lanes)
	}
	if lanes[0].Cover != "" {
		t.Fatal("the lanes of the running task are not changed")
	}
	if shown.Next == nil || shown.Next.Name != "Second.nsp" || shown.Next.Cover != "/t/second.jpg" {
		t.Fatalf("the next file: %+v", shown.Next)
	}
	task.Paused = true
	if web.withCovers(task).Next != nil {
		t.Fatal("a paused task takes no next file")
	}
	if web.withCovers(Task{}).Next != nil {
		t.Fatal("a task without files has none")
	}
}

// A long task shows how far it is before its first whole percent.
func TestTaskPercentExact(t *testing.T) {
	cases := map[[2]int]string{{8, 1000}: "0.80", {0, 1000}: "0.00", {1000, 1000}: "100", {5, 0}: "0", {3, 2}: "100"}
	for c, want := range cases {
		if got := (Task{Current: c[0], Total: c[1]}).PercentExact(); got != want {
			t.Errorf("%d of %d: %s, want %s", c[0], c[1], got, want)
		}
	}
}
