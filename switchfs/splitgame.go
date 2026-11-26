package switchfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// A file of games may be kept split in parts for a FAT32 card, as the tools that install them
// write it: a folder named like the file ("Game [0100…][v0].nsp", or a NUP or a .nx) holding the
// parts "00", "01"… in their order, each but the last of the same size. The first part stands for
// the whole file in the library, and the folder itself may be given instead.

// isPartName tells the name of a part of a split file in its folder: "00", "01"…
func isPartName(name string) bool {
	if len(name) < 2 {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// WholeFile is the path of the file a part of a split file stands for, its folder
// (".../Game.nup/00" gives ".../Game.nup"); any other path is given as it is.
func WholeFile(path string) string {
	if isPartName(filepath.Base(path)) {
		if dir := filepath.Dir(path); dir != "." && dir != path {
			return dir
		}
	}
	return path
}

// hasExt tells whether the file at path, or the file a part of a split one stands for, has the
// extension ext (with its dot).
func hasExt(path string, ext string) bool {
	return strings.EqualFold(filepath.Ext(WholeFile(path)), ext)
}

// openWhole opens a file to read it, or the parts of a split one as one file, given its first
// part or its folder, and tells its size.
func openWhole(path string) (ReadAtCloser, int64, error) {
	part := path
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		part = filepath.Join(path, "00")
	}
	if isPartName(filepath.Base(part)) {
		split, err := NewSplitFileReader(part)
		if err != nil {
			return nil, 0, err
		}
		return split, split.Size(), nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

// WholeSize is the size of a file, or of all the parts of a split one, given its first part or
// its folder.
func WholeSize(path string) (int64, error) {
	file, size, err := openWhole(path)
	if err != nil {
		return 0, err
	}
	file.Close()
	return size, nil
}

// errSplitWrite refuses to write a file split in parts in place: its parts would no longer be of
// the sizes the readers ask.
var errSplitWrite = errors.New("the file is split in parts: join it first")

// isSplit tells a path of a file split in parts: its first part, or its folder.
func isSplit(path string) bool {
	if WholeFile(path) != path {
		return true
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
