package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	oldUpdate := file("Game [0100000000010800][v65536].nsp", 50)
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
	if duplicates := spaceGroup(groups, SPACE_DUPLICATES); len(duplicates.Files) != 1 || duplicates.Size != 70 {
		t.Fatalf("duplicates: %+v", duplicates)
	}
	compressed := spaceGroup(groups, SPACE_COMPRESSED)
	if len(compressed.Files) != 1 || compressed.Files[0].Deletable || compressed.DeletableSize != 0 {
		t.Fatalf("an original whose copy was not verified cannot be deleted: %+v", compressed)
	}

	// once the copy is verified, the original can be deleted
	copyPath := filepath.Join(dir, "Game [0100000000010000][v0].nsz")
	info, _ := os.Stat(copyPath)
	web.verifications().set(copyPath, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checked: time.Now()})
	if compressed := spaceGroup(web.spaceGroups(), SPACE_COMPRESSED); !compressed.Files[0].Deletable || compressed.DeletableSize != 300 {
		t.Fatalf("verified copy: %+v", compressed)
	}
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
