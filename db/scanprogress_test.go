package db

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	if err := scanFolder(folder, true, &files, nil, nil, recorder, 3); err != nil {
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
	scanFolder(folder, true, &[]ExtendedFileInfo{}, nil, nil, unknown, 0)
	if unknown.steps[0] != [2]int{-1, -1} {
		t.Fatalf("without a last scan the progress is unknown: %v", unknown.steps)
	}
}

// Cached data of a game the titles database does not know is read again once, to learn
// whether the game says it is a demo and its publisher; known games and data with the
// attributes are kept.
func TestUnknownGamesAreReadAgainForTheirAttributes(t *testing.T) {
	ldb := &LocalSwitchDBManager{titles: &SwitchTitlesDB{TitlesMap: map[string]*SwitchTitle{"0100000000010": {}}}}
	meta := func(id string, read bool) map[string]*switchfs.ContentMetaAttributes {
		return map[string]*switchfs.ContentMetaAttributes{id: {TitleId: id, Ncap: &switchfs.Nacp{AttributesRead: read, NamesRead: read, PublishersRead: read, TitleName: map[string]switchfs.NacpTitle{"AmericanEnglish": {Title: "Game"}}}}}
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
	// data read before the publishers were read: an unknown game is read again for its publisher
	withoutPublisher := meta("0100000000020000", true)
	withoutPublisher["0100000000020000"].Ncap.PublishersRead = false
	if !ldb.needsAttributes(withoutPublisher) {
		t.Fatal("an unknown game read without its publisher is read again")
	}
	// data read before the names were read apart is read again too
	older := meta("0100000000020000", true)
	older["0100000000020000"].Ncap.NamesRead = false
	if !ldb.needsAttributes(older) {
		t.Fatal("an unknown game read with the names mixed up is read again")
	}
	// a known game left without any name is read again too; one with a name is kept
	known := meta("0100000000010000", true)
	known["0100000000010000"].Ncap.NamesRead = false
	known["0100000000010000"].Ncap.TitleName = map[string]switchfs.NacpTitle{"BrazilianPortuguese": {Title: ""}}
	if !ldb.needsAttributes(known) {
		t.Fatal("a known game without a name is read again")
	}
	known["0100000000010000"].Ncap.TitleName = map[string]switchfs.NacpTitle{"AmericanEnglish": {Title: "Known Game"}}
	if ldb.needsAttributes(known) {
		t.Fatal("a known game with a name is kept")
	}
}

// The walk notes the temporary files of the app, and only those.
func TestScanFindsLeftovers(t *testing.T) {
	folder := t.TempDir()
	for _, name := range []string{".Game [0100000000010000][v0].nsz.tmp", ".slm-measure-123.tmp", ".hidden.tmp", "Game.nsp", ".DS_Store"} {
		os.WriteFile(filepath.Join(folder, name), []byte("x"), 0644)
	}
	leftovers := []LeftoverFile{}
	if err := scanFolder(folder, true, &[]ExtendedFileInfo{}, &leftovers, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 2 {
		t.Fatalf("leftovers: %+v", leftovers)
	}
}

// The walk of a scan finds the folders with nothing in them but files Windows or macOS leave;
// a folder with a game, with another folder, with a file that cannot be read, or with a hidden
// file of its own holds something.
func TestScanFindsEmptyFolders(t *testing.T) {
	folder := t.TempDir()
	for _, sub := range []string{"Empty", "Thumbs", "Parent/Child", "Game", "Kept"} {
		os.MkdirAll(filepath.Join(folder, sub), 0o755)
	}
	os.WriteFile(filepath.Join(folder, "Thumbs", "Thumbs.db"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(folder, "Thumbs", ".DS_Store"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(folder, "Game", "Game [0100000000010000][v0].nsp"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(folder, "Kept", ".keep"), []byte("x"), 0o644)

	empty := []string{}
	if err := scanFolder(folder, true, &[]ExtendedFileInfo{}, nil, &empty, nil, 0); err != nil {
		t.Fatal(err)
	}
	sort.Strings(empty)
	want := []string{filepath.Join(folder, "Empty"), filepath.Join(folder, "Parent", "Child"), filepath.Join(folder, "Thumbs")}
	if strings.Join(empty, "|") != strings.Join(want, "|") {
		t.Fatalf("empty folders: %v, want %v", empty, want)
	}
}
