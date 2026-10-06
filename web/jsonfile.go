package web

import (
	"os"
)

// writeFileAtomic writes a file in the data folder through a temporary file, so a crash or
// a full disk while writing leaves the previous version instead of a cut file.
func writeFileAtomic(path string, data []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, path)
}
