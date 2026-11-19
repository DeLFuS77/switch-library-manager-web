package edensync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLibrary is a library in memory, with a clock of its own.
type fakeLibrary struct {
	saves     []LibrarySave
	zips      map[string][]byte
	pairs     Pairs
	conflicts []Conflict
	clock     int64
	reports   int
	failSaves bool
}

func newFakeLibrary() *fakeLibrary {
	return &fakeLibrary{zips: map[string][]byte{}, clock: 1000}
}

// add keeps a save of a console, of an account.
func (l *fakeLibrary) addConsole(t *testing.T, title, account string, files map[string]string) LibrarySave {
	t.Helper()
	data := zipOf(t, files)
	raw, _ := io.ReadAll(data)
	hash, _ := HashZip(bytes.NewReader(raw), int64(len(raw)))
	l.clock += 10
	save := LibrarySave{TitleId: title, Id: fmt.Sprintf("%016x", l.clock), When: l.clock, Account: account, Console: "Switch", Size: int64(len(raw)), Hash: hash}
	l.saves = append(l.saves, save)
	l.zips[save.Id] = raw
	return save
}

func (l *fakeLibrary) Saves(ctx context.Context) ([]LibrarySave, error) {
	if l.failSaves {
		return nil, errors.New("down")
	}
	return append([]LibrarySave{}, l.saves...), nil
}

func (l *fakeLibrary) Download(ctx context.Context, titleId, id string, out io.Writer) error {
	data, ok := l.zips[id]
	if !ok {
		return errors.New("no such save")
	}
	_, err := out.Write(data)
	return err
}

func (l *fakeLibrary) Upload(ctx context.Context, upload Upload) (LibrarySave, error) {
	data, err := io.ReadAll(upload.Body)
	if err != nil || int64(len(data)) != upload.Size {
		return LibrarySave{}, fmt.Errorf("the body: %d of %d, %v", len(data), upload.Size, err)
	}
	hash, err := HashZip(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return LibrarySave{}, err
	}
	l.clock += 10
	save := LibrarySave{TitleId: upload.TitleId, Id: fmt.Sprintf("%016x", l.clock), When: l.clock, Account: upload.Profile.Name, Console: upload.Emulator.Name(),
		Size: int64(len(data)), Emulator: true, Profile: upload.Profile.ID.String(), Auto: upload.Auto, Hash: hash}
	l.saves = append(l.saves, save)
	l.zips[save.Id] = data
	// the library does not tell the hash it keeps in its answer
	save.Hash = ""
	return save, nil
}

func (l *fakeLibrary) Pairs(ctx context.Context) (Pairs, error) {
	return l.pairs, nil
}

func (l *fakeLibrary) ReportProfiles(ctx context.Context, emulator Emulator, profiles []ProfileReport) (Pairs, error) {
	l.reports++
	kept := []PairedProfile{}
	for _, report := range profiles {
		paired := PairedProfile{Profile: report.Profile, Name: report.Name, Emulator: string(emulator), Mine: true}
		if old, ok := pairedProfile(l.pairs, report.Profile); ok {
			paired.Account, paired.Chosen = old.Account, old.Chosen
		}
		if report.Account != nil {
			paired.Account, paired.Chosen = *report.Account, true
		}
		kept = append(kept, paired)
	}
	l.pairs.Profiles = kept
	return l.pairs, nil
}

func (l *fakeLibrary) ReportConflict(ctx context.Context, conflict Conflict) (string, error) {
	conflict.Id = fmt.Sprintf("c%d", len(l.conflicts)+1)
	l.conflicts = append(l.conflicts, conflict)
	return conflict.Id, nil
}

func (l *fakeLibrary) Conflicts(ctx context.Context) ([]Conflict, error) {
	return append([]Conflict{}, l.conflicts...), nil
}

