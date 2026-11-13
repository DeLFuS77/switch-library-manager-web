package process

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// moved writes a file at to as Organize left it, and gives its move from from.
func moved(t *testing.T, from string, to string, data string) UndoMove {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(to)
	if err != nil {
		t.Fatal(err)
	}
	return UndoMove{From: from, To: to, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
}

func content(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(data)
}

// The look before an undo tells each file as it stands, and changes nothing.
func TestCheckUndoTellsEachFile(t *testing.T) {
	root := t.TempDir()
	back := moved(t, filepath.Join(root, "loose", "a.nsp"), filepath.Join(root, "A", "A [0100].nsp"), "aaaa")

	taken := moved(t, filepath.Join(root, "b.nsp"), filepath.Join(root, "B", "B [0100].nsp"), "bbbb")
	os.WriteFile(taken.From, []byte("another file"), 0o644)

	bigger := moved(t, filepath.Join(root, "c.nsp"), filepath.Join(root, "C", "C.nsp"), "cccc")
	os.WriteFile(bigger.To, []byte("cccc and more"), 0o644)

	touched := moved(t, filepath.Join(root, "d.nsp"), filepath.Join(root, "D", "D.nsp"), "dddd")
	later := time.Now().Add(time.Hour)
	os.Chtimes(touched.To, later, later)

	gone := moved(t, filepath.Join(root, "e.nsp"), filepath.Join(root, "E", "E.nsp"), "eeee")
	os.Remove(gone.To)

	// put back already by an undo the app was stopped in the middle of
	done := moved(t, filepath.Join(root, "f.nsp"), filepath.Join(root, "F", "F.nsp"), "ffff")
	os.Rename(done.To, done.From)

	for move, want := range map[UndoMove]string{back: UNDO_BACK, taken: UNDO_TAKEN, bigger: UNDO_CHANGED_SIZE, touched: UNDO_CHANGED_DATE, gone: UNDO_GONE, done: UNDO_DONE} {
		if got := CheckUndo(move); got != want {
			t.Errorf("%s: %s, want %s", move.To, got, want)
		}
	}
	if _, err := os.Stat(back.To); err != nil {
		t.Fatal("the look must not move anything")
	}
	if content(t, taken.From) != "another file" || content(t, taken.To) != "bbbb" {
		t.Fatal("the look must not change anything")
	}
}

// A file goes back to its name and folder of before, the folder made again when it is missing;
// one that cannot go back stays as it is.
func TestPutBackMovesTheFileBack(t *testing.T) {
	root := t.TempDir()
	move := moved(t, filepath.Join(root, "gone folder", "deep", "a.nsz"), filepath.Join(root, "A", "A [0100][v0].nsz"), "game")
	if state, err := PutBack(move, nil); state != UNDO_BACK || err != nil {
		t.Fatalf("%s %v", state, err)
	}
	if content(t, move.From) != "game" {
		t.Fatal("the file must be at its path of before")
	}
	if _, err := os.Stat(move.To); !os.IsNotExist(err) {
		t.Fatal("the file must have left its path of after")
	}
	info, _ := os.Stat(move.From)
	if info.ModTime().UnixNano() != move.ModTime {
		t.Fatal("the file keeps its date")
	}
	// again: it is back already, nothing to do
	if state, err := PutBack(move, nil); state != UNDO_DONE || err != nil {
		t.Fatalf("again: %s %v", state, err)
	}
}

// Nothing is written over: not a file at the path of before when it is looked at, nor one that
// comes there between the look and the move, on the same disk or between two.
func TestPutBackNeverWritesOver(t *testing.T) {
	root := t.TempDir()
	move := moved(t, filepath.Join(root, "a.nsp"), filepath.Join(root, "A", "a.nsp"), "the game")
	os.WriteFile(move.From, []byte("another"), 0o644)
	if state, _ := PutBack(move, nil); state != UNDO_TAKEN {
		t.Fatalf("a file at the path of before: %s", state)
	}
	if content(t, move.From) != "another" || content(t, move.To) != "the game" {
		t.Fatal("both files must stay as they were")
	}

	for _, across := range []bool{false, true} {
		folder := t.TempDir()
		move := moved(t, filepath.Join(folder, "b.nsp"), filepath.Join(folder, "B", "b.nsp"), "the game")
		came := func(from, to string) {
			// a file comes at the path of before right after the look
			if to == move.From {
				os.WriteFile(move.From, []byte("came"), 0o644)
			}
		}
		if across {
			acrossDisks(t, came)
		} else {
			t.Cleanup(func() { rename = renameNoReplace })
			rename = func(from, to string) error {
				came(from, to)
				return renameNoReplace(from, to)
			}
		}
		if state, _ := PutBack(move, nil); state != UNDO_TAKEN {
			t.Fatalf("across disks %v: %s", across, state)
		}
		if content(t, move.From) != "came" || content(t, move.To) != "the game" {
			t.Fatalf("across disks %v: both files must stay as they were", across)
		}
		rename = renameNoReplace
	}
}

// Between two disks the file is copied back, checked, and only then deleted where it was.
func TestPutBackBetweenDisks(t *testing.T) {
	root := t.TempDir()
	move := moved(t, filepath.Join(root, "old", "a.nsp"), filepath.Join(root, "A", "a.nsp"), "the whole game")
	acrossDisks(t, nil)
	if state, err := PutBack(move, nil); state != UNDO_BACK || err != nil {
		t.Fatalf("%s %v", state, err)
	}
	if content(t, move.From) != "the whole game" {
		t.Fatal("the copy must be whole")
	}
	if _, err := os.Stat(move.To); !os.IsNotExist(err) {
		t.Fatal("the original goes once copied")
	}
}

// A file whose name only changed in its case goes back to it, on a disk that takes names in any
// case.
func TestPutBackTheCaseOfAName(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("names in any case are of Windows and macOS")
	}
	root := t.TempDir()
	move := moved(t, filepath.Join(root, "game.nsp"), filepath.Join(root, "Game.nsp"), "data")
	if state, err := PutBack(move, nil); state != UNDO_BACK || err != nil {
		t.Fatalf("%s %v", state, err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "game.nsp" {
		t.Fatalf("%v", entries)
	}
}

// Only the folders Organize made that are empty go, the deepest first; one with anything in it,
// even a file of the system, stays.
func TestUndoFoldersDeletesOnlyEmptyOnes(t *testing.T) {
	root := t.TempDir()
	game := filepath.Join(root, "Game")
	updates := filepath.Join(game, "Updates")
	kept := filepath.Join(root, "Other")
	os.MkdirAll(updates, 0o755)
	os.MkdirAll(kept, 0o755)
	os.WriteFile(filepath.Join(kept, "Thumbs.db"), []byte("x"), 0o644)
	deleted := UndoFolders([]string{game, updates, kept, filepath.Join(root, "never made")})
	if len(deleted) != 2 || deleted[0] != updates || deleted[1] != game {
		t.Fatalf("deleted %v", deleted)
	}
	if _, err := os.Stat(filepath.Join(kept, "Thumbs.db")); err != nil {
		t.Fatal("a folder with a file stays, with it")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("the folders above stay")
	}
}

// Organize tells the first folder it made for a folder, so the undo knows each one it made and
// none it did not.
func TestOrganizeTellsTheFoldersItMade(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "Games"), 0o755)
	ops := newFileOperations(false)
	if err := ops.mkdir(filepath.Join(root, "Games", "Game", "DLC")); err != nil {
		t.Fatal(err)
	}
	if len(ops.operations) != 1 {
		t.Fatalf("%+v", ops.operations)
	}
	made := MadeFolders(ops.operations[0])
	want := []string{filepath.Join(root, "Games", "Game", "DLC"), filepath.Join(root, "Games", "Game")}
	if len(made) != 2 || made[0] != want[0] || made[1] != want[1] {
		t.Fatalf("made %v, want %v", made, want)
	}
	// a preview makes nothing, and tells the same; a folder made alone has no first folder
	preview := newFileOperations(true)
	preview.mkdir(filepath.Join(root, "New", "Deep"))
	preview.mkdir(filepath.Join(root, "New", "Deep", "Deeper"))
	if preview.operations[0].From != filepath.Join(root, "New") || preview.operations[1].From != "" {
		t.Fatalf("%+v", preview.operations)
	}
	if _, err := os.Stat(filepath.Join(root, "New")); err == nil {
		t.Fatal("a preview makes nothing")
	}
}

