package web

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// spaceWeb has a library with a game whose NSP has an NSZ copy, an old update and a
// duplicate, all real files.
func spaceWeb(t *testing.T) (*Web, string) {
	t.Helper()
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	web.localDbManager = manager
	// the rescan after a cleanup must finish before the folders are removed
	t.Cleanup(func() {
		for deadline := time.Now().Add(10 * time.Second); web.state.IsSynchronizing() && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		manager.Close()
	})
	dir := t.TempDir()
	file := func(name string, size int) db.ExtendedFileInfo {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0644); err != nil {
			t.Fatal(err)
		}
		return db.ExtendedFileInfo{FileName: name, BaseFolder: dir, Size: int64(size)}
	}
	game := file("Game [0100000000010000][v0].nsp", 300)
	file("Game [0100000000010000][v0].nsz", 100)
	// the compressed copy was just written: the next scan would find it
	web.rememberWritten(filepath.Join(dir, "Game [0100000000010000][v0].nsz"))
	oldUpdate := file("Game [0100000000010800][v65536].nsp", 50)
	// the newer update the old one is replaced by
	file("Game [0100000000010800][v131072].nsp", 60)
	duplicate := file("Other [0100000000020000][v0] (copy).nsp", 70)
	other := file("Other [0100000000020000][v0].nsp", 70)

	localDB := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: game, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"}},
				Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}},
			"0100000000020": {BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: other, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000020000"}},
				Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			oldUpdate: {ReasonCode: db.REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + filepath.Join(dir, "Game [0100000000010800][v131072].nsp") + ")"},
			duplicate: {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + filepath.Join(dir, other.FileName) + ")"},
		},
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, localDB)
	web.HandleSpace()
	return web, dir
}

func spaceGroup(groups []SpaceGroup, id string) SpaceGroup {
	for _, group := range groups {
		if group.Id == id {
			return group
		}
	}
	return SpaceGroup{}
}

func TestSpaceGroups(t *testing.T) {
	web, dir := spaceWeb(t)
	groups := web.spaceGroups()
	if old := spaceGroup(groups, SPACE_OLD_UPDATES); len(old.Files) != 1 || old.Size != 50 || old.Files[0].KeptBy == "" {
		t.Fatalf("old updates: %+v", old)
	}
	duplicates := spaceGroup(groups, SPACE_DUPLICATES)
	if len(duplicates.Files) != 1 || duplicates.Size != 70 || duplicates.Files[0].Deletable || duplicates.Files[0].Note != NOTE_SPACE_VERIFY_KEPT {
		t.Fatalf("a duplicate whose kept copy was not verified cannot be deleted: %+v", duplicates)
	}
	if paths := web.spaceVerifyPaths(); len(paths) != 2 {
		t.Fatalf("the kept duplicate and the compressed copy are checked: %v", paths)
	}
	compressed := spaceGroup(groups, SPACE_COMPRESSED)
	if len(compressed.Files) != 1 || compressed.Files[0].Deletable || compressed.DeletableSize != 0 {
		t.Fatalf("an original whose copy was not verified cannot be deleted: %+v", compressed)
	}

	// once the copy is verified, the original can be deleted, if the copy holds all of it
	copyPath := filepath.Join(dir, "Game [0100000000010000][v0].nsz")
	os.WriteFile(filepath.Join(dir, "Game [0100000000010000][v0].nsp"), testPfs0("game.nca", "update.nca"), 0644)
	verified := func(names ...string) {
		os.WriteFile(copyPath, testPfs0(names...), 0644)
		info, _ := os.Stat(copyPath)
		web.verifications().set(copyPath, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checked: time.Now(), Checker: verifyChecker})
	}
	verified("game.ncz")
	if compressed := spaceGroup(web.spaceGroups(), SPACE_COMPRESSED); compressed.Files[0].Deletable || compressed.Files[0].Note != NOTE_SPACE_NOT_SAME {
		t.Fatalf("the copy lacks the update of the original: %+v", compressed)
	}
	verified("game.ncz", "update.nca")
	if compressed := spaceGroup(web.spaceGroups(), SPACE_COMPRESSED); !compressed.Files[0].Deletable || compressed.DeletableSize != 300 {
		t.Fatalf("verified copy: %+v", compressed)
	}
	if data := web.spacePageData(); data.Reclaimable != 350 || data.LibrarySize == 0 {
		t.Fatalf("page data: reclaimable %d of %d", data.Reclaimable, data.LibrarySize)
	}

	// a damaged kept copy protects the duplicate; a sound one lets it be deleted
	kept := filepath.Join(dir, "Other [0100000000020000][v0].nsp")
	info, _ := os.Stat(kept)
	web.verifications().set(kept, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false, Checked: time.Now(), Checker: verifyChecker})
	if file := spaceGroup(web.spaceGroups(), SPACE_DUPLICATES).Files[0]; file.Deletable || file.Note != NOTE_SPACE_KEPT_DAMAGED {
		t.Fatalf("damaged kept copy: %+v", file)
	}
	web.verifications().set(kept, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checked: time.Now()})
	if data := web.spacePageData(); data.Reclaimable != 420 || data.LibrarySize == 0 {
		t.Fatalf("page data: reclaimable %d of %d", data.Reclaimable, data.LibrarySize)
	}
}

