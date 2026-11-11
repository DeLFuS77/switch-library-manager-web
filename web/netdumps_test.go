package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// dumpsWeb is pushWeb with a folder of the library, and the scans after a dump counted.
func dumpsWeb(t *testing.T) (web *Web, bob *client, console *client, auth string, library string, scans *atomic.Int32) {
	t.Helper()
	web, _, bob, console, auth, _, _ = pushWeb(t)
	library = t.TempDir()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Folder = library })
	scans = &atomic.Int32{}
	original := netDumpRescan
	netDumpRescan = func(*Web) { scans.Add(1) }
	t.Cleanup(func() { netDumpRescan = original })
	return web, bob, console, auth, library, scans
}

func dumpPath(name string, query string) string {
	path := netInstallUpload + url.PathEscape(name)
	if query != "" {
		path += "?" + query
	}
	return path
}

type dumpAnswer struct {
	Have  int64  `json:"have"`
	Done  bool   `json:"done"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

func dumpReply(t *testing.T, got *httptest.ResponseRecorder) dumpAnswer {
	t.Helper()
	answer := dumpAnswer{}
	if err := json.Unmarshal(got.Body.Bytes(), &answer); err != nil {
		t.Fatalf("not JSON: %d %s", got.Code, got.Body.String())
	}
	return answer
}

// A console uploads a dump in pieces: it asks where to go on, a wrong offset tells where the app
// is, and the last piece gives the file its name in the folder of the dumps and scans the
// library. The same name again takes " (2)".
func TestNetDumpUploadedInPieces(t *testing.T) {
	_, _, console, auth, library, scans := dumpsWeb(t)
	name := "Game [0100000000040000][v0].nsp"
	if got := dumpReply(t, console.do("GET", dumpPath(name, ""), nil, "Authorization", auth)); got.Have != 0 {
		t.Fatalf("nothing yet: %+v", got)
	}
	first := sendBody(console, "PUT", dumpPath(name, ""), []byte("hello "), "Authorization", auth)
	if first.Code != http.StatusOK || dumpReply(t, first).Have != 6 {
		t.Fatalf("the first piece: %d %s", first.Code, first.Body.String())
	}
	if got := dumpReply(t, console.do("GET", dumpPath(name, ""), nil, "Authorization", auth)); got.Have != 6 {
		t.Fatalf("where it goes on: %+v", got)
	}
	wrong := sendBody(console, "PUT", dumpPath(name, "offset=3"), []byte("lo world"), "Authorization", auth)
	if wrong.Code != http.StatusConflict || dumpReply(t, wrong).Have != 6 {
		t.Fatalf("a wrong offset: %d %s", wrong.Code, wrong.Body.String())
	}
	last := sendBody(console, "PUT", dumpPath(name, "offset=6&done=1"), []byte("world"), "Authorization", auth)
	answer := dumpReply(t, last)
	if last.Code != http.StatusOK || !answer.Done || answer.Have != 11 || answer.Name != name {
		t.Fatalf("the last piece: %d %s", last.Code, last.Body.String())
	}
	folder := filepath.Join(library, netDumpsFolderName)
	if data, err := os.ReadFile(filepath.Join(folder, name)); err != nil || string(data) != "hello world" {
		t.Fatalf("the file: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(folder, name+netDumpPartSuffix)); !os.IsNotExist(err) {
		t.Fatal("the part is gone")
	}
	if scans.Load() != 1 {
		t.Fatalf("the library is scanned once: %d", scans.Load())
	}

	// the same name again, whole in one request
	again := dumpReply(t, sendBody(console, "PUT", dumpPath(name, "done=1"), []byte("again"), "Authorization", auth))
	if again.Name != "Game [0100000000040000][v0] (2).nsp" {
		t.Fatalf("a name of its own: %+v", again)
	}
	if data, _ := os.ReadFile(filepath.Join(folder, again.Name)); string(data) != "again" {
		t.Fatalf("the second file: %q", data)
	}
	// done without anything sent is refused
	if got := sendBody(console, "PUT", dumpPath("Other.xci", "done=1"), nil, "Authorization", auth); got.Code != http.StatusBadRequest {
		t.Fatalf("an empty dump: %d", got.Code)
	}
}

// Only the name of a file an installer takes, without folders.
func TestNetDumpNames(t *testing.T) {
	_, _, console, auth, library, _ := dumpsWeb(t)
	for _, name := range []string{"game.txt", "game.nup", "..", "game", `..\game.nsp`, "a/../../game.nsp"} {
		got := sendBody(console, "PUT", dumpPath(name, "done=1"), []byte("data"), "Authorization", auth)
		if got.Code == http.StatusOK {
			t.Errorf("%q: %d %s", name, got.Code, got.Body.String())
		}
	}
	entries, _ := os.ReadDir(filepath.Join(library, netDumpsFolderName))
	if len(entries) != 0 {
		t.Fatalf("nothing written: %v", entries)
	}
	for name, want := range map[string]string{"Game.XCZ": "Game.XCZ", ".hidden.nsz": "hidden.nsz", "game.nsp": "game.nsp", "a\\b.nsp": "", "notes.md": ""} {
		if got := netDumpName(name); got != want {
			t.Errorf("%q: %q, not %q", name, got, want)
		}
	}
}

// The folder of the settings is used, the uploads can be turned off, a dump that does not fit is
// refused before it is sent, and only the key of a console uploads.
func TestNetDumpSettingsSpaceAndKey(t *testing.T) {
	web, bob, console, auth, _, _ := dumpsWeb(t)
	own := filepath.Join(t.TempDir(), "dumps")
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.UploadFolder = own })
	if got := sendBody(console, "PUT", dumpPath("Game.nsp", "done=1"), []byte("data"), "Authorization", auth); got.Code != http.StatusOK {
		t.Fatalf("to the folder of the settings: %d %s", got.Code, got.Body.String())
	}
	if _, err := os.Stat(filepath.Join(own, "Game.nsp")); err != nil {
		t.Fatalf("in the folder of the settings: %v", err)
	}

	huge := httptest.NewRequest("PUT", dumpPath("Huge.xci", ""), strings.NewReader("data"))
	huge.RemoteAddr = "192.168.1.10:1234"
	huge.Header.Set("Authorization", auth)
	huge.ContentLength = 1 << 60
	recorder := httptest.NewRecorder()
	console.handler.ServeHTTP(recorder, huge)
	if recorder.Code != http.StatusInsufficientStorage {
		t.Fatalf("does not fit: %d %s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(own, "Huge.xci"+netDumpPartSuffix)); !os.IsNotExist(err) {
		t.Fatal("nothing written of what does not fit")
	}

	if got := sendBody(bob, "PUT", dumpPath("Game.nsp", ""), []byte("data")); got.Code == http.StatusOK {
		t.Fatalf("a session is not a console: %d", got.Code)
	}
	off := false
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.AllowUploads = &off })
	if got := sendBody(console, "PUT", dumpPath("Game.nsp", ""), []byte("data"), "Authorization", auth); got.Code != http.StatusForbidden {
		t.Fatalf("turned off: %d", got.Code)
	}
	if got := console.do("GET", dumpPath("Game.nsp", ""), nil, "Authorization", auth); got.Code != http.StatusForbidden {
		t.Fatalf("turned off, asked: %d", got.Code)
	}
}

// The uploads are on unless turned off, in the settings and in their page.
func TestNetDumpUploadsOnByDefault(t *testing.T) {
	if !(settings.NetInstallOptions{}).UploadsAllowed() {
		t.Fatal("on by default")
	}
	off := false
	if (settings.NetInstallOptions{AllowUploads: &off}).UploadsAllowed() {
		t.Fatal("off when turned off")
	}
}

// Settings keep whether the consoles may upload what they dump, and where it goes; the page shows
// the switch on until it is turned off.
func TestNetDumpSettingsAreSaved(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleSettings()
	current := settings.ReadSettings(web.dataFolder)
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/settings.html", nil))
	if !strings.Contains(page.Body.String(), `name="net_install_allow_uploads" value="true" checked`) {
		t.Fatal("on at first")
	}
	folder := t.TempDir()
	form := url.Values{"scan_folders": {t.TempDir()}, "sync_interval_hours": {strconv.Itoa(current.SyncIntervalHours)},
		"quarantine_limit": {strconv.Itoa(current.Quarantine.LimitGB)}, "net_install_upload_folder": {"  " + folder + " "}}
	if response := postForm(web, "/settings.html", form); response.Code != http.StatusOK {
		t.Fatalf("saved: %d %s", response.Code, response.Body.String())
	}
	saved := settings.ReadSettings(web.dataFolder).NetInstall
	if saved.UploadsAllowed() || saved.UploadFolder != folder {
		t.Fatalf("turned off, with its folder: %+v", saved)
	}
	form.Set("net_install_allow_uploads", "true")
	postForm(web, "/settings.html", form)
	if !settings.ReadSettings(web.dataFolder).NetInstall.UploadsAllowed() {
		t.Fatal("turned on again")
	}
}