func (l *fakeLibrary) ConflictDone(ctx context.Context, id string) error {
	for i, conflict := range l.conflicts {
		if conflict.Id == id {
			l.conflicts = append(l.conflicts[:i], l.conflicts[i+1:]...)
			return nil
		}
	}
	return errors.New("no such conflict")
}

// syncSetup is Eden with one profile, Player, paired with the account Marta of a console.
func syncSetup(t *testing.T) (*Syncer, *fakeLibrary, string) {
	t.Helper()
	folder := fakeEden(t, Profile{ID: testId, Name: "Player"})
	library := newFakeLibrary()
	library.pairs = Pairs{Accounts: []string{"Marta"}}
	statePath := filepath.Join(t.TempDir(), "state.json")
	syncer := &Syncer{Folder: folder, Library: library, StatePath: statePath, TempDir: t.TempDir(), IsRunning: func() (bool, error) { return false, nil }}
	return syncer, library, folder.SaveFolder(testId, "0100A1B2C3D40000")
}

func pass(t *testing.T, syncer *Syncer) Report {
	t.Helper()
	report, err := syncer.Pass(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) > 0 {
		t.Fatalf("errors: %v", report.Errors)
	}
	return report
}

// A save played in Eden goes to the library once, marked as uploaded by itself, with the profile;
// the only profile is paired by itself with the only account; the state is kept in its file.
func TestPassUploadsAndPairsByItself(t *testing.T) {
	syncer, library, dir := syncSetup(t)
	writeSave(t, dir, map[string]string{"save.dat": "eden 1", ".yuzu_save_size": "size"})
	report := pass(t, syncer)
	if len(report.Uploaded) != 1 || len(library.saves) != 1 {
		t.Fatalf("uploaded: %+v", report)
	}
	save := library.saves[0]
	if !save.Emulator || !save.Auto || save.Profile != testId.String() || save.Account != "Player" || save.Console != "Eden" || save.TitleId != "0100A1B2C3D40000" {
		t.Fatalf("the save: %+v", save)
	}
	if paired, _ := pairedProfile(library.pairs, testId.String()); paired.Account != "Marta" {
		t.Fatalf("paired by itself: %+v", library.pairs)
	}
	if again := pass(t, syncer); len(again.Uploaded)+len(again.Brought)+len(again.Conflicts) != 0 || len(library.saves) != 1 {
		t.Fatalf("nothing more: %+v", again)
	}
	state, err := LoadState(syncer.StatePath)
	if err != nil || state.Games[StateKey(Eden, testId, "0100A1B2C3D40000")] == nil || state.Uploaded != 1 || state.LastPass == 0 {
		t.Fatalf("the state: %+v %v", state, err)
	}
	// played again: a new copy
	writeSave(t, dir, map[string]string{"save.dat": "eden 2"})
	if again := pass(t, syncer); len(again.Uploaded) != 1 || len(library.saves) != 2 {
		t.Fatalf("played again: %+v", again)
	}
}

// A newer save of the console goes into Eden when Eden is closed, and the size Eden keeps stays;
// with Eden open nothing is touched.
func TestPassBringsTheConsoleSave(t *testing.T) {
	syncer, library, dir := syncSetup(t)
	writeSave(t, dir, map[string]string{"save.dat": "eden 1", ".yuzu_save_size": "size"})
	pass(t, syncer)
	library.addConsole(t, "0100A1B2C3D40000", "Marta", map[string]string{"save.dat": "console 2", "extra.bin": "x"})

	syncer.IsRunning = func() (bool, error) { return true, nil }
	if report := pass(t, syncer); !report.Running || readFolder(t, dir)["save.dat"] != "eden 1" {
		t.Fatalf("Eden open: %+v", report)
	}
	syncer.IsRunning = func() (bool, error) { return false, errors.New("cannot tell") }
	if _, err := syncer.Pass(context.Background()); err == nil || readFolder(t, dir)["save.dat"] != "eden 1" {
		t.Fatal("not knowing whether Eden is open touches nothing")
	}
	syncer.IsRunning = func() (bool, error) { return false, nil }
	report := pass(t, syncer)
	if len(report.Brought) != 1 || len(report.Uploaded) != 0 {
		t.Fatalf("brought: %+v", report)
	}
	if got := readFolder(t, dir); got["save.dat"] != "console 2" || got["extra.bin"] != "x" || got[".yuzu_save_size"] != "size" {
		t.Fatalf("in Eden: %v", got)
	}
	if again := pass(t, syncer); len(again.Uploaded)+len(again.Brought) != 0 {
		t.Fatalf("in step now: %+v", again)
	}
	// a save of another account is not this player's
	library.addConsole(t, "0100A1B2C3D40000", "Pau", map[string]string{"save.dat": "pau"})
	if again := pass(t, syncer); len(again.Brought) != 0 || readFolder(t, dir)["save.dat"] != "console 2" {
		t.Fatalf("another account: %+v", again)
	}
}

