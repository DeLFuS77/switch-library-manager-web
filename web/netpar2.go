package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/dtrunk90/switch-library-manager-web/par2"
)

// A game with recovery data next to it ("<name>.par2", written by NXPress) is checked slice by
// slice as it is sent to a console: a slice damaged on the disk is rebuilt with the recovery
// data before it is sent, so the console never gets damaged data. A file the recovery data
// cannot rebuild is not sent on: the transfer stops, and the log tells why.

// netRepairs keeps one repair at a time for each file
var netRepairs sync.Map // path -> *sync.Mutex

// errNetDamaged is given when a slice of a game is damaged and its recovery data cannot rebuild it
var errNetDamaged = errors.New("the file is damaged on the disk and its recovery data cannot rebuild it")

// checkedReader reads a file as http.ServeContent does (Seek, then Read), slice by slice of its
// recovery data, each checked before any of it is given.
type checkedReader struct {
	file    *os.File
	path    string
	checker *par2.Checker
	size    int64
	pos     int64
	slice   int
	buffer  []byte
	// repair rebuilds the file with its recovery data
	repair func() error
	// told tells a slice rebuilt, or one that could not be
	told func(string)
}

// newCheckedReader gives the reader of a file checked with its recovery data, or nil when the
// file has none, or one that does not fit it (another length).
func newCheckedReader(file *os.File, path string, size int64, repair func() error, told func(string)) io.ReadSeeker {
	recovery := par2.Path(path)
	if _, err := os.Stat(recovery); err != nil {
		return nil
	}
	checker, err := par2.NewChecker(recovery)
	if err != nil || checker.Length() != size {
		if told != nil {
			told(fmt.Sprintf("the recovery data of %s does not fit it: the file is sent as it is", path))
		}
		return nil
	}
	return &checkedReader{file: file, path: path, checker: checker, size: size, slice: -1, buffer: make([]byte, checker.SliceSize()), repair: repair, told: told}
}

func (r *checkedReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.pos
	case io.SeekEnd:
		offset += r.size
	}
	if offset < 0 {
		return 0, errors.New("negative position")
	}
	r.pos = offset
	return offset, nil
}

func (r *checkedReader) Read(p []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	sliceSize := r.checker.SliceSize()
	index := int(r.pos / sliceSize)
	if index != r.slice {
		if err := r.load(index); err != nil {
			return 0, err
		}
	}
	start := r.pos - int64(index)*sliceSize
	end := min(sliceSize, r.size-int64(index)*sliceSize)
	n := copy(p, r.buffer[start:end])
	r.pos += int64(n)
	return n, nil
}

// load reads slice index into the buffer once it is checked: a damaged one is rebuilt first.
func (r *checkedReader) load(index int) error {
	r.slice = -1
	whole, err := r.checker.ReadSlice(r.file, index, r.buffer)
	if err != nil {
		return err
	}
	if whole {
		r.slice = index
		return nil
	}
	if r.repair == nil {
		return errNetDamaged
	}
	if err := r.repair(); err != nil {
		if r.told != nil {
			r.told(fmt.Sprintf("%s is damaged and could not be repaired with its recovery data: %v", r.path, err))
		}
		return errNetDamaged
	}
	if whole, err = r.checker.ReadSlice(r.file, index, r.buffer); err != nil || !whole {
		return errNetDamaged
	}
	if r.told != nil {
		r.told(fmt.Sprintf("%s was damaged on the disk: repaired with its recovery data before it was sent", r.path))
	}
	r.slice = index
	return nil
}

// repairNetFile rebuilds a file with its recovery data, one repair at a time for it: a second
// transfer of the same file waits for the first repair and finds it whole.
func repairNetFile(ctx context.Context, path string) error {
	lock, _ := netRepairs.LoadOrStore(path, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	_, err := par2.Repair(ctx, par2.Path(path), path, nil)
	return err
}
