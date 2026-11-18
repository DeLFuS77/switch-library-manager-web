package edensync

import (
	"archive/zip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The files of a save: its hash, its ZIP, and a save folder replaced by the files of a ZIP.
//
// The ZIP of a save holds the files of its folder with relative paths and "/", nothing else: the
// shape the library takes from NXDock and from the page Saves. The files of the emulator in the
// folder (.yuzu_save_size) and the ones Windows and macOS leave are not part of the save: they are
// neither hashed nor zipped, and they stay in the folder when it is replaced.

// sizeFile is the file where Eden keeps the size of a save (GetSaveDataSizeFileName).
const sizeFile = ".yuzu_save_size"

// the files of a folder that are not of the game: of the emulator, and of the system
var notOfTheGame = map[string]bool{sizeFile: true, ".ds_store": true, "thumbs.db": true, "desktop.ini": true}

// the names a save folder has for a moment while it is replaced
const (
	newSuffix = ".sync-new-"
	oldSuffix = ".sync-old-"
)

var (
	// ErrEmpty tells a save without files of the game: a folder is never replaced with it.
	ErrEmpty = errors.New("the save has no files")
	// ErrBadZip tells a ZIP that is not the one of a save: a path that climbs out or is absolute,
	// or two files of the same name.
	ErrBadZip = errors.New("the ZIP is not one of a save")
)

// Ignored tells a file of a save folder that is not of the game, by its name: the size the
// emulator keeps, and what Windows and macOS leave in folders.
func Ignored(name string) bool {
	return notOfTheGame[strings.ToLower(path.Base(filepath.ToSlash(name)))]
}

// saveFile is a file of a save: its path in the save, with "/".
type saveFile struct {
	name string
	path string // on the disk, for a folder
	size int64
	mod  time.Time
	zip  *zip.File
}

// folderFiles are the files of the game in a save folder, by their names; none when the folder
// is missing.
func folderFiles(dir string) ([]saveFile, error) {
	files := []saveFile{}
	err := filepath.WalkDir(dir, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			if at == dir && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if entry.IsDir() || Ignored(entry.Name()) {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%s is not a file", at)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, at)
		if err != nil {
			return err
		}
		files = append(files, saveFile{name: filepath.ToSlash(rel), path: at, size: info.Size(), mod: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, nil
}

// zipName is the name of a file of a ZIP of a save, relative and with "/", if it can be one.
func zipName(name string) (string, bool) {
	name = strings.ReplaceAll(name, `\`, "/")
	for strings.HasPrefix(name, "./") {
		name = name[2:]
	}
	if name == "" || strings.HasPrefix(name, "/") {
		return "", false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, ":\x00") {
			return "", false
		}
	}
	return name, true
}

// zipFiles are the files of the game in a ZIP of a save, by their names.
func zipFiles(archive *zip.Reader) ([]saveFile, error) {
	files := []saveFile{}
	seen := map[string]bool{}
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name, ok := zipName(entry.Name)
		if !ok {
			return nil, ErrBadZip
		}
		if Ignored(name) {
			continue
		}
		if seen[strings.ToLower(name)] {
			return nil, ErrBadZip
		}
		seen[strings.ToLower(name)] = true
		files = append(files, saveFile{name: name, size: int64(entry.UncompressedSize64), mod: entry.Modified, zip: entry})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, nil
}

func (file saveFile) open() (io.ReadCloser, error) {
	if file.zip != nil {
		return file.zip.Open()
	}
	return os.Open(file.path)
}

// hashFiles is the hash of the files of a save: SHA-256 of, for each file in the order of their
// names, its name, a zero byte and the SHA-256 of its bytes. "" for a save without files.
func hashFiles(files []saveFile) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	all := sha256.New()
	for _, file := range files {
		reader, err := file.open()
		if err != nil {
			return "", err
		}
		one := sha256.New()
		_, err = io.Copy(one, reader)
		reader.Close()
		if err != nil {
			return "", err
		}
		all.Write([]byte(file.name))
		all.Write([]byte{0})
		all.Write(one.Sum(nil))
	}
	return hex.EncodeToString(all.Sum(nil)), nil
}

// HashFolder is the hash of the save in a folder: the same for the same files with the same
// bytes, wherever they are, and the same as HashZip of its ZIP. "" for a folder that is missing or
// holds no file of the game.
func HashFolder(dir string) (string, error) {
	files, err := folderFiles(dir)
	if err != nil {
		return "", err
	}
	return hashFiles(files)
}

// HashZip is the hash of the save in a ZIP, as HashFolder.
func HashZip(reader io.ReaderAt, size int64) (string, error) {
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return "", ErrBadZip
	}
	files, err := zipFiles(archive)
	if err != nil {
		return "", err
	}
	return hashFiles(files)
}

// HashZipFile is HashZip of a file.
func HashZipFile(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	return HashZip(file, info.Size())
}

// ZipFolder writes the files of the game of a save folder into a ZIP, by their relative paths.
// It is ErrEmpty when there are none.
func ZipFolder(dir string, out io.Writer) error {
	files, err := folderFiles(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return ErrEmpty
	}
	archive := zip.NewWriter(out)
	for _, file := range files {
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: file.name, Method: zip.Deflate, Modified: file.mod})
		if err != nil {
			return err
		}
		reader, err := file.open()
		if err != nil {
			return err
		}
		_, err = io.Copy(entry, reader)
		reader.Close()
		if err != nil {
			return err
		}
	}
	return archive.Close()
}

func randomSuffix() string {
	random := make([]byte, 6)
	rand.Read(random)
	return hex.EncodeToString(random)
}

// ReplaceFromZip puts the save of a ZIP in a save folder: its files are written in a folder next
// to it, which takes its place only when all of them are there; the folder of before is kept
// until then, and put back if the swap fails. The files of the emulator of the folder of before
// (.yuzu_save_size) stay. A ZIP without files of the game replaces nothing (ErrEmpty).
func ReplaceFromZip(dir string, reader io.ReaderAt, size int64) error {
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return ErrBadZip
	}
	files, err := zipFiles(archive)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return ErrEmpty
	}
	dir = filepath.Clean(dir)
	if err := Recover(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	fresh := dir + newSuffix + randomSuffix()
	if err := os.Mkdir(fresh, 0o755); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			os.RemoveAll(fresh)
		}
	}()
	for _, file := range files {
		if err := writeSaveFile(fresh, file); err != nil {
			return err
		}
	}
	// the size of the save the emulator keeps stays as it was
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if entry.Type().IsRegular() && entry.Name() == sizeFile {
				if err := copyFile(filepath.Join(dir, entry.Name()), filepath.Join(fresh, entry.Name())); err != nil {
					return err
				}
			}
		}
	}
	if !isDir(dir) {
		if err := os.Rename(fresh, dir); err != nil {
			return err
		}
		done = true
		return nil
	}
	old := dir + oldSuffix + randomSuffix()
	if err := os.Rename(dir, old); err != nil {
		return err
	}
	if err := os.Rename(fresh, dir); err != nil {
		if back := os.Rename(old, dir); back != nil {
			return fmt.Errorf("%w; the save of before is in %s", err, old)
		}
		return err
	}
	done = true
	return os.RemoveAll(old)
}

// writeSaveFile writes a file of a ZIP in a folder, by its name, which never leaves the folder.
func writeSaveFile(folder string, file saveFile) error {
	target := filepath.Join(folder, filepath.FromSlash(file.name))
	if rel, err := filepath.Rel(folder, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrBadZip
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	reader, err := file.open()
	if err != nil {
		return err
	}
	defer reader.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, reader)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil && !file.mod.IsZero() {
		os.Chtimes(target, file.mod, file.mod)
	}
	return err
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o644)
}

// Recover puts back a save folder a replace cut halfway left aside, and removes the folders it
// was writing. Nothing is done when the folder is there and nothing was left.
func Recover(dir string) error {
	dir = filepath.Clean(dir)
	parent, base := filepath.Dir(dir), filepath.Base(dir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	olds := []string{}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, base+newSuffix) && entry.IsDir():
			os.RemoveAll(filepath.Join(parent, name))
		case strings.HasPrefix(name, base+oldSuffix) && entry.IsDir():
			olds = append(olds, filepath.Join(parent, name))
		}
	}
	if len(olds) == 0 {
		return nil
	}
	if !isDir(dir) {
		// the newest one left aside is the save of before
		sort.Slice(olds, func(i, j int) bool { return modTime(olds[i]).After(modTime(olds[j])) })
		if err := os.Rename(olds[0], dir); err != nil {
			return err
		}
		olds = olds[1:]
	}
	for _, old := range olds {
		os.RemoveAll(old)
	}
	return nil
}

func modTime(name string) time.Time {
	info, err := os.Stat(name)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
