package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// saveZip is a save as NXDock sends it: the files of the save with relative paths.
func saveZip(t *testing.T, names ...string) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write([]byte("data of " + name))
	}
	writer.Close()
	return archive.Bytes()
}

// sendBody is client.do with a body of bytes.
func sendBody(c *client, method string, path string, body []byte, headers ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.RemoteAddr = "192.168.1.10:1234"
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	for _, cookie := range c.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	return recorder
}

type savesAnswer struct {
	Saves []netSave `json:"saves"`
}

func listSaves(t *testing.T, c *client, path string, headers ...string) []netSave {
	t.Helper()
	got := c.do("GET", path, nil, headers...)
	var answer savesAnswer
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &answer) != nil {
		t.Fatalf("the saves: %d %s", got.Code, got.Body.String())
	}
	return answer.Saves
}

// A console uploads a save of a game, which is kept for the owner of its key with its note, the
// version of the game, the profile and the name of the console; it lists it with the name the
// library gives the game and takes it back as it was.
func TestNetSaveUploadedListedAndTakenBack(t *testing.T) {
	web, _, bob, console, auth, _, _ := pushWeb(t)
	body := saveZip(t, "save/progress.bin", "save/settings/options.dat")
	sent := sendBody(console, "POST", "/install/saves/0100000000010000?note=Before+the+last+boss&version=65536&account=Bob", body, "Authorization", auth)
	var answer struct {
		Id   string `json:"id"`
		When int64  `json:"when"`
		Size int64  `json:"size"`
	}
	if sent.Code != http.StatusOK || json.Unmarshal(sent.Body.Bytes(), &answer) != nil || !netSaveIdPattern.MatchString(answer.Id) ||
		answer.Size != int64(len(body)) || time.Since(time.Unix(answer.When, 0)) > time.Minute {
		t.Fatalf("uploaded: %d %s", sent.Code, sent.Body.String())
	}
	saves := listSaves(t, console, "/install/saves", "Authorization", auth)
	want := netSave{TitleId: "0100000000010000", Game: "Game", Id: answer.Id, When: answer.When, Note: "Before the last boss", Version: 65536,
		Account: "Bob", Console: "Switch", Size: int64(len(body))}
	if len(saves) != 1 || saves[0] != want {
		t.Fatalf("listed: %+v", saves)
	}
	if other := listSaves(t, console, "/install/saves?title=0100000000020000", "Authorization", auth); len(other) != 0 {
		t.Fatalf("only the saves of the game asked: %+v", other)
	}
	back := console.do("GET", "/install/saves/0100000000010000/"+answer.Id, nil, "Authorization", auth)
	if back.Code != http.StatusOK || !bytes.Equal(back.Body.Bytes(), body) || back.Header().Get("Content-Type") != "application/zip" ||
		back.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatalf("taken back: %d %v", back.Code, back.Header())
	}
	// the user sees it on the web too, and nobody else does
	if web := listSaves(t, bob, "/api/saves"); len(web) != 1 || web[0].Id != answer.Id {
		t.Fatalf("on the web: %+v", web)
	}
	if _, err := os.Stat(filepath.Join(web.netSavesUserFolder("bob"), "0100000000010000", answer.Id+".zip")); err != nil {
		t.Fatalf("kept in the folder of bob: %v", err)
	}
	if inside, err := filepath.Rel(filepath.Join(web.dataFolder, VAULT_FOLDER), web.netSavesUserFolder("bob")); err == nil && !strings.HasPrefix(inside, "..") {
		t.Fatal("never in the folder of the vault of JKSV, which its WebDAV share reads")
	}
}