// Before a save of Eden the library does not hold is replaced, it is uploaded.
func TestPassKeepsACopyBeforeReplacing(t *testing.T) {
	syncer, library, dir := syncSetup(t)
	writeSave(t, dir, map[string]string{"save.dat": "eden 1"})
	pass(t, syncer)
	// the copy in the library is deleted by the user, then the console uploads a newer one
	library.saves, library.zips = nil, map[string][]byte{}
	library.addConsole(t, "0100A1B2C3D40000", "Marta", map[string]string{"save.dat": "console 2"})
	report := pass(t, syncer)
	if len(report.Brought) != 1 || len(report.Uploaded) != 0 || len(library.saves) != 2 || !library.saves[1].Emulator {
		t.Fatalf("a copy first: %+v %+v", report, library.saves)
	}
	if data := library.zips[library.saves[1].Id]; !bytes.Contains(data, []byte("eden 1")) && len(data) == 0 {
		t.Fatal("the copy holds the save of Eden")
	}
}

// When both changed, both stay in the library and the user is asked; nothing is replaced until the
// choice, which the next pass applies: the console's goes into Eden, or Eden keeps its own.
func TestPassConflict(t *testing.T) {
	for _, keep := range []string{KeepConsole, KeepEmulator} {
		t.Run(keep, func(t *testing.T) {
			syncer, library, dir := syncSetup(t)
			writeSave(t, dir, map[string]string{"save.dat": "eden 1"})
			pass(t, syncer)
			console := library.addConsole(t, "0100A1B2C3D40000", "Marta", map[string]string{"save.dat": "console 2"})
			writeSave(t, dir, map[string]string{"save.dat": "eden 2"})
			report := pass(t, syncer)
			if len(report.Conflicts) != 1 || len(library.conflicts) != 1 || len(library.saves) != 3 {
				t.Fatalf("a conflict: %+v %+v", report, library.saves)
			}
			conflict := library.conflicts[0]
			if conflict.ConsoleSave != console.Id || conflict.EmulatorSave != library.saves[2].Id || conflict.Profile != testId.String() || conflict.TitleId != "0100A1B2C3D40000" {
				t.Fatalf("the conflict: %+v", conflict)
			}
			// while the user has not chosen, nothing moves
			if again := pass(t, syncer); len(again.Waiting) != 1 || len(again.Uploaded)+len(again.Brought)+len(again.Conflicts) != 0 || readFolder(t, dir)["save.dat"] != "eden 2" {
				t.Fatalf("waiting: %+v", again)
			}
			library.conflicts[0].Keep = keep
			applied := pass(t, syncer)
			if len(library.conflicts) != 0 {
				t.Fatal("the conflict is done")
			}
			want := "eden 2"
			if keep == KeepConsole {
				want = "console 2"
				if len(applied.Brought) != 1 {
					t.Fatalf("brought: %+v", applied)
				}
			}
			if got := readFolder(t, dir)["save.dat"]; got != want {
				t.Fatalf("in Eden: %q, want %q", got, want)
			}
			if again := pass(t, syncer); len(again.Uploaded)+len(again.Brought)+len(again.Conflicts) != 0 || len(library.saves) != 3 {
				t.Fatalf("in step after the choice: %+v", again)
			}
		})
	}
}

