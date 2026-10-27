package web

import (
	"fmt"
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

// The view by covers shows every file of a running task: ended, at work and waiting.
func TestTaskPlanShowsEveryFile(t *testing.T) {
	log := newTestWeb(t).taskLog()
	id := log.Start(TASK_VERIFY, TRIGGER_MANUAL)
	log.SetPlan(id, []string{"A.nsp", "B.nsp", "C.nsp", "D.nsp"})
	log.AddFileTile(id, "A.nsp", false, "")
	log.AddFileTile(id, "B.nsp", true, NOTE_VERIFY_DAMAGED)
	task := log.Snapshot()[0]
	shown, more := shownPlan(task.Plan, []TaskLane{{Name: "C.nsp", Done: 1, Total: 4}}, map[string]string{"C.nsp": "/t/c.jpg"})
	if more != 0 || len(shown) != 4 {
		t.Fatalf("every file: %+v, %d more", shown, more)
	}
	if shown[0].Waiting || shown[0].Failed || !shown[1].Failed || shown[1].Note != NOTE_VERIFY_DAMAGED {
		t.Fatalf("the files ended: %+v", shown[:2])
	}
	if !shown[2].Working || shown[2].Percent != 25 || shown[2].Cover != "/t/c.jpg" || !shown[3].Waiting || shown[3].Working {
		t.Fatalf("at work and waiting: %+v", shown[2:])
	}
	log.Finish(id, nil)
	if log.Snapshot()[0].Plan != nil {
		t.Fatal("an ended task keeps no plan")
	}

	// a long plan: a few files ended, then those at work and waiting, and how many are left out
	names := make([]string, 1000)
	for i := range names {
		names[i] = fmt.Sprintf("%04d.nsp", i)
	}
	id = log.Start(TASK_VERIFY, TRIGGER_MANUAL)
	log.SetPlan(id, names)
	for _, name := range names[:500] {
		log.AddFileTile(id, name, false, "")
	}
	shown, more = shownPlan(log.Snapshot()[0].Plan, nil, nil)
	if len(shown) != maxShownTiles || shown[0].Name != "0476.nsp" || more != 1000-476-maxShownTiles {
		t.Fatalf("a long plan: from %s, %d shown, %d more", shown[0].Name, len(shown), more)
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