// The look at a whole run goes as the undo does, the last file moved first: a file whose place
// another file of the run leaves first goes back, and two files never go back to one path.
func TestCheckUndoAllGoesInOrder(t *testing.T) {
	root := t.TempDir()
	// B left b.nsp, then A took it
	b := moved(t, filepath.Join(root, "b.nsp"), filepath.Join(root, "B", "b.nsp"), "b")
	a := moved(t, filepath.Join(root, "a.nsp"), filepath.Join(root, "b.nsp"), "a")
	// two files with the same path of before (one came there after the run): only the last
	// moved goes back
	c := moved(t, filepath.Join(root, "c.nsp"), filepath.Join(root, "C", "c1.nsp"), "c1")
	d := moved(t, filepath.Join(root, "c.nsp"), filepath.Join(root, "C", "c2.nsp"), "c2")
	states := CheckUndoAll([]UndoMove{b, a, c, d})
	if want := []string{UNDO_BACK, UNDO_BACK, UNDO_TAKEN, UNDO_BACK}; strings.Join(states, ",") != strings.Join(want, ",") {
		t.Fatalf("%v, want %v", states, want)
	}
	// taken alone: b.nsp holds A
	if CheckUndo(b) != UNDO_TAKEN {
		t.Fatal("alone, the place of B is taken")
	}
}
