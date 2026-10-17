package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// writeFileAtomic writes a file in the data folder through a temporary file, so a crash or
// a full disk while writing leaves the previous version instead of a cut file. Each write has
// a temporary file of its own: two saves at the same time never mix, and the file is on the
// disk before it takes the place of the previous one.
func writeFileAtomic(path string, data []byte) error {
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
