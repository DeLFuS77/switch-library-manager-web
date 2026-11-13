package web

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/diskfree"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The Library can show the games of one of the folders it scans, and sort them by folder; the
// Statistics show what each folder holds and what is left on its disk. A game is of the folder
// that holds the file of its base game: its updates and DLC may be elsewhere, but the game is
// where the game is. A folder inside another one scanned too has its own games.

// LibraryFolder is a folder the library scans, with the name it is shown by.
type LibraryFolder struct {
	// a short name of its path that does not tell the path: the filter of the Library is shared
	// in links, and the users who only look do not see the paths
	Id string
	// the last part of its path, with the one above when two folders have the same name
	Name string
	Path string
}

// folderIdPattern is what a folder of the filter can be.
var folderIdPattern = regexp.MustCompile(`^[0-9a-f]{10}$`)

// folderId names a folder without telling its path: the same path always gives the same name,
// so links and smart collections keep their folder when the others change.
func folderId(path string) string {
	key := filepath.Clean(path)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		key = strings.ToLower(key)
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:5])
}

// libraryFolders are the folders of the settings, the main one first, each once.
func libraryFolders(s *settings.AppSettings) []LibraryFolder {
	folders := []LibraryFolder{}
	seen := map[string]bool{}
	for _, path := range scanFolders(s) {
		id := folderId(path)
		if seen[id] {
			continue
		}
		seen[id] = true
		folders = append(folders, LibraryFolder{Id: id, Path: path})
	}
	nameFolders(folders)
	return folders
}

// pathParts are the names of the folders of a path, the last one first ("E:" for the root of a
// drive).
func pathParts(path string) []string {
	path = filepath.Clean(path)
	parts := []string{}
	for {
		base := filepath.Base(path)
		if base == "" || base == "." || base == string(filepath.Separator) || base == "/" {
			break
		}
		parts = append(parts, base)
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	if volume := filepath.VolumeName(path); volume != "" && (len(parts) == 0 || parts[len(parts)-1] != volume) {
		parts = append(parts, volume)
	}
	if len(parts) == 0 {
		parts = append(parts, filepath.Clean(path))
	}
	return parts
}

// nameFolders names each folder by the last part of its path; folders with the same name take
// the part above in brackets ("Games (NAS)"), and more if it is still the same.
func nameFolders(folders []LibraryFolder) {
	parts := make([][]string, len(folders))
	for i, folder := range folders {
		parts[i] = pathParts(folder.Path)
	}
	name := func(i int, depth int) string {
		p := parts[i]
		if depth == 0 || len(p) == 1 {
			return p[0]
		}
		above := []string{}
		for level := 1; level <= depth && level < len(p); level++ {
			above = append(above, p[level])
		}
		return p[0] + " (" + strings.Join(above, ", ") + ")"
	}
	depths := make([]int, len(folders))
	for round := 0; round < 8; round++ {
		counts := map[string]int{}
		for i := range folders {
			counts[strings.ToLower(name(i, depths[i]))]++
		}
		same := false
		for i := range folders {
			if counts[strings.ToLower(name(i, depths[i]))] > 1 && depths[i] < len(parts[i])-1 {
				depths[i]++
				same = true
			}
		}
		if !same {
			break
		}
	}
	for i := range folders {
		folders[i].Name = name(i, depths[i])
	}
}

// folderOf is the folder of the library that holds a folder of files: the deepest one, as a
// folder inside another one scanned has its own games. nil when it is in none.
func folderOf(folders []LibraryFolder, base string) *LibraryFolder {
	var found *LibraryFolder
	for i := range folders {
		folder := &folders[i]
		if !db.SameFolderName(base, folder.Path) && !db.FolderInsideName(base, folder.Path) {
			continue
		}
		if found == nil || len(filepath.Clean(folder.Path)) > len(filepath.Clean(found.Path)) {
			found = folder
		}
	}
	return found
}

// folderFinder tells the folder of the library of each folder of files, once for each.
func folderFinder(folders []LibraryFolder) func(base string) *LibraryFolder {
	known := map[string]*LibraryFolder{}
	return func(base string) *LibraryFolder {
		if folder, ok := known[base]; ok {
			return folder
		}
		folder := folderOf(folders, base)
		known[base] = folder
		return folder
	}
}

// FolderChoice is a folder of the filter of the Library: its name alone, never its path, and
// how many games it has.
type FolderChoice struct {
	Id    string
	Name  string
	Count int
}

// FolderSpace is a folder of the library on the Statistics: its games and the size of its
// files, and for the administrators its path and its disk.
type FolderSpace struct {
	Id    string
	Name  string
	Games int
	Size  int64
	// shown to the administrators only: "" and 0 for the others
	Path string
	// the disk of the folder: what is free on it and its size, 0 when it cannot be told
	Free  int64
	Total int64
	// the bar of the disk, in percent: the files of the folder, and the rest that is used
	FolderPercent int
	OtherPercent  int
}

// HasDisk reports whether the disk of the folder is known.
func (f FolderSpace) HasDisk() bool {
	return f.Total > 0
}

// withDisk measures the disk of the folder for its bar.
func (f FolderSpace) withDisk(path string) FolderSpace {
	f.Path = path
	available, total, err := diskfree.Space(path)
	if err != nil || total == 0 {
		return f
	}
	f.Free, f.Total = int64(available), int64(total)
	used := max(f.Total-f.Free, 0)
	mine := min(f.Size, used)
	f.FolderPercent = barPercent(mine, f.Total)
	f.OtherPercent = min(barPercent(used-mine, f.Total), 100-f.FolderPercent)
	return f
}

// barPercent is a part of a bar in percent, at least 1 when there is some.
func barPercent(part int64, total int64) int {
	if part <= 0 || total <= 0 {
		return 0
	}
	return min(max(int(part*100/total), 1), 100)
}

// folderSpaces are the folders of the Statistics as a user sees them: the administrators see
// their paths and disks, the others their names, games and sizes.
func (web *Web) folderSpaces(stats Statistics, admin bool) []FolderSpace {
	folders := libraryFolders(settings.ReadSettings(web.dataFolder))
	paths := map[string]string{}
	for _, folder := range folders {
		paths[folder.Id] = folder.Path
	}
	spaces := make([]FolderSpace, 0, len(stats.Folders))
	for _, space := range stats.Folders {
		if admin {
			space = space.withDisk(paths[space.Id])
		}
		spaces = append(spaces, space)
	}
	return spaces
}
