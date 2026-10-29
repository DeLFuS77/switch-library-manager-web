package web

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/process"
)

// A file moved by Organize is found where it went, also after it was moved again; a move that
// failed is not followed.
func TestMovedFilesFollowOrganize(t *testing.T) {
	var moves movedFiles
	if moves.resolve("/games/a.nsp") != "/games/a.nsp" {
		t.Fatal("nothing moved: the same path")
	}
	moves.record([]process.Operation{
		{Kind: process.OP_MOVE, From: "/games/a.nsp", To: "/games/A [0100].nsp"},
		{Kind: process.OP_MOVE, From: "/games/b.nsp", To: "/games/B.nsp", Error: "exists"},
		{Kind: process.OP_DELETE, From: "/games/c.nsp"},
	})
	moves.record([]process.Operation{{Kind: process.OP_MOVE, From: "/games/A [0100].nsp", To: "/games/A/A [0100].nsp"}})
	for from, want := range map[string]string{
		"/games/a.nsp":          "/games/A/A [0100].nsp",
		"/games/A [0100].nsp":   "/games/A/A [0100].nsp",
		"/games/b.nsp":          "/games/b.nsp",
		"/games/c.nsp":          "/games/c.nsp",
		"/games/A/A [0100].nsp": "/games/A/A [0100].nsp",
	} {
		if got := moves.resolve(from); got != want {
			t.Errorf("%s: %s, want %s", from, got, want)
		}
	}
	// a file moved back and forth does not loop
	moves.record([]process.Operation{{Kind: process.OP_MOVE, From: "/x", To: "/y"}, {Kind: process.OP_MOVE, From: "/y", To: "/x"}})
	moves.resolve("/x")
}

// A page drawn before Organize sends the old paths, and the library may still list them or
// already list the new ones: both give the file where it is now, once, and nothing else.
func TestLibraryPathsFollowMovedFiles(t *testing.T) {
	web := &Web{}
	web.moves.record([]process.Operation{{Kind: process.OP_MOVE, From: "/games/old.nsp", To: "/games/New [0100].nsp"}})
	for _, candidates := range [][]CompressCandidate{
		{{Path: "/games/old.nsp"}, {Path: "/games/other.nsp"}},
		{{Path: "/games/New [0100].nsp"}, {Path: "/games/other.nsp"}},
	} {
		paths := web.libraryPaths([]string{"/games/old.nsp", "/games/New [0100].nsp", "/etc/passwd"}, candidates)
		if len(paths) != 1 || paths[0] != "/games/New [0100].nsp" {
			t.Fatalf("candidates %v: %v", candidates, paths)
		}
	}
}

// A task started with the old path of a file moved by Organize works on the file where it is
// now; a file no longer there is told as such, and the job is not run for it.
func TestFileTasksFindMovedFiles(t *testing.T) {
	web, _ := compressWeb(t)
	folder := t.TempDir()
	moved := filepath.Join(folder, "Game [0100000000010000][v0].nsp")
	os.WriteFile(moved, make([]byte, 100), 0o644)
	old := filepath.Join(folder, "game.nsp")
	web.moves.record([]process.Operation{{Kind: process.OP_MOVE, From: old, To: moved}})

	var mutex sync.Mutex
	got := []string{}
	job := func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		mutex.Lock()
		got = append(got, path)
		mutex.Unlock()
		return 0, nil
	}
	if !web.runFileTask(TASK_COMPRESS, []string{old}, 1, job) {
		t.Fatal("not started")
	}
	waitForCompression(t, web)
	if len(got) != 1 || got[0] != moved {
		t.Fatalf("worked on %v, want %s", got, moved)
	}

	got = got[:0]
	gone := filepath.Join(folder, "deleted.nsp")
	if !web.runFileTask(TASK_COMPRESS, []string{gone}, 1, job) {
		t.Fatal("not started")
	}
	waitForCompression(t, web)
	if len(got) != 0 {
		t.Fatalf("the job ran for a file that is not there: %v", got)
	}
	var last Task
	for _, task := range web.taskLog().Snapshot() {
		if task.Kind == TASK_COMPRESS && task.Id > last.Id {
			last = task
		}
	}
	if last.Error == nil || last.Error.Text != NOTE_FILE_GONE {
		t.Fatalf("the task tells %+v", last.Error)
	}
}