func TestSpaceCleanDeletesOnlyListedFiles(t *testing.T) {
	web, dir := spaceWeb(t)
	outside := filepath.Join(t.TempDir(), "precious.nsp")
	os.WriteFile(outside, []byte("keep"), 0644)
	original := filepath.Join(dir, "Game [0100000000010000][v0].nsp")
	oldUpdate := filepath.Join(dir, "Game [0100000000010800][v65536].nsp")

	response := postForm(web, "/space/clean", url.Values{"path": {outside, original, oldUpdate, filepath.Join(dir, "Other [0100000000020000][v0].nsp")}})
	if response.Code != http.StatusOK {
		t.Fatalf("got %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(oldUpdate); err == nil {
		t.Fatal("the old update is deleted")
	}
	for _, path := range []string{outside, original, filepath.Join(dir, "Other [0100000000020000][v0].nsp"), filepath.Join(dir, "Game [0100000000010000][v0].nsz")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s must stay: not listed, not verified or kept", filepath.Base(path))
		}
	}

	if response := postForm(web, "/space/clean", url.Values{"path": {outside}}); response.Code != http.StatusBadRequest {
		t.Fatalf("nothing deletable selected: %d", response.Code)
	}
}

func TestSpaceCleanRefusesChangedFiles(t *testing.T) {
	web, dir := spaceWeb(t)
	duplicate := filepath.Join(dir, "Other [0100000000020000][v0] (copy).nsp")
	// the file changed since the scan
	os.WriteFile(duplicate, make([]byte, 999), 0644)
	postForm(web, "/space/clean", url.Values{"path": {duplicate}})
	if _, err := os.Stat(duplicate); err != nil {
		t.Fatal("a file that changed since the scan is not deleted")
	}
}

func TestShortPaths(t *testing.T) {
	for text, want := range map[string]string{
		`duplicate base file (/games/Game [0100][v0].nsp)`:      `duplicate base file (Game [0100][v0].nsp)`,
		`actualización antigua (C:\Roms\Game (1) [v65536].nsp)`: `actualización antigua (Game (1) [v65536].nsp)`,
		`file type is not supported`:                            `file type is not supported`,
	} {
		if got := shortPaths(text); got != want {
			t.Errorf("shortPaths(%q) = %q", text, got)
		}
	}
	if fileBase(`C:\Roms\a.nsp`) != "a.nsp" || fileDir("/games/a.nsp") != "/games" || fileDir("a.nsp") != "" {
		t.Fatal("file name helpers")
	}
}

func TestKeptByWithParenthesesInTheName(t *testing.T) {
	if got := keptBy("duplicate base file (/roms/DOOM [0100B1A00D8CE000][v0](2).nsz)"); got != "/roms/DOOM [0100B1A00D8CE000][v0](2).nsz" {
		t.Fatalf("got %q", got)
	}
	for copy, name := range map[string]string{
		"Game [v0](2).nsz":      "Game [v0].nsz",
		"Game [v0] (3).nsp":     "Game [v0].nsp",
		"Game [v0] - Copy.nsz":  "Game [v0].nsz",
		"Game [v0] - copia.nsz": "Game [v0].nsz",
	} {
		if !isCopyOf(copy, name) {
			t.Errorf("%q is a copy of %q", copy, name)
		}
	}
	if isCopyOf("Game [v0].nsz", "Game [v0].nsz") || isCopyOf("Other(2).nsz", "Game.nsz") {
		t.Fatal("not copies")
	}
}

func TestSpaceDeletesTheCopyNotTheCleanName(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	clean := filepath.Join(dir, "Game [0100000000010000][v0].nsz")
	copy := filepath.Join(dir, "Game [0100000000010000][v0](2).nsz")
	os.WriteFile(clean, make([]byte, 40), 0644)
	os.WriteFile(copy, make([]byte, 40), 0644)
	localDB := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{},
		// the copy was found first and is in the library; the clean name is the duplicate
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			{FileName: filepath.Base(clean), BaseFolder: dir, Size: 40}: {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + copy + ")"},
		},
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, localDB)
	duplicates := spaceGroup(web.spaceGroups(), SPACE_DUPLICATES)
	if len(duplicates.Files) != 1 || duplicates.Files[0].Path != copy || duplicates.Files[0].KeptBy != clean {
		t.Fatalf("the copy must be the file to delete: %+v", duplicates.Files)
	}
}

// testPfs0 is a package with empty files of these names.
func testPfs0(names ...string) []byte {
	table := []byte{}
	offsets := []uint32{}
	for _, name := range names {
		offsets = append(offsets, uint32(len(table)))
		table = append(append(table, name...), 0)
	}
	header := make([]byte, 0x10+0x18*len(names))
	copy(header, "PFS0")
	binary.LittleEndian.PutUint32(header[4:], uint32(len(names)))
	binary.LittleEndian.PutUint32(header[8:], uint32(len(table)))
	for i := range names {
		binary.LittleEndian.PutUint32(header[0x10+0x18*i+16:], offsets[i])
	}
	return append(header, table...)
}

