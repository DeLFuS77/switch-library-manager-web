package web

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// jksvZip makes a backup like JKSV does: a ZIP with the save files and its meta file.
func jksvZip(t *testing.T, titleId uint64, when time.Time) []byte {
	t.Helper()
	meta := make([]byte, 86)
	binary.LittleEndian.PutUint32(meta[0:4], jksvMetaMagic)
	meta[4] = 1
	binary.LittleEndian.PutUint64(meta[5:13], titleId)
	binary.LittleEndian.PutUint64(meta[49:57], uint64(when.Unix()))
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range map[string][]byte{jksvMetaName: meta, "save.dat": []byte("progress")} {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		file.Write(data)
	}
	writer.Close()
	return buffer.Bytes()
}

// vaultTestServer is the app with the vault on and the whole chain of handlers of the server.
func vaultTestServer(t *testing.T) (*Web, http.Handler) {
	t.Helper()
	web := newTestWeb(t)
	auth, err := newAuth(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	web.auth = auth
	web.state.set(testDatabases(t))
	hash, err := hashVaultPassword("vault-secret-42")
	if err != nil {
		t.Fatal(err)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Vault = settings.VaultOptions{Enabled: true, User: "jksv", PasswordHash: hash}
	})
	return web, withBodyLimit(web.withVault(http.NotFoundHandler()))
}

func davRequest(handler http.Handler, method, path string, body []byte, headers ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.SetBasicAuth("jksv", "vault-secret-42")
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i] == "Authorization" && headers[i+1] == "" {
			request.Header.Del("Authorization")
			continue
		}
		request.Header.Set(headers[i], headers[i+1])
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestVaultSpeaksLikeJKSVExpects(t *testing.T) {
	web, handler := vaultTestServer(t)

	// the listing of the base folder when JKSV starts
	if r := davRequest(handler, "PROPFIND", "/dav/JKSV/", nil, "Depth", "1"); r.Code != http.StatusMultiStatus || !strings.Contains(r.Body.String(), "multistatus") {
		t.Fatalf("propfind: %d %s", r.Code, r.Body.String())
	}
	// a folder per game: JKSV expects 201
	if r := davRequest(handler, "MKCOL", "/dav/JKSV/Known%20Game/", nil); r.Code != http.StatusCreated {
		t.Fatalf("mkcol: %d", r.Code)
	}
	backup := jksvZip(t, 0x0100000000010000, time.Date(2026, 5, 4, 20, 0, 0, 0, time.UTC))
	if r := davRequest(handler, http.MethodPut, "/dav/JKSV/Known%20Game/Player%20-%202026.05.04.zip", backup); r.Code != http.StatusCreated {
		t.Fatalf("put: %d", r.Code)
	}
	listing := davRequest(handler, "PROPFIND", "/dav/JKSV/Known%20Game/", nil, "Depth", "1").Body.String()
	if !strings.Contains(listing, "getcontentlength") || !strings.Contains(listing, "2026.05.04.zip") {
		t.Fatalf("the backup is listed with its size: %s", listing)
	}
	if r := davRequest(handler, http.MethodGet, "/dav/JKSV/Known%20Game/Player%20-%202026.05.04.zip", nil); r.Code != http.StatusOK || !bytes.Equal(r.Body.Bytes(), backup) {
		t.Fatalf("get: %d", r.Code)
	}

	// matched to the game of the library by the title ID inside the ZIP
	games := web.saveGames("en")
	if len(games) != 1 || games[0].TitleId != "0100000000010000" || !games[0].InLibrary || games[0].Backups[0].Time.Year() != 2026 {
		t.Fatalf("games: %+v", games)
	}
	if saves := web.titleSaves("0100000000010000", "en"); len(saves) != 1 {
		t.Fatalf("on the game page: %+v", saves)
	}

	// renamed, then deleted: JKSV expects 201 or 204, then 204
	if r := davRequest(handler, "MOVE", "/dav/JKSV/Known%20Game/Player%20-%202026.05.04.zip", nil, "Destination", "http://example.com/dav/JKSV/Known%20Game/Renamed.zip"); r.Code != http.StatusCreated && r.Code != http.StatusNoContent {
		t.Fatalf("move: %d", r.Code)
	}
	if r := davRequest(handler, http.MethodDelete, "/dav/JKSV/Known%20Game/Renamed.zip", nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", r.Code)
	}
	waitForBackgroundWork(web)
}