// A path holds the file now there: a new file under the old name of one Organize moved is
// that new file, a move to a path forgets the move of the file that was there before, and a
// file moved back is where it went last.
func TestMovedFilesKnowWhatIsThereNow(t *testing.T) {
	dir := t.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }
	write := func(name string) {
		os.WriteFile(path(name), []byte(name), 0o644)
	}
	var moves movedFiles
	// Game.nsp went into its folder, and a new download came in under the same name
	write("Game [0100].nsp")
	moves.record([]process.Operation{{Kind: process.OP_MOVE, From: path("Game.nsp"), To: path("Game [0100].nsp")}})
	if got := moves.resolve(path("Game.nsp")); got != path("Game [0100].nsp") {
		t.Fatalf("the file moved: %s", got)
	}
	write("Game.nsp")
	if got := moves.resolve(path("Game.nsp")); got != path("Game.nsp") {
		t.Fatalf("the new file at the old path is that file: %s", got)
	}

	// B went to C, then A to B: A is at B, B is the file now there
	moves = movedFiles{}
	write("B")
	moves.record([]process.Operation{{Kind: process.OP_MOVE, From: path("B"), To: path("C")}})
	moves.record([]process.Operation{{Kind: process.OP_MOVE, From: path("A"), To: path("B")}})
	if got := moves.resolve(path("A")); got != path("B") {
		t.Fatalf("A is at B, not where B went before: %s", got)
	}
	if _, ok := moves.to[path("B")]; ok {
		t.Fatal("the move of the file that was at B is forgotten")
	}
	// A went to B and back: it is at A, and B goes there too
	moves = movedFiles{}
	write("A")
	moves.record([]process.Operation{{Kind: process.OP_MOVE, From: path("A"), To: path("B2")}})
	moves.record([]process.Operation{{Kind: process.OP_MOVE, From: path("B2"), To: path("A")}})
	if got, back := moves.resolve(path("B2")), moves.resolve(path("A")); got != path("A") || back != path("A") {
		t.Fatalf("moved back: %s %s", got, back)
	}

	// the very file under its old name (a name changed in its case only) is followed
	write("Same [0100].nsp")
	if err := os.Link(path("Same [0100].nsp"), path("same.nsp")); err == nil {
		moves.record([]process.Operation{{Kind: process.OP_MOVE, From: path("same.nsp"), To: path("Same [0100].nsp")}})
		if got := moves.resolve(path("same.nsp")); got != path("Same [0100].nsp") {
			t.Fatalf("the same file: %s", got)
		}
	}

	// the scan that follows knows the new paths: the moves before it are forgotten
	moves.forget(time.Now().Add(time.Second))
	if len(moves.to) != 0 {
		t.Fatalf("forgotten after the scan: %v", moves.to)
	}
}

// The scan after Organize forgets its moves, once the tasks waiting in the queue took the new
// paths of their files.
func TestTheScanAfterOrganizeForgetsTheMoves(t *testing.T) {
	web := newTestWeb(t)
	folder := t.TempDir()
	old, moved := filepath.Join(folder, "game.nsp"), filepath.Join(folder, "Game [0100000000010000][v0].nsp")
	os.WriteFile(moved, []byte("game"), 0o644)
	web.queue().add(QueuedTask{Kind: TASK_PACK, Paths: []string{old}, Names: map[string]string{old: "Game.nsp"}})
	web.moves.record([]process.Operation{{Kind: process.OP_MOVE, From: old, To: moved}})
	web.scanStarted.Store(time.Now().Add(time.Second).UnixNano())
	web.afterScan()
	web.afterScanWork.Wait()
	if web.moves.resolve(old) != old {
		t.Fatal("the moves are forgotten")
	}
	queued := web.queue().list()
	if len(queued) != 1 || queued[0].Paths[0] != moved || queued[0].Names[moved] != "Game.nsp" {
		t.Fatalf("the task waiting has the new path: %+v", queued)
	}
	if reloaded := loadTaskQueue(filepath.Join(web.dataFolder, "queue.json")).list(); reloaded[0].Paths[0] != moved {
		t.Fatal("and keeps it")
	}
}
