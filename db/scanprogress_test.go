package db

import (
	"os"
	"path/filepath"
	"testing"
)

type stepRecorder struct{ steps [][2]int }

func (r *stepRecorder) UpdateProgress(current int, total int, _ string) {
	r.steps = append(r.steps, [2]int{current, total})
}

// Walking the folders, the progress goes up to the number of files of the last scan, and is
// never full before the walk ends, even with more files than last time.
func TestWalkingTheFoldersShowsProgress(t *testing.T) {
	folder := t.TempDir()
	for _, name := range []string{"a.nsp", "b.nsp", "c.nsp", "d.nsp"} {
		os.WriteFile(filepath.Join(folder, name), []byte("x"), 0o644)
	}
	recorder := &stepRecorder{}
	files := []ExtendedFileInfo{}
	if err := scanFolder(folder, true, &files, recorder, 3); err != nil {
		t.Fatal(err)
	}
	if len(recorder.steps) != 4 || recorder.steps[0] != [2]int{1, 3} {
		t.Fatalf("steps: %v", recorder.steps)
	}
	for _, step := range recorder.steps {
		if step[0] >= step[1] {
			t.Fatalf("full before the end: %v", recorder.steps)
		}
	}
	unknown := &stepRecorder{}
	scanFolder(folder, true, &[]ExtendedFileInfo{}, unknown, 0)
	if unknown.steps[0] != [2]int{-1, -1} {
		t.Fatalf("without a last scan the progress is unknown: %v", unknown.steps)
	}
}
