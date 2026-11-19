package edensync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The lock of the data folder of an emulator: Switch Library Manager Web (when it runs on the
// computer of the emulator) and NXPress both keep the saves of an emulator in step, and both on the
// same computer would read and write the same save folders at once. The program that keeps them in
// step writes a small file in the data folder and renews it on every pass; the other one does
// nothing while that file is there and fresh. Eden ignores the files it does not know there.
//
// The file holds who wrote it, on which computer, its process and when, as JSON:
//
//	{"owner": "NXPress", "computer": "DESKTOP-1", "pid": 4242, "updated": 1795000000}
//
// It is stale, and taken by the next program that passes, when it was not renewed for LockStale,
// when the process that wrote it no longer runs on this computer, or when it cannot be read.

// The programs that keep the saves of an emulator in step, as the lock names them.
const (
	OwnerLibrary = "Switch Library Manager Web"
	OwnerNXPress = "NXPress"
)

// LockName is the name of the lock file, in the data folder of the emulator (Folder.Root).
const LockName = "slmw-eden-sync.lock"

// LockStale is how long a lock lasts without being renewed; the passes renew it every minute.
const LockStale = 3 * time.Minute

// lockRenew is how often a long pass renews the lock as it goes.
const lockRenew = time.Minute

// Lock is what the lock file holds.
type Lock struct {
	Owner    string `json:"owner"`
	Computer string `json:"computer"`
	Pid      int    `json:"pid"`
	// when it was written or last renewed, in seconds since 1970
	Updated int64 `json:"updated"`
}

// thisComputer is the name of this computer, as the lock writes it.
func thisComputer() string {
	name, _ := os.Hostname()
	return name
}

func lockPath(folder Folder) string {
	return filepath.Join(folder.Root, LockName)
}

// ReadLock reads the lock of the data folder of an emulator; ok is false when there is none or it
// cannot be read.
func ReadLock(folder Folder) (Lock, bool) {
	if folder.Root == "" {
		return Lock{}, false
	}
	data, err := os.ReadFile(lockPath(folder))
	if err != nil {
		return Lock{}, false
	}
	lock := Lock{}
	if err := json.Unmarshal(data, &lock); err != nil {
		return Lock{}, false
	}
	return lock, true
}

// mine tells a lock of this program on this computer, whichever its process: one left by a run of
// before is taken again at once.
func (l Lock) mine(owner, computer string) bool {
	return l.Owner == owner && strings.EqualFold(l.Computer, computer)
}

// stale tells a lock nobody holds any more: without an owner, not renewed for LockStale (or dated
// that far ahead), or written by a process that no longer runs on this computer.
func (l Lock) stale(now time.Time, computer string) bool {
	if strings.TrimSpace(l.Owner) == "" {
		return true
	}
	age := now.Sub(time.Unix(l.Updated, 0))
	if age >= LockStale || age <= -LockStale {
		return true
	}
	return l.Pid > 0 && strings.EqualFold(l.Computer, computer) && !processAlive(l.Pid)
}

// HeldBy is the program other than owner that keeps the saves of the data folder of an emulator in
// step now, "" for none. It only reads: the pages tell it, and a pass of owner would wait.
func HeldBy(folder Folder, owner string) string {
	return heldBy(folder, owner, thisComputer(), time.Now())
}

func heldBy(folder Folder, owner, computer string, now time.Time) string {
	lock, ok := ReadLock(folder)
	if !ok || lock.mine(owner, computer) || lock.stale(now, computer) {
		return ""
	}
	return lock.Owner
}

// takeLock writes the lock of owner, unless another program holds it: then it gives its name and
// writes nothing.
func takeLock(folder Folder, owner string, now time.Time) (string, error) {
	computer := thisComputer()
	if held := heldBy(folder, owner, computer, now); held != "" {
		return held, nil
	}
	if err := writeLock(folder, Lock{Owner: owner, Computer: computer, Pid: os.Getpid(), Updated: now.Unix()}); err != nil {
		return "", err
	}
	// two programs that wrote it at the same moment: the one written last keeps it
	return heldBy(folder, owner, computer, now), nil
}

// writeLock writes the lock through a temporary file, so the other program never reads one cut
// halfway.
func writeLock(folder Folder, lock Lock) error {
	data, err := json.Marshal(lock)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(folder.Root, "."+LockName+".*.tmp")
	if err != nil {
		return err
	}
	_, err = temporary.Write(data)
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		// Windows refuses to replace a file the other program reads at that very moment
		for try := 0; try < 5; try++ {
			if err = os.Rename(temporary.Name(), lockPath(folder)); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if err != nil {
		os.Remove(temporary.Name())
	}
	return err
}

// Release removes the lock of the data folder of an emulator when it is the one of owner on this
// computer, so the other program may keep the saves in step at once: as the passes are turned
// off, and as the program stops.
func Release(folder Folder, owner string) error {
	lock, ok := ReadLock(folder)
	if !ok || !lock.mine(owner, thisComputer()) {
		return nil
	}
	if err := os.Remove(lockPath(folder)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
