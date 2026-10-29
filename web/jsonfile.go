package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// unreadStores are the files of the data folder that were there but could not be read for their
// store (access denied, in use by another program, a disk error): the store starts empty, and its
// next save would write over all the file holds. Such a file is not written until it is read.
var unreadStores sync.Map

// readStore reads the file of a store of the data folder; false when there is nothing to read.
// A file that does not exist is an empty store; one that could not be read is reported and kept
// as it is (see unreadStores).
func readStore(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	// on Windows a file being replaced by a save at the same moment refuses to open for an
	// instant: tried again before giving up
	for attempt := 0; err != nil && !errors.Is(err, fs.ErrNotExist) && attempt < 20; attempt++ {
		time.Sleep(10 * time.Millisecond)
		data, err = os.ReadFile(path)
	}
	if err == nil {
		unreadStores.Delete(path)
		return data, true
	}
	if !errors.Is(err, fs.ErrNotExist) {
		unreadStores.Store(path, true)
		zap.S().Errorf("%s could not be read (%v): it is kept as it is, and not saved until it can be read", filepath.Base(path), err)
	}
	return nil, false
}

// errStoreUnread is a save refused for a file that could not be read when its store was loaded.
var errStoreUnread = errors.New("the file could not be read when it was loaded: it is not written over")

// writeFileAtomic writes a file in the data folder through a temporary file, so a crash or
// a full disk while writing leaves the previous version instead of a cut file. Each write has
// a temporary file of its own: two saves at the same time never mix, and the file is on the
// disk before it takes the place of the previous one.
func writeFileAtomic(path string, data []byte) error {
	if _, unread := unreadStores.Load(path); unread {
		return fmt.Errorf("%s: %w", filepath.Base(path), errStoreUnread)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	_, err = temporary.Write(data)
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(name, 0600)
	}
	if err != nil {
		os.Remove(name)
		return err
	}
	// on Windows a file being replaced by another save at the same moment refuses the
	// rename for an instant: tried again before giving up
	for attempt := 0; ; attempt++ {
		if err = os.Rename(name, path); err == nil || attempt == 20 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

// parseStore reads the JSON of a file of the data folder. A file that cannot be read is set
// aside under another name and reported, instead of being taken for empty and written over
// by the next save, with all it held.
func parseStore(path string, data []byte, value any) error {
	err := json.Unmarshal(data, value)
	if err != nil {
		aside := fmt.Sprintf("%s.corrupt-%s", path, time.Now().Format("20060102-150405"))
		if renameErr := os.Rename(path, aside); renameErr == nil {
			zap.S().Errorf("%s could not be read (%v): it was kept as %s", filepath.Base(path), err, filepath.Base(aside))
		} else {
			zap.S().Errorf("%s could not be read: %v", filepath.Base(path), err)
		}
	}
	return err
}