func TestVaultRefusesWithoutTheRightPassword(t *testing.T) {
	web, handler := vaultTestServer(t)
	if r := davRequest(handler, "PROPFIND", "/dav/JKSV/", nil, "Authorization", ""); r.Code != http.StatusUnauthorized || r.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("no password: %d", r.Code)
	}
	wrong := func() int {
		request := httptest.NewRequest("PROPFIND", "/dav/JKSV/", nil)
		request.SetBasicAuth("jksv", "not-the-password")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	codes := map[int]int{}
	for i := 0; i < maxLoginFailures+1; i++ {
		codes[wrong()]++
	}
	if codes[http.StatusTooManyRequests] == 0 {
		t.Fatalf("guessing is blocked like the login: %v", codes)
	}

	// turned off, the vault does not exist
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Vault.Enabled = false })
	if r := davRequest(handler, "PROPFIND", "/dav/JKSV/", nil); r.Code != http.StatusNotFound {
		t.Fatalf("off: %d", r.Code)
	}
}

func TestVaultTakesBigBackups(t *testing.T) {
	_, handler := vaultTestServer(t)
	davRequest(handler, "MKCOL", "/dav/JKSV/Big/", nil)
	big := bytes.Repeat([]byte("x"), maxRequestBody+1024)
	if r := davRequest(handler, http.MethodPut, "/dav/JKSV/Big/big.zip", big); r.Code != http.StatusCreated {
		t.Fatalf("a backup bigger than the forms: %d", r.Code)
	}
}

func TestVaultPathStaysInside(t *testing.T) {
	web := newTestWeb(t)
	for _, bad := range []string{"../settings.json", "JKSV/../../x.zip", "JKSV/game/save.dat", ""} {
		if path, ok := web.vaultPath(bad); ok && !strings.HasPrefix(path, web.vaultRoot()) || ok && !strings.HasSuffix(path, ".zip") {
			t.Errorf("%q gives %q", bad, path)
		}
	}
	if path, ok := web.vaultPath("../../etc/passwd.zip"); ok && !strings.HasPrefix(path, web.vaultRoot()) {
		t.Fatalf("outside: %q", path)
	}
	if _, ok := web.vaultPath("JKSV/Game/a.zip"); !ok {
		t.Fatal("a backup of the vault")
	}
}

func TestPruneVaultFolderKeepsTheNewest(t *testing.T) {
	folder := t.TempDir()
	for i := 1; i <= 4; i++ {
		data := jksvZip(t, 0x0100000000010000, time.Date(2026, 1, i, 0, 0, 0, 0, time.UTC))
		os.WriteFile(filepath.Join(folder, "b"+string(rune('0'+i))+".zip"), data, 0644)
	}
	if removed := pruneVaultFolder(folder, 2); removed != 2 {
		t.Fatalf("two removed: %d", removed)
	}
	for _, name := range []string{"b3.zip", "b4.zip"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatalf("%s is one of the newest", name)
		}
	}
}

func TestJksvMetaOfAnOldBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.zip")
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, _ := writer.Create("save.dat")
	file.Write([]byte("x"))
	writer.Close()
	os.WriteFile(path, buffer.Bytes(), 0644)
	if _, _, ok := jksvMeta(path); ok {
		t.Fatal("a backup without the meta file of JKSV")
	}
}

func TestSavesPage(t *testing.T) {
	web := demoWeb(t)
	web.HandleSaves()
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest("GET", "/saves.html", nil))
	body := recorder.Body.String()
	if recorder.Code != 200 || !strings.Contains(body, "save-game") || !strings.Contains(body, "/saves/download?path=") {
		t.Fatalf("the backups of the demo: %d", recorder.Code)
	}
}
