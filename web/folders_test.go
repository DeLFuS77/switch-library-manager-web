package web

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/process"
)

// Settings refuse a library folder listed twice or inside another one: its files would be read
// twice and count as duplicates of themselves.
func TestRepeatedFolders(t *testing.T) {
	roms := filepath.Join("lib", "roms")
	inside := filepath.Join(roms, "updates")
	other := filepath.Join("lib", "romsx")
	if message := repeatedFolders([]string{roms, other}, "en"); message != "" {
		t.Fatalf("two different folders: %q", message)
	}
	if message := repeatedFolders([]string{roms, other, roms + string(filepath.Separator)}, "en"); !strings.Contains(message, "listed twice") {
		t.Fatalf("a folder twice: %q", message)
	}
	if message := repeatedFolders([]string{inside, roms}, "en"); !strings.Contains(message, "is inside") || !strings.HasPrefix(message, inside) {
		t.Fatalf("a folder inside another: %q", message)
	}
	if message := repeatedFolders([]string{roms, roms}, "es"); !strings.Contains(message, "dos veces") {
		t.Fatalf("in the language of the page: %q", message)
	}
}

// otherPath is folder written in another way that reaches the same folder: with \\?\ in front
// on Windows, a link to it elsewhere; the test is skipped when the system makes no link.
func otherPath(t *testing.T, folder string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `\\?\` + folder
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(folder, link); err != nil {
		t.Skipf("no link to a folder here: %v", err)
	}
	return link
}

// One folder written in two ways (a drive and its share, two volumes of a container) is the
// same folder for the settings too, and so is a folder inside it.
func TestRepeatedFoldersOnTheDisk(t *testing.T) {
	lib := t.TempDir()
	if err := os.Mkdir(filepath.Join(lib, "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := otherPath(t, lib)
	if message := repeatedFolders([]string{lib, alias}, "en"); !strings.Contains(message, "listed twice") {
		t.Fatalf("one folder by two paths: %q", message)
	}
	if message := repeatedFolders([]string{filepath.Join(alias, "new"), lib}, "en"); !strings.Contains(message, "is inside") {
		t.Fatalf("a folder inside the other by another path: %q", message)
	}
}

// The Space page never offers to delete a duplicate that is the copy that stays, reached by
// another path (a library scanned by an older version listed it so).
func TestSpaceKeepsADuplicateThatIsTheKeptFile(t *testing.T) {
	web, dir := spaceWeb(t)
	other := filepath.Join(dir, "Other [0100000000020000][v0].nsp")
	seen := db.ExtendedFileInfo{FileName: filepath.Base(other), BaseFolder: otherPath(t, dir), Size: 70}
	_, localDB := web.state.get()
	localDB.Skipped = map[db.ExtendedFileInfo]db.SkippedFile{
		seen: {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + other + ")"},
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, localDB)
	info, _ := os.Stat(other)
	web.verifications().set(other, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checker: verifyChecker})
	files := spaceGroup(web.spaceGroups(), SPACE_DUPLICATES).Files
	if len(files) != 1 || files[0].Deletable || files[0].Note != process.REASON_SAME_FILE {
		t.Fatalf("the only copy is not offered: %+v", files)
	}
}
