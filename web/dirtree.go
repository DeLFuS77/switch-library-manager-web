package web

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// a folder changed this recently, or with files written this recently, is read file by file
// at every check, so a file still being copied is noticed while it grows
const hotFolderPeriod = 10 * time.Minute

// dirTree remembers the folders of the library. Adding, removing or renaming a file changes
// the modification time of its folder, so a check only asks each folder for its time and
// reads again the folders that changed. With tens of thousands of files that is a fraction
// of the disk access of reading every file each time.
type dirTree struct {
	mutex sync.Mutex
	dirs  map[string]*dirInfo
	// how many folders were read file by file at the last check, for tests
	lastRead int
}

type dirInfo struct {
	modTime  int64
	subdirs  []string
	files    uint64
	hotUntil time.Time
	// the folder holds the file that leaves it out of the scan (settings.SKIP_MARKER)
	marked bool
}

func newDirTree() *dirTree {
	return &dirTree{dirs: map[string]*dirInfo{}}
}

// refresh checks the folders and returns a fingerprint of all their files, which changes
// when a file is added, removed, renamed or rewritten, and the folders found. The folders the
// scan leaves out (exclude, see settings.ExcludedFolder, or with settings.SKIP_MARKER in them)
// are left out too: what changes in them is no reason to scan. Putting or removing the file
// changes the folder, and its files then count or no longer do.
func (t *dirTree) refresh(roots []string, exclude []string) (uint64, []string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	now := time.Now()
	visited := map[string]bool{}
	var sum, xor, count uint64
	read := 0

	var visit func(dir string, chosen bool)
	visit = func(dir string, chosen bool) {
		if visited[dir] {
			return
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			// unreadable folders are skipped, like the scan does
			return
		}
		visited[dir] = true
		known := t.dirs[dir]
		modTime := info.ModTime().UnixNano()
		if known == nil || known.modTime != modTime || now.Before(known.hotUntil) {
			known = readDir(dir, modTime, now)
			t.dirs[dir] = known
			read++
		}
		// a folder left out by the file in it, below a folder chosen, is no reason to scan
		if known.marked && !chosen {
			return
		}
		// only files count: an empty folder is no reason to scan
		if known.files != 0 {
			entry := mix(hashString(dir) ^ known.files)
			sum += entry
			xor ^= entry
			count++
		}
		for _, sub := range known.subdirs {
			if !settings.ExcludedFolder(exclude, filepath.Base(sub)) {
				visit(sub, false)
			}
		}
	}
	for _, root := range roots {
		visit(filepath.Clean(root), true)
	}
	// folders that disappeared are forgotten
	for dir := range t.dirs {
		if !visited[dir] {
			delete(t.dirs, dir)
		}
	}
	t.lastRead = read
	dirs := make([]string, 0, len(visited))
	for dir := range visited {
		dirs = append(dirs, dir)
	}
	return sum ^ (xor * 0x9e3779b97f4a7c15) ^ count, dirs
}

// readDir lists a folder: its sub-folders and a fingerprint of its files.
func readDir(dir string, modTime int64, now time.Time) *dirInfo {
	result := &dirInfo{modTime: modTime}
	if now.Sub(time.Unix(0, modTime)) < hotFolderPeriod {
		result.hotUntil = now.Add(hotFolderPeriod)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}
	var sum, xor uint64
	files := 0
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			// the files waiting in the quarantine are not the library's
			if entry.Name() != db.QUARANTINE_FOLDER {
				result.subdirs = append(result.subdirs, path)
			}
			continue
		}
		if settings.IsSkipMarker(entry.Name()) {
			result.marked = true
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) < hotFolderPeriod {
			result.hotUntil = now.Add(hotFolderPeriod)
		}
		value := mix(hashString(entry.Name() + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)))
		sum += value
		xor ^= value
		files++
	}
	if files > 0 {
		// never 0, which stands for a folder without files
		result.files = (sum ^ (xor * 0x9e3779b97f4a7c15) ^ uint64(files)) | 1
	}
	return result
}

func hashString(text string) uint64 {
	hash := fnv.New64a()
	hash.Write([]byte(text))
	return hash.Sum64()
}

// mix spreads the bits, so values with similar hashes do not cancel out when combined
func mix(value uint64) uint64 {
	value ^= value >> 33
	value *= 0xff51afd7ed558ccd
	value ^= value >> 33
	return value
}
