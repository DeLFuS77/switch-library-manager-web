package web

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

const testCheats = "[Game 1.0.1 by someone TID: 0100000000010000 BID: 2222222222222222]\n\n{Master Code}\n580F0000 01234567\n\n[Infinite HP]\n580F0000 03CDB540\n640F0000 00000000 0000270F\n\n[Max Gold]\n640F0000 00000000 0098967F\n"

// cheatsServer serves a cheats database like switch-cheats-db: the game has cheats for its
// update only, another game for its first version only.
func cheatsServer(t *testing.T) *httptest.Server {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, text := range map[string]string{
		"contents/0100000000010000/cheats/2222222222222222.txt": testCheats,
		"contents/0100000000020000/cheats/3333333333333333.txt": "[Fly]\n04000000 00000000 00000001\n",
	} {
		entry, _ := writer.Create(name)
		io.WriteString(entry, text)
	}
	writer.Close()
	versions := `{"0100000000010000": {"0": "1111111111111111", "65536": "2222222222222222", "latest": 65536, "title": "Game"},
		"0100000000020000": {"0": "3333333333333333", "131072": "4444444444444444", "latest": 131072, "title": "Other"},
		"0100000000030000": {"0": "5555555555555555", "title": "No cheats"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/versions.json":
			io.WriteString(w, versions)
		case "/VERSION":
			io.WriteString(w, "2026-10-01\n")
		case "/contents_complete.zip":
			w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	original := cheatsSource
	cheatsSource.versions, cheatsSource.zip, cheatsSource.release = server.URL+"/versions.json", server.URL+"/contents_complete.zip", server.URL+"/VERSION"
	t.Cleanup(func() { cheatsSource = original })
	return server
}

func cheatsWeb(t *testing.T) *Web {
	t.Helper()
	cheatsServer(t)
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	game := func(id string, latest int) *db.SwitchGameFiles {
		files := &db.SwitchGameFiles{BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: id + ".nsp", BaseFolder: t.TempDir()}, Metadata: &switchfs.ContentMetaAttributes{TitleId: id}},
			Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}, LatestUpdate: latest}
		if latest > 0 {
			files.Updates[latest] = db.SwitchFileInfo{Metadata: &switchfs.ContentMetaAttributes{TitleId: id[:13] + "800", Version: latest}}
		}
		return files
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": game("0100000000010000", 65536),
		// the cheats are for v0, the library has the update
		"0100000000020": game("0100000000020000", 131072),
		"0100000000030": game("0100000000030000", 0),
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})
	return web
}

func TestCheatsAreDownloadedAndMatchTheVersion(t *testing.T) {
	web := cheatsWeb(t)
	if web.cheats() != nil {
		t.Fatal("no cheats before they are downloaded")
	}
	web.downloadCheats()
	cheats := web.cheats()
	if cheats == nil {
		t.Fatal("the cheats were downloaded")
	}
	if !cheats.has("0100000000010000", 65536) || cheats.has("0100000000010000", 0) || cheats.has("0100000000020000", 131072) || cheats.has("0100000000030000", 0) {
		t.Fatal("cheats are those of the build of the version")
	}
	if got := settings.ReadSettings(web.dataFolder).LocalizedTitlesEtags["cheats-release"]; got != "2026-10-01" {
		t.Fatalf("the release is remembered: %q", got)
	}

	info := web.cheatsInfo("0100000000010000", 65536)
	if info == nil || info.BuildID != "2222222222222222" || strings.Join(info.Names, "|") != "Master Code|Infinite HP|Max Gold" {
		t.Fatalf("the cheats of the version: %+v", info)
	}
	if other := web.cheatsInfo("0100000000020000", 131072); other == nil || other.BuildID != "" || len(other.OtherVersions) != 1 || other.OtherVersions[0] != 0 {
		t.Fatalf("cheats of another version only: %+v", other)
	}
	if web.cheatsInfo("0100000000030000", 0) != nil {
		t.Fatal("a game without cheats")
	}

	// the library marks the game, and filters by it
	filter := defaultFilter()
	filter.Extra = EXTRA_CHEATS
	items, _, facets := web.getLibraryWithFacets(filter, "en")
	if len(items) != 1 || items[0].Id != "0100000000010000" || !items[0].Cheats || facets.Cheats != 1 {
		t.Fatalf("the games with cheats for their version: %+v %d", items, facets.Cheats)
	}

	// the cheats are given as Atmosphère reads them
	web.HandleCheats()
	get := func(url string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		web.router.ServeHTTP(response, httptest.NewRequest("GET", url, nil))
		return response
	}
	if text := get("/api/titles/0100000000010000/cheats.txt"); text.Code != 200 || text.Body.String() != testCheats || !strings.Contains(text.Header().Get("Content-Disposition"), "2222222222222222.txt") {
		t.Fatalf("the text of the cheats: %d %q", text.Code, text.Header().Get("Content-Disposition"))
	}
	if missing := get("/api/titles/0100000000020000/cheats.txt"); missing.Code != 404 {
		t.Fatalf("no cheats for the version: %d", missing.Code)
	}
	all := get("/api/cheats.zip")
	archive, err := zip.NewReader(bytes.NewReader(all.Body.Bytes()), int64(all.Body.Len()))
	if err != nil || len(archive.File) != 1 || archive.File[0].Name != "atmosphere/contents/0100000000010000/cheats/2222222222222222.txt" {
		t.Fatalf("the cheats of the library for the SD card: %v", err)
	}

	// the game page lists the cheats of the version, or tells the versions they are for
	web.HandleTitle()
	if page := get("/title/0100000000010000.html"); page.Code != 200 || !strings.Contains(page.Body.String(), "Infinite HP") || !strings.Contains(page.Body.String(), "/api/titles/0100000000010000/cheats.zip") {
		t.Fatalf("the game page with its cheats: %d", page.Code)
	}
	if page := get("/title/0100000000020000.html"); page.Code != 200 || !strings.Contains(page.Body.String(), "(v0)") {
		t.Fatalf("the game page with cheats of another version: %d", page.Code)
	}

	// once a day at most
	os.Remove(web.dataFolder + "/" + CHEATS_ZIP_FILENAME)
	web.downloadCheats()
	if _, err := os.Stat(web.dataFolder + "/" + CHEATS_ZIP_FILENAME); err == nil {
		t.Fatal("not downloaded again so soon")
	}
	web.cheatsFile.checked = time.Time{}
	web.downloadCheats()
	if _, err := os.Stat(web.dataFolder + "/" + CHEATS_ZIP_FILENAME); err != nil {
		t.Fatal("downloaded again when it is missing")
	}

	// turned off, nothing is shown
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CheatsDisabled = true })
	if web.cheats() != nil {
		t.Fatal("the cheats are turned off")
	}
}

func TestCheatNames(t *testing.T) {
	names := cheatNames(testCheats)
	if strings.Join(names, "|") != "Master Code|Infinite HP|Max Gold" {
		t.Fatalf("names: %v", names)
	}
}