// What is not a ZIP of a save is refused: not a ZIP, an empty one, one with a path that climbs
// out or starts at the root, a version that is not a number. Nothing is kept of them.
func TestNetSaveRefusesWhatIsNotASave(t *testing.T) {
	web, _, _, console, auth, _, _ := pushWeb(t)
	for name, body := range map[string][]byte{
		"not a zip":    []byte("not a zip"),
		"empty":        saveZip(t),
		"climbs out":   saveZip(t, "save/../../evil.bin"),
		"from root":    saveZip(t, "/etc/evil.bin"),
		"a drive":      saveZip(t, "C:/evil.bin"),
		"a backslash":  saveZip(t, `..\evil.bin`),
		"only a climb": saveZip(t, ".."),
	} {
		if got := sendBody(console, "POST", "/install/saves/0100000000010000", body, "Authorization", auth); got.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	if got := sendBody(console, "POST", "/install/saves/0100000000010000?version=one", saveZip(t, "a"), "Authorization", auth); got.Code != http.StatusBadRequest {
		t.Errorf("a version that is not a number: %d", got.Code)
	}
	if saves := listSaves(t, console, "/install/saves", "Authorization", auth); len(saves) != 0 {
		t.Fatalf("nothing kept: %+v", saves)
	}
	entries, _ := os.ReadDir(filepath.Join(web.netSavesUserFolder("bob"), "0100000000010000"))
	if len(entries) != 0 {
		t.Fatalf("no file left: %v", entries)
	}
}

// The note and the profile are on one line and not too long.
func TestNetSaveNoteIsCut(t *testing.T) {
	_, _, _, console, auth, _, _ := pushWeb(t)
	note := strings.Repeat("é", 250)
	got := sendBody(console, "POST", "/install/saves/0100000000010000?note="+note+"%0Anext&account="+strings.Repeat("a", 80), saveZip(t, "a"), "Authorization", auth)
	if got.Code != http.StatusOK {
		t.Fatalf("uploaded: %d %s", got.Code, got.Body.String())
	}
	saves := listSaves(t, console, "/install/saves", "Authorization", auth)
	if len(saves) != 1 || len([]rune(saves[0].Note)) != maxNetSaveNote || strings.Contains(saves[0].Note, "\n") || len(saves[0].Account) != maxNetSaveAccount {
		t.Fatalf("cut: %+v", saves)
	}
}

// Ten saves are kept per game: the oldest that is not pinned goes, never a pinned one.
func TestNetSavesKeepTenPerGame(t *testing.T) {
	web, _, bob, console, auth, _, _ := pushWeb(t)
	ids := []string{}
	for i := 0; i < netSavesKeep; i++ {
		got := sendBody(console, "POST", "/install/saves/0100000000010000?note="+strconv.Itoa(i), saveZip(t, "a"), "Authorization", auth)
		var answer struct{ Id string }
		json.Unmarshal(got.Body.Bytes(), &answer)
		ids = append(ids, answer.Id)
	}
	// the order of the saves is their time: told apart here without waiting seconds
	web.netSaves.Lock()
	saves, _ := web.readNetSaves("bob")
	for i := range saves {
		saves[i].When -= int64(100 - i)
	}
	web.writeNetSaves("bob", saves)
	web.netSaves.Unlock()
	// the oldest is pinned: the second oldest goes
	if got := sendJSON(bob, "PATCH", "/api/saves/0100000000010000/"+ids[0], `{"pinned": true}`); got.Code != http.StatusOK {
		t.Fatalf("pinned: %d %s", got.Code, got.Body.String())
	}
	sendBody(console, "POST", "/install/saves/0100000000020000", saveZip(t, "a"), "Authorization", auth)
	newest := sendBody(console, "POST", "/install/saves/0100000000010000?note=new", saveZip(t, "a"), "Authorization", auth)
	if newest.Code != http.StatusOK {
		t.Fatalf("one more: %d", newest.Code)
	}
	kept := listSaves(t, console, "/install/saves?title=0100000000010000", "Authorization", auth)
	notes := map[string]bool{}
	for _, save := range kept {
		notes[save.Note] = true
	}
	if len(kept) != netSavesKeep || !notes["0"] || notes["1"] || !notes["new"] || kept[0].Note != "new" {
		t.Fatalf("ten kept, the pinned one among them: %+v", kept)
	}
	if _, err := os.Stat(web.netSavePath("bob", "0100000000010000", ids[1])); !os.IsNotExist(err) {
		t.Fatalf("the file of the save that went is deleted: %v", err)
	}
	if other := listSaves(t, console, "/install/saves?title=0100000000020000", "Authorization", auth); len(other) != 1 {
		t.Fatalf("the saves of another game count apart: %+v", other)
	}
}

// On the web the user writes a note, pins, downloads and deletes their saves; another user sees
// none of them and changes none, and a console key cannot use the paths of the web.
func TestNetSavesOnTheWeb(t *testing.T) {
	_, admin, bob, console, auth, _, _ := pushWeb(t)
	body := saveZip(t, "a")
	got := sendBody(console, "POST", "/install/saves/0100000000010000", body, "Authorization", auth)
	var answer struct{ Id string }
	json.Unmarshal(got.Body.Bytes(), &answer)
	path := "/api/saves/0100000000010000/" + answer.Id

	changed := sendJSON(bob, "PATCH", path, `{"note": "  after the castle  "}`)
	if changed.Code != http.StatusOK || !strings.Contains(changed.Body.String(), `"note":"after the castle"`) || !strings.Contains(changed.Body.String(), `"pinned":false`) {
		t.Fatalf("the note: %d %s", changed.Code, changed.Body.String())
	}
	if pinned := sendJSON(bob, "PATCH", path, `{"pinned": true}`); !strings.Contains(pinned.Body.String(), `"note":"after the castle"`) || !strings.Contains(pinned.Body.String(), `"pinned":true`) {
		t.Fatalf("pinned keeps the note: %s", pinned.Body.String())
	}
	if download := bob.do("GET", path, nil); download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), body) ||
		!strings.Contains(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("downloaded: %d", download.Code)
	}

	if saves := listSaves(t, admin, "/api/saves"); len(saves) != 0 {
		t.Fatalf("another user sees none: %+v", saves)
	}
	if code := sendJSON(admin, "PATCH", path, `{"note": "mine"}`).Code; code != http.StatusNotFound {
		t.Fatalf("another user changes none: %d", code)
	}
	if code := admin.do("DELETE", path, nil).Code; code != http.StatusNotFound {
		t.Fatalf("another user deletes none: %d", code)
	}
	if code := admin.do("GET", path, nil).Code; code != http.StatusNotFound {
		t.Fatalf("another user downloads none: %d", code)
	}
	if code := console.do("GET", "/api/saves", nil, "Authorization", auth).Code; code == http.StatusOK {
		t.Fatalf("a console key on the web: %d", code)
	}
	if code := console.do("DELETE", "/install/saves/0100000000010000/"+answer.Id, nil, "Authorization", auth).Code; code == http.StatusOK {
		t.Fatalf("a console deletes nothing: %d", code)
	}

	if deleted := bob.do("DELETE", path, nil); deleted.Code != http.StatusOK {
		t.Fatalf("deleted: %d %s", deleted.Code, deleted.Body.String())
	}
	if saves := listSaves(t, bob, "/api/saves"); len(saves) != 0 {
		t.Fatalf("gone: %+v", saves)
	}
	if code := bob.do("DELETE", path, nil).Code; code != http.StatusNotFound {
		t.Fatalf("deleted twice: %d", code)
	}
}

