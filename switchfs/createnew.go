package switchfs

import "os"

// CreateNew creates a file for writing, replacing a file left at that path. A link left there
// is removed instead of followed, so writing never reaches the file it points to.
func CreateNew(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && !info.IsDir() {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
}
