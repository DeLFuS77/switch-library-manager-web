package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/switchfs"
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

// Cached data of a game the titles database does not know is read again once, to learn
// whether the game says it is a demo; known games and data with the attributes are kept.
func TestUnknownGamesAreReadAgainForTheirAttributes(t *testing.T) {
	ldb := &LocalSwitchDBManager{titles: &SwitchTitlesDB{TitlesMap: map[string]*SwitchTitle{"0100000000010": {}}}}
	meta := func(id string, read bool) map[string]*switchfs.ContentMetaAttributes {
		return map[string]*switchfs.ContentMetaAttributes{id: {TitleId: id, Ncap: &switchfs.Nacp{AttributesRead: read, NamesRead: read}}}
	}
	if !ldb.needsAttributes(meta("0100000000020000", false)) {
		t.Fatal("an unknown game without attributes is read again")
	}
	if ldb.needsAttributes(meta("0100000000010000", false)) {
		t.Fatal("a known game is not read again")
	}
	if ldb.needsAttributes(meta("0100000000020000", true)) {
		t.Fatal("data with the attributes is kept")
	}
	// data read before the names were read apart is read again too
	older := meta("0100000000020000", true)
	older["0100000000020000"].Ncap.NamesRead = false
	if !ldb.needsAttributes(older) {
		t.Fatal("an unknown game read with the names mixed up is read again")
	}
}