// The saves answer behind the gate of the network install, and only to the key of a console.
func TestNetSavesBehindTheGate(t *testing.T) {
	web, _, bob, console, auth, _, _ := pushWeb(t)
	if code := sendBody(bob, "POST", "/install/saves/0100000000010000", saveZip(t, "a")).Code; code == http.StatusOK {
		t.Fatalf("a session is not a console: %d", code)
	}
	if code := (&client{t: t, handler: console.handler, ip: "203.0.113.5"}).do("GET", "/install/saves", nil, "Authorization", auth).Code; code != http.StatusForbidden {
		t.Fatalf("from outside the local network: %d", code)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	if code := sendBody(console, "POST", "/install/saves/0100000000010000", saveZip(t, "a"), "Authorization", auth).Code; code != http.StatusNotFound {
		t.Fatalf("turned off: %d", code)
	}
}

// The page Saves shows the saves of the user signed in by game, with their note, and is in the
// menu of the account while the network install is on.
func TestNetSavesPage(t *testing.T) {
	web, admin, bob, console, auth, _, _ := pushWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleNetSavesPage()
	sendBody(console, "POST", "/install/saves/0100000000010000?note=At+the+castle&version=65536&account=Bob", saveZip(t, "a"), "Authorization", auth)
	page := bob.do("GET", "/saves", nil)
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, "At the castle") || !strings.Contains(body, `href="/title/0100000000010000.html">Game</a>`) ||
		!strings.Contains(body, "1 copy") || !strings.Contains(body, `data-net-save="/api/saves/0100000000010000/`) || !strings.Contains(body, "v65536") ||
		!strings.Contains(body, `href="/saves"`) {
		t.Fatalf("the page: %d %s", page.Code, body)
	}
	if other := admin.do("GET", "/saves", nil).Body.String(); !strings.Contains(other, "No saves yet") || strings.Contains(other, "At the castle") {
		t.Fatal("another user sees none of them")
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	if off := bob.do("GET", "/saves", nil).Body.String(); strings.Contains(off, `href="/saves"`) {
		t.Fatal("not in the menu while the network install is off")
	}
}
