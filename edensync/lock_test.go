package edensync

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// writeTestLock writes a lock file as a program would have left it.
func writeTestLock(t *testing.T, folder Folder, lock Lock) {
	t.Helper()
	data, _ := json.Marshal(lock)
	if err := os.WriteFile(filepath.Join(folder.Root, LockName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestLock(t *testing.T, folder Folder) Lock {
	t.Helper()
	lock, ok := ReadLock(folder)
	if !ok {
		t.Fatal("no lock")
	}
	return lock
}

// twoSyncers are the library and NXPress on one computer, with the same Eden and the same saves.
func twoSyncers(t *testing.T) (library, nxpress *Syncer, dir string) {
	t.Helper()
	library, fake, dir := syncSetup(t)
	library.Owner = OwnerLibrary
	nxpress = &Syncer{Folder: library.Folder, Library: fake, StatePath: filepath.Join(t.TempDir(), "state.json"), TempDir: t.TempDir(),
		IsRunning: func() (bool, error) { return false, nil }, Owner: OwnerNXPress}
	return library, nxpress, dir
}

// The first program that passes keeps the saves in step; the other one does nothing while it does,
// and tells who does. Telling it never writes the lock.
func TestLockOneProgramAtATime(t *testing.T) {
	library, nxpress, dir := twoSyncers(t)
	if HeldBy(library.Folder, OwnerNXPress) != "" {
		t.Fatal("held by nobody yet")
	}
	if _, err := os.Stat(filepath.Join(library.Folder.Root, LockName)); !os.IsNotExist(err) {
		t.Fatalf("telling who holds it wrote the lock: %v", err)
	}
	writeSave(t, dir, map[string]string{"save.dat": "eden 1"})
	if report := pass(t, library); report.HeldBy != "" || len(report.Uploaded) != 1 {
		t.Fatalf("the library: %+v", report)
	}
	lock := readTestLock(t, library.Folder)
	if lock.Owner != OwnerLibrary || lock.Computer != thisComputer() || lock.Pid != os.Getpid() || lock.Updated == 0 {
		t.Fatalf("the lock: %+v", lock)
	}
	writeSave(t, dir, map[string]string{"save.dat": "eden 2"})
	report := pass(t, nxpress)
	if report.HeldBy != OwnerLibrary || len(report.Uploaded)+len(report.Brought)+len(report.Conflicts) != 0 || report.Running {
		t.Fatalf("NXPress waits: %+v", report)
	}
	if nxpress.State.LastPass != 0 {
		t.Fatal("NXPress looked at the saves")
	}
	if got := HeldBy(nxpress.Folder, OwnerNXPress); got != OwnerLibrary {
		t.Fatalf("held by: %q", got)
	}
	if got := HeldBy(library.Folder, OwnerLibrary); got != "" {
		t.Fatalf("its own lock is not held by another: %q", got)
	}
	// the lock is kept while the emulator is open too
	library.IsRunning = func() (bool, error) { return true, nil }
	if report := pass(t, library); !report.Running || readTestLock(t, library.Folder).Owner != OwnerLibrary {
		t.Fatalf("Eden open: %+v", report)
	}
	// turned off in the library: NXPress takes it on its next pass
	if err := library.Release(); err != nil {
		t.Fatal(err)
	}
	if report := pass(t, nxpress); report.HeldBy != "" || nxpress.State.LastPass == 0 || readTestLock(t, nxpress.Folder).Owner != OwnerNXPress {
		t.Fatalf("NXPress: %+v", report)
	}
	if report := pass(t, library); report.HeldBy != OwnerNXPress {
		t.Fatalf("now the library waits: %+v", report)
	}
}

// A pass renews its own lock, also one left by a run of before of the same program.
func TestLockRenewed(t *testing.T) {
	library, _, _ := twoSyncers(t)
	now := time.Unix(1_800_000_000, 0)
	library.Now = func() time.Time { return now }
	pass(t, library)
	if lock := readTestLock(t, library.Folder); lock.Updated != now.Unix() {
		t.Fatalf("written: %+v", lock)
	}
	now = now.Add(2 * time.Minute)
	pass(t, library)
	if lock := readTestLock(t, library.Folder); lock.Updated != now.Unix() {
		t.Fatalf("renewed: %+v", lock)
	}
	// left by a run of before, whose process ran a moment ago
	writeTestLock(t, library.Folder, Lock{Owner: OwnerLibrary, Computer: thisComputer(), Pid: os.Getpid() + 1, Updated: now.Unix()})
	if report := pass(t, library); report.HeldBy != "" || readTestLock(t, library.Folder).Pid != os.Getpid() {
		t.Fatalf("taken again: %+v", report)
	}
}

// endedProcess is the number of a process that ran and ended.
func endedProcess(t *testing.T) int {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^$")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	return command.ProcessState.Pid()
}

// A lock nobody renews any more is taken over: not renewed for LockStale, dated that far ahead,
// written by a process that ended on this computer, or that cannot be read.
func TestLockStaleTakenOver(t *testing.T) {
	library, _, _ := twoSyncers(t)
	now := time.Now()
	library.Now = func() time.Time { return now }
	ended := endedProcess(t)
	if processAlive(ended) || !processAlive(os.Getpid()) {
		t.Fatalf("processes: ended %v, this one %v", processAlive(ended), processAlive(os.Getpid()))
	}
	nxpress := func(computer string, pid int, updated time.Time) Lock {
		return Lock{Owner: OwnerNXPress, Computer: computer, Pid: pid, Updated: updated.Unix()}
	}
	for name, lock := range map[string]Lock{
		"not renewed":            nxpress(thisComputer(), os.Getpid(), now.Add(-LockStale)),
		"ahead":                  nxpress(thisComputer(), os.Getpid(), now.Add(LockStale+time.Minute)),
		"ended":                  nxpress(thisComputer(), ended, now),
		"of another computer":    nxpress("ANOTHER-COMPUTER", os.Getpid(), now.Add(-4*time.Minute)),
		"without an owner":       {Computer: thisComputer(), Pid: os.Getpid(), Updated: now.Unix()},
		"without a process told": nxpress(thisComputer(), 0, now.Add(-LockStale-time.Second)),
	} {
		writeTestLock(t, library.Folder, lock)
		if report := pass(t, library); report.HeldBy != "" {
			t.Fatalf("%s: %+v", name, report)
		}
		if got := readTestLock(t, library.Folder); got.Owner != OwnerLibrary || got.Pid != os.Getpid() || got.Updated != now.Unix() {
			t.Fatalf("%s: %+v", name, got)
		}
	}
	for name, data := range map[string]string{"corrupt": "{\"owner\": \"NXPr", "empty": "", "not JSON": "NXPress"} {
		if err := os.WriteFile(filepath.Join(library.Folder.Root, LockName), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if HeldBy(library.Folder, OwnerLibrary) != "" {
			t.Fatalf("%s: held", name)
		}
		if report := pass(t, library); report.HeldBy != "" || readTestLock(t, library.Folder).Owner != OwnerLibrary {
			t.Fatalf("%s: %+v", name, report)
		}
	}
	// fresh, and of a process that runs, or of another computer whose processes are not known here
	for name, lock := range map[string]Lock{
		"runs":                nxpress(thisComputer(), os.Getpid(), now.Add(-2*time.Minute)),
		"of another computer": nxpress("ANOTHER-COMPUTER", ended, now),
	} {
		writeTestLock(t, library.Folder, lock)
		if report := pass(t, library); report.HeldBy != OwnerNXPress {
			t.Fatalf("%s: %+v", name, report)
		}
	}
}

// Release removes only the lock of the program on this computer.
func TestLockRelease(t *testing.T) {
	library, nxpress, _ := twoSyncers(t)
	path := filepath.Join(library.Folder.Root, LockName)
	if err := library.Release(); err != nil {
		t.Fatalf("no lock: %v", err)
	}
	pass(t, library)
	if err := nxpress.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("NXPress removed the lock of the library: %v", err)
	}
	writeTestLock(t, library.Folder, Lock{Owner: OwnerLibrary, Computer: "ANOTHER-COMPUTER", Pid: os.Getpid(), Updated: time.Now().Unix()})
	if err := library.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the lock of another computer was removed: %v", err)
	}
	if err := os.WriteFile(path, []byte("not JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := library.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a lock that cannot be read was removed: %v", err)
	}
	pass(t, library)
	if err := Release(library.Folder, OwnerLibrary); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("its own lock stays: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(library.Folder.Root, "."+LockName+".*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

// takingLibrary is a library during whose first upload NXPress takes the lock, a minute later.
type takingLibrary struct {
	*fakeLibrary
	take func()
}

func (l *takingLibrary) Upload(ctx context.Context, upload Upload) (LibrarySave, error) {
	if l.take != nil {
		l.take()
		l.take = nil
	}
	return l.fakeLibrary.Upload(ctx, upload)
}

// A long pass renews the lock before each game, and stops when another program took it meanwhile.
func TestLockTakenDuringAPass(t *testing.T) {
	library, _, dir := twoSyncers(t)
	now := time.Now()
	library.Now = func() time.Time { return now }
	writeSave(t, dir, map[string]string{"save.dat": "eden 1"})
	writeSave(t, library.Folder.SaveFolder(testId, "0100A1B2C3D50000"), map[string]string{"save.dat": "other 1"})
	taking := &takingLibrary{fakeLibrary: library.Library.(*fakeLibrary)}
	taking.take = func() {
		now = now.Add(lockRenew)
		writeTestLock(t, library.Folder, Lock{Owner: OwnerNXPress, Computer: thisComputer(), Pid: os.Getpid(), Updated: now.Unix()})
	}
	library.Library = taking
	report, err := library.Pass(context.Background())
	if err != nil || report.HeldBy != OwnerNXPress || len(report.Uploaded) != 1 || report.Uploaded[0].TitleId != "0100A1B2C3D40000" {
		t.Fatalf("the pass: %+v %v", report, err)
	}
	if lock := readTestLock(t, library.Folder); lock.Owner != OwnerNXPress {
		t.Fatalf("the lock: %+v", lock)
	}
	if state, err := LoadState(library.StatePath); err != nil || state.Games[StateKey(Eden, testId, "0100A1B2C3D40000")] == nil {
		t.Fatalf("what was done is remembered: %+v %v", state, err)
	}
}