// A save of the console for a game never opened in Eden with that profile waits: Eden makes the
// folder of the save the first time.
func TestPassWaitsForTheFolder(t *testing.T) {
	syncer, library, dir := syncSetup(t)
	library.pairs.Profiles = []PairedProfile{{Profile: testId.String(), Name: "Player", Emulator: "eden", Account: "Marta", Chosen: true, Mine: true}}
	library.addConsole(t, "0100A1B2C3D40000", "Marta", map[string]string{"save.dat": "console 1"})
	report := pass(t, syncer)
	if len(report.Waiting) != 1 || len(report.Brought) != 0 {
		t.Fatalf("waiting: %+v", report)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the folder is not made")
	}
	os.MkdirAll(dir, 0o755)
	if report := pass(t, syncer); len(report.Brought) != 1 || readFolder(t, dir)["save.dat"] != "console 1" {
		t.Fatalf("brought once the game was opened: %+v", report)
	}
}

// Eden on another computer: a profile paired with the same account takes the save the other one
// uploaded; a save of an emulator brought by hand, without a profile, is not taken.
func TestPassTakesTheSaveOfAnotherComputer(t *testing.T) {
	syncer, library, dir := syncSetup(t)
	writeSave(t, dir, map[string]string{"save.dat": "here 1"})
	pass(t, syncer)
	other := "aaaaaaaa-0000-0000-0000-000000000001"
	library.pairs.Profiles = append(library.pairs.Profiles, PairedProfile{Profile: other, Name: "Player", Emulator: "eden", Account: "Marta", Computer: "NXPress on OTHER"})
	library.clock += 10
	data := zipOf(t, map[string]string{"save.dat": "there 2"})
	raw, _ := io.ReadAll(data)
	hash, _ := HashZip(bytes.NewReader(raw), int64(len(raw)))
	library.saves = append(library.saves, LibrarySave{TitleId: "0100A1B2C3D40000", Id: "00000000000fffff", When: library.clock + 100, Emulator: true, Profile: other, Hash: hash})
	library.zips["00000000000fffff"] = raw
	library.saves = append(library.saves, LibrarySave{TitleId: "0100A1B2C3D40000", Id: "00000000000ffffe", When: library.clock + 200, Emulator: true, Console: "Ryujinx"})
	report := pass(t, syncer)
	if len(report.Brought) != 1 || report.Brought[0].SaveId != "00000000000fffff" || readFolder(t, dir)["save.dat"] != "there 2" {
		t.Fatalf("from the other computer: %+v", report)
	}
}

// The profiles are told to the library when they change, not at every pass.
func TestPassTellsTheProfilesOnce(t *testing.T) {
	syncer, library, _ := syncSetup(t)
	library.pairs.Accounts = nil
	pass(t, syncer)
	pass(t, syncer)
	if library.reports != 1 {
		t.Fatalf("told %d times", library.reports)
	}
	path := filepath.Join(syncer.Folder.Nand, filepath.FromSlash(profilesPath))
	os.WriteFile(path, ProfilesFile([]Profile{{ID: testId, Name: "Renamed"}}), 0o644)
	pass(t, syncer)
	if library.reports != 2 || library.pairs.Profiles[0].Name != "Renamed" {
		t.Fatalf("a new name: %d %+v", library.reports, library.pairs)
	}
	library.failSaves = true
	if _, err := syncer.Pass(context.Background()); err == nil || !strings.Contains(err.Error(), "down") {
		t.Fatalf("a library that does not answer: %v", err)
	}
}
