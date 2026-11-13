package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Two folders with the same name are told apart by the folder above; the others keep their name.
func TestFoldersWithTheSameNameTakeTheOneAbove(t *testing.T) {
	folders := []LibraryFolder{
		{Path: filepath.FromSlash("/mnt/nas/Games")},
		{Path: filepath.FromSlash("/mnt/usb/games")},
		{Path: filepath.FromSlash("/data/Switch/")},
		{Path: filepath.FromSlash("/one/same/Games")},
		{Path: filepath.FromSlash("/two/same/Games")},
	}
	nameFolders(folders)
	want := []string{"Games (nas)", "games (usb)", "Switch", "Games (same, one)", "Games (same, two)"}
	for i, folder := range folders {
		if folder.Name != want[i] {
			t.Errorf("%s is named %q, want %q", folder.Path, folder.Name, want[i])
		}
	}
	// the id tells nothing of the path, and is the same for the same folder
	id := folderId(folders[0].Path)
	if !folderIdPattern.MatchString(id) || id != folderId(folders[0].Path+string(filepath.Separator)) || id == folderId(folders[1].Path) {
		t.Fatalf("the id of a folder: %q", id)
	}
}

// A game is of the deepest folder scanned that holds the file of its base game.
func TestGameIsOfTheFolderOfItsBaseFile(t *testing.T) {
	outer := filepath.FromSlash("/games")
	inner := filepath.FromSlash("/games/usb")
	folders := []LibraryFolder{{Id: "a", Path: outer}, {Id: "b", Path: inner}}
	for base, want := range map[string]string{
		outer:                          "a",
		filepath.Join(outer, "x"):      "a",
		inner:                          "b",
		filepath.Join(inner, "y", "z"): "b",
		filepath.FromSlash("/gamesx"):  "",
	} {
		got := ""
		if folder := folderOf(folders, base); folder != nil {
			got = folder.Id
		}
		if got != want {
			t.Errorf("%s is of %q, want %q", base, got, want)
		}
	}
}

// foldersTestWeb is a library in three folders: two games in the main one, one in a folder of
// the same name elsewhere, and its DLC in the third one.
func foldersTestWeb(t *testing.T) (*Web, []string) {
	t.Helper()
	web := newTestWeb(t)
	root := t.TempDir()
	paths := []string{filepath.Join(root, "nas", "Games"), filepath.Join(root, "usb", "Games"), filepath.Join(root, "Extras")}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	file := func(folder string, name string, size int64) db.ExtendedFileInfo {
		return db.ExtendedFileInfo{FileName: name, BaseFolder: folder, Size: size}
	}
	game := func(id string, info db.ExtendedFileInfo) *db.SwitchGameFiles {
		return &db.SwitchGameFiles{BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: info, Metadata: &switchfs.ContentMetaAttributes{TitleId: id + "000"}},
			Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	}
	localDB := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": game("0100000000010", file(paths[0], "Alpha [0100000000010000][v0].nsp", 100)),
		"0100000000020": game("0100000000020", file(filepath.Join(paths[0], "sub"), "Bravo [0100000000020000][v0].nsp", 200)),
		"0100000000030": game("0100000000030", file(paths[1], "Charlie [0100000000030000][v0].nsp", 300)),
	}}
	localDB.TitlesMap["0100000000030"].Dlc["0100000000031001"] = db.SwitchFileInfo{ExtendedInfo: file(paths[2], "Charlie DLC [0100000000031001][v0].nsp", 50),
		Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000031001"}}
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": {Attributes: db.TitleAttributes{Id: "0100000000010000", Name: "Alpha"}},
		"0100000000020": {Attributes: db.TitleAttributes{Id: "0100000000020000", Name: "Bravo"}},
		"0100000000030": {Attributes: db.TitleAttributes{Id: "0100000000030000", Name: "Charlie"}},
	}}
	web.state.set(switchDB, localDB)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Folder = paths[0]
		s.ScanFolders = paths[1:]
	})
	return web, paths
}