// An old update is only deleted while its newer version is still there and not damaged.
func TestOldUpdateNeedsItsNewerVersion(t *testing.T) {
	web, dir := spaceWeb(t)
	newer := filepath.Join(dir, "Game [0100000000010800][v131072].nsp")
	if file := spaceGroup(web.spaceGroups(), SPACE_OLD_UPDATES).Files[0]; !file.Deletable {
		t.Fatalf("the newer update is there: %+v", file)
	}
	// found damaged by the older check: not trusted until checked again
	info, _ := os.Stat(newer)
	web.verifications().set(newer, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false})
	if file := spaceGroup(web.spaceGroups(), SPACE_OLD_UPDATES).Files[0]; file.Deletable || file.Note != NOTE_SPACE_NEWER_DAMAGED {
		t.Fatalf("a damage of the older check: %+v", file)
	}
	os.Remove(newer)
	if file := spaceGroup(web.spaceGroups(), SPACE_OLD_UPDATES).Files[0]; file.Deletable || file.Note != NOTE_SPACE_KEPT_MISSING {
		t.Fatalf("the newer update was deleted by hand: %+v", file)
	}
}

// A copy checked only in part (an update whose romfs cannot be checked on its own) does not
// let the original be deleted.
func TestPartlyCheckedCopyKeepsTheOriginal(t *testing.T) {
	web, dir := spaceWeb(t)
	os.WriteFile(filepath.Join(dir, "Game [0100000000010000][v0].nsp"), testPfs0("game.nca"), 0644)
	copyPath := filepath.Join(dir, "Game [0100000000010000][v0].nsz")
	os.WriteFile(copyPath, testPfs0("game.ncz"), 0644)
	info, _ := os.Stat(copyPath)
	web.verifications().set(copyPath, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Partial: true, Checker: verifyChecker})
	if file := spaceGroup(web.spaceGroups(), SPACE_COMPRESSED).Files[0]; file.Deletable {
		t.Fatalf("a copy checked in part: %+v", file)
	}
}

// The Space page opens at once: while its content is prepared it says so, and once ready it
// shows it, until the verification results change.
func TestSpacePageOpensAtOnce(t *testing.T) {
	web, _ := spaceWeb(t)
	get := func(url string) string {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))
		return recorder.Body.String()
	}
	if page := get("/space.html"); !strings.Contains(page, "data-space-preparing") {
		t.Fatal("the first time, the page says its content is prepared")
	}
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(get("/space/ready"), "true"); {
		if time.Now().After(deadline) {
			t.Fatal("the content is never ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if page := get("/space.html"); strings.Contains(page, "data-space-preparing") || !strings.Contains(page, "spaceForm") {
		t.Fatal("once ready, the page shows its content")
	}
	web.verifications().set("other", verifyRecord{OK: true})
	if strings.Contains(get("/space/ready"), "true") {
		t.Fatal("a new verification result prepares the content again")
	}
	for deadline := time.Now().Add(10 * time.Second); web.spacePreparing.Load() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
}

// An original and its compressed copy found as duplicates of each other: the original can
// go as a compressed original, the compressed copy is never offered as its duplicate, so
// both are never deleted.
func TestSpaceNeverOffersBothCopies(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	nspPath := filepath.Join(dir, "Game [0100000000010000][v0].nsp")
	nszPath := filepath.Join(dir, "Game [0100000000010000][v0].nsz")
	os.WriteFile(nspPath, testPfs0("game.nca"), 0644)
	os.WriteFile(nszPath, testPfs0("game.ncz"), 0644)
	nspInfo, _ := os.Stat(nspPath)
	nszInfo, _ := os.Stat(nszPath)
	nsp := db.ExtendedFileInfo{FileName: filepath.Base(nspPath), BaseFolder: dir, Size: nspInfo.Size()}
	nsz := db.ExtendedFileInfo{FileName: filepath.Base(nszPath), BaseFolder: dir, Size: nszInfo.Size()}
	for path, info := range map[string]os.FileInfo{nspPath: nspInfo, nszPath: nszInfo} {
		web.verifications().set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checked: time.Now(), Checker: verifyChecker})
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: nsp, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"}},
				Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			nsz: {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + nspPath + ")"},
		},
	})
	groups := web.spaceGroups()
	for _, file := range spaceGroup(groups, SPACE_DUPLICATES).Files {
		if file.Path == nszPath {
			t.Fatalf("the compressed copy is offered as a duplicate of its original: %+v", file)
		}
	}
	compressed := spaceGroup(groups, SPACE_COMPRESSED)
	if len(compressed.Files) != 1 || compressed.Files[0].Path != nspPath || !compressed.Files[0].Deletable {
		t.Fatalf("the original goes, the compressed copy stays: %+v", compressed)
	}
}
