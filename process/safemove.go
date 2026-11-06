package process

import (
	"errors"
	"fmt"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// rename gives a file a name no file has, as renameNoReplace; a variable for the tests, which
// make it fail as between two disks.
var rename = renameNoReplace

// moveFile moves a file to a name no file has: an error that is fs.ErrExist when a file has it,
// even one that came after it was looked at. Between two disks (two volumes of a container,
// two datasets of ZFS, two drives of Windows), where a rename cannot go, the file is copied
// next to its destination under a hidden name, the copy is checked against the original and
// takes the name, and only then is the original deleted.
func moveFile(from string, to string) error {
	return moveFileTelling(from, to, nil)
}

// CopyProgress is told how much of a file copied to another disk is written, about once a
// second: a game of many GB takes minutes, and its task would look stuck.
type CopyProgress func(written int64, total int64)

// moveFileTelling is moveFile telling progress how a copy to another disk goes.
func moveFileTelling(from string, to string, progress CopyProgress) error {
	err := rename(from, to)
	if err == nil || !crossDevice(err) {
		return err
	}
	return copyAcross(from, to, progress)
}

// CopyingMessage is the progress of a task while a file is copied to another disk: "Copying "
// and the file with how much of it is written, which the tasks page translates.
func CopyingMessage(name string, written int64, total int64) string {
	gb := func(n int64) string { return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) }
	return "Copying " + name + " (" + gb(written) + "/" + gb(total) + " GB)"
}

// progressWriter tells progress what was written through it, at most once a second.
type progressWriter struct {
	w        io.Writer
	written  int64
	total    int64
	told     time.Time
	progress CopyProgress
}

func (p *progressWriter) Write(data []byte) (int, error) {
	n, err := p.w.Write(data)
	p.written += int64(n)
	if now := time.Now(); now.Sub(p.told) >= time.Second {
		p.told = now
		p.progress(p.written, p.total)
	}
	return n, err
}

// copyAcross is moveFile between two disks.
func copyAcross(from string, to string, progress CopyProgress) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	before, err := source.Stat()
	if err != nil {
		return err
	}
	// the name of a temporary file of the app: one left by a stop in the middle is found by the
	// scan and can be cleaned up
	temporary := filepath.Join(filepath.Dir(to), "."+filepath.Base(to)+".tmp")
	err = func() error {
		target, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, before.Mode().Perm())
		if err != nil {
			return err
		}
		var destination io.Writer = target
		if progress != nil {
			destination = &progressWriter{w: target, total: before.Size(), told: time.Now(), progress: progress}
		}
		written, err := io.Copy(destination, source)
		if err == nil && progress != nil {
			// all of it, however fast it went
			progress(written, before.Size())
		}
		if err == nil {
			// on the disk before the original goes
			err = target.Sync()
		}
		if closeErr := target.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		after, err := os.Stat(from)
		if err != nil {
			return err
		}
		copied, err := os.Stat(temporary)
		if err != nil {
			return err
		}
		if written != before.Size() || copied.Size() != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
			return fmt.Errorf("the copy on the other disk does not match the original (%d of %d bytes), the original stays", copied.Size(), before.Size())
		}
		// the date of the original, as a rename keeps it: the checks of the file follow it by it
		os.Chtimes(temporary, after.ModTime(), after.ModTime())
		return renameNoReplace(temporary, to)
	}()
	if err != nil {
		os.Remove(temporary)
		return err
	}
	durable.SyncFolder(filepath.Dir(to))
	source.Close()
	if err := os.Remove(from); err != nil {
		// the original cannot go (a folder that cannot be written): the copy goes instead, so
		// the game is not twice in the library
		if os.Remove(to) == nil {
			return fmt.Errorf("the original cannot be deleted after its copy to the other disk, nothing was moved: %w", err)
		}
		return fmt.Errorf("the original cannot be deleted after its copy to the other disk: %w", err)
	}
	return nil
}

// renameChecked is a rename that refuses a name a file has, for the systems with no way to:
// a file that comes between the look and the rename is the only one written over.
func renameChecked(from string, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: os.ErrExist}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(from, to)
}