// The Library shows the games of the folder chosen, counts the choice among its filters and
// keeps it in its links; sorted by folder, the games go by the name of their folder, then theirs.
func TestLibraryFilterAndSortByFolder(t *testing.T) {
	web, paths := foldersTestWeb(t)
	_, p, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	if p.NumItems != 3 || !facets.ShowFolders {
		t.Fatalf("all the games, and the folders offered: %d %+v", p.NumItems, facets.Folders)
	}
	// the folder of the DLC alone has no game of its own: it is not offered
	if len(facets.Folders) != 2 || facets.Folders[0].Name != "Games (nas)" || facets.Folders[0].Count != 2 || facets.Folders[1].Name != "Games (usb)" || facets.Folders[1].Count != 1 {
		t.Fatalf("the folders of the filter: %+v", facets.Folders)
	}
	for _, folder := range facets.Folders {
		if strings.Contains(folder.Id, "nas") || folder.Id != folderId(paths[0]) && folder.Id != folderId(paths[1]) {
			t.Fatalf("the filter tells the folder by its id: %+v", folder)
		}
	}

	filter := defaultFilter()
	filter.Folder = strings.ToUpper(folderId(paths[1]))
	filter.Normalize()
	items, p, _ := web.getLibraryWithFacets(filter, "en")
	if p.NumItems != 1 || items[0].Name != "Charlie" {
		t.Fatalf("the games of the folder: %+v", items)
	}
	if filter.SecondaryCount() != 1 || !filter.Active() || filter.query().Get("folder") != folderId(paths[1]) {
		t.Fatalf("the folder is a filter of the Library: %d %v", filter.SecondaryCount(), filter.query())
	}
	// a folder that is not one gives nothing; a value that cannot be one is dropped
	bad := defaultFilter()
	bad.Folder = "../etc"
	bad.Normalize()
	if bad.Folder != "" {
		t.Fatalf("a folder that cannot be one: %q", bad.Folder)
	}
	// with the missing games shown, a folder shows only games of the library
	withMissing := &TitleItemFilter{Folder: folderId(paths[0])}
	withMissing.Normalize()
	if _, p, _ := web.getLibraryWithFacets(withMissing, "en"); p.NumItems != 2 {
		t.Fatalf("the games of the main folder with the missing ones shown: %d", p.NumItems)
	}

	sorted := defaultFilter()
	sorted.SortBy = "folder"
	sorted.Normalize()
	items, _, _ = web.getLibraryWithFacets(sorted, "en")
	names := []string{}
	for _, item := range items {
		names = append(names, item.Name)
	}
	if strings.Join(names, ",") != "Alpha,Bravo,Charlie" {
		t.Fatalf("sorted by folder: %v", names)
	}
	sorted.SortOrder = "desc"
	items, _, _ = web.getLibraryWithFacets(sorted, "en")
	if items[0].Name != "Charlie" {
		t.Fatalf("sorted by folder, the last first: %v", items[0].Name)
	}

	// a smart collection keeps the folder
	if query := cleanSmartQuery("folder=" + folderId(paths[1])); !strings.HasPrefix(query, "folder="+folderId(paths[1])+"&") {
		t.Fatalf("a smart collection of a folder: %q", query)
	}

	// with one folder, the filter is not offered
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.ScanFolders = []string{} })
	if _, _, facets := web.getLibraryWithFacets(defaultFilter(), "en"); facets.ShowFolders {
		t.Fatal("one folder is not a filter")
	}
}

// The Statistics have a row for each folder: its games and the size of the files in it.
func TestStatisticsRowForEachFolder(t *testing.T) {
	web, paths := foldersTestWeb(t)
	stats := web.getStatistics("en")
	if len(stats.Folders) != 3 {
		t.Fatalf("a row for each folder: %+v", stats.Folders)
	}
	want := []FolderSpace{{Name: "Games (nas)", Games: 2, Size: 300}, {Name: "Games (usb)", Games: 1, Size: 300}, {Name: "Extras", Games: 0, Size: 50}}
	for i, row := range stats.Folders {
		if row.Name != want[i].Name || row.Games != want[i].Games || row.Size != want[i].Size || row.Path != "" {
			t.Errorf("row %d: %+v, want %+v", i, row, want[i])
		}
	}
	admin := web.folderSpaces(stats, true)
	if admin[0].Path != paths[0] || !admin[0].HasDisk() || admin[0].FolderPercent+admin[0].OtherPercent > 100 {
		t.Fatalf("the administrators see the path and the disk: %+v", admin[0])
	}
	for _, row := range web.folderSpaces(stats, false) {
		if row.Path != "" || row.HasDisk() {
			t.Fatalf("the others see neither the path nor the disk: %+v", row)
		}
	}
}

// The users who only look see the names of the folders, never their paths or their disks: in
// the Statistics and in the filter of the Library.
func TestReadOnlyUsersSeeNoPathOfTheFolders(t *testing.T) {
	web, paths := foldersTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleIndex()
	web.HandleStatistics()
	page := func(path string, role string) string {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = request.WithContext(context.WithValue(request.Context(), principalKey{}, &Principal{Name: "bob", Role: role}))
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, recorder.Code)
		}
		return recorder.Body.String()
	}
	contains := func(body string, path string) bool {
		escaped := strings.ReplaceAll(path, `\`, `\\`)
		return strings.Contains(body, path) || strings.Contains(body, escaped) || strings.Contains(body, strings.ReplaceAll(path, `\`, "&#92;"))
	}

	stats := page("/statistics.html", ROLE_ADMIN)
	if !strings.Contains(stats, "Space by folder") || !contains(stats, paths[0]) || !strings.Contains(stats, "disk-bar-folder") {
		t.Fatal("the administrators see the folders with their paths and disks")
	}
	for _, role := range []string{ROLE_VIEWER, ROLE_GUEST} {
		stats := page("/statistics.html", role)
		if !strings.Contains(stats, "Games (usb)") || strings.Contains(stats, "disk-bar-folder") {
			t.Fatalf("%s sees the names of the folders, without their disks", role)
		}
		library := page("/index.html?missing=0", role)
		if !strings.Contains(library, "Games (usb)") || !strings.Contains(library, "folder="+folderId(paths[1])) {
			t.Fatalf("%s sees the folders in the filter of the Library", role)
		}
		for _, body := range []string{stats, library} {
			for _, path := range paths {
				if contains(body, path) {
					at := strings.Index(body, filepath.Base(filepath.Dir(path)))
					t.Fatalf("%s sees the path %s: %s", role, path, body[max(at-300, 0):min(at+200, len(body))])
				}
			}
		}
	}
}

// The catalogue of a folder lists only its games and tells the name of the folder, never its path.
func TestCatalogueOfAFolder(t *testing.T) {
	web, paths := foldersTestWeb(t)
	filter := &TitleItemFilter{Folder: folderId(paths[1])}
	data := web.catalogue(filter, "en")
	if len(data.Games) != 1 || data.Games[0].Name != "Charlie" || data.Folder != "Games (usb)" {
		t.Fatalf("%q %+v", data.Folder, data.Games)
	}
}
