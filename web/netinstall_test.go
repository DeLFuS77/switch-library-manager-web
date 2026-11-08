package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// netWeb is keysWeb with a library of a game, its two updates and a DLC, the network install
// turned on, and a key of the role Console made by bob, a player.
func netWeb(t *testing.T) (*Web, *client, string) {
	t.Helper()
	web, admin := keysWeb(t)
	dir := t.TempDir()
	file := func(name string, id string, version int) db.SwitchFileInfo {
		os.WriteFile(filepath.Join(dir, name), []byte("data of "+name), 0644)
		return db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: name, BaseFolder: dir, Size: int64(len("data of " + name))},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: id, Version: version}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": {BaseExist: true, File: file("Game [0100000000010000][v0].nsp", "0100000000010000", 0),
			Updates: map[int]db.SwitchFileInfo{65536: file("Game [0100000000010800][v65536].nsp", "0100000000010800", 65536),
				131072: file("Game [0100000000010800][v131072].nsz", "0100000000010800", 131072)},
			LatestUpdate: 131072,
			Dlc:          map[string]db.SwitchFileInfo{"0100000000011001": file("Game Extra [0100000000011001][v0].nsp", "0100000000011001", 0)}},
	}})
	web.HandleNetInstall()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = true; s.NetInstall.Message = "Hello" })
	bob := &client{t: t, handler: admin.handler}
	bob.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	key := createKey(t, bob, "Switch", ROLE_CONSOLE)
	if key.Role != ROLE_CONSOLE {
		t.Fatalf("the key of a console: %+v", key.KeyRow)
	}
	return web, admin, key.Key
}

func TestNetInstallListsAndGivesTheLibrary(t *testing.T) {
	_, admin, key := netWeb(t)
	console := &client{t: t, handler: admin.handler}
	auth := basic("switch", key)

	root := console.do("GET", "/install/dbi/", nil, "Authorization", auth)
	if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), `<a href="Games/">Games/</a>`) || !strings.Contains(root.Body.String(), "By%20game/") {
		t.Fatalf("the folders: %d %s", root.Code, root.Body.String())
	}
	games := console.do("GET", "/install/dbi/Games/", nil, "Authorization", auth).Body.String()
	if !strings.Contains(games, "[0100000000010000][v0].nsp</a>") {
		t.Fatalf("the games: %s", games)
	}
	updates := console.do("GET", "/install/dbi/Updates/", nil, "Authorization", auth).Body.String()
	if !strings.Contains(updates, "[v131072].nsz") || strings.Contains(updates, "[v65536]") {
		t.Fatalf("only the newest update: %s", updates)
	}
	byGame := console.do("GET", "/install/dbi/By%20game/Game/", nil, "Authorization", auth).Body.String()
	if strings.Count(byGame, "[0100000000") != 3 {
		t.Fatalf("a game with its update and DLC: %s", byGame)
	}

	name := "Game [0100000000010000][v0].nsp"
	download := console.do("GET", "/install/dbi/Games/"+url.PathEscape(name), nil, "Authorization", auth)
	if download.Code != http.StatusOK || download.Body.String() != "data of "+name {
		t.Fatalf("the game: %d %q", download.Code, download.Body.String())
	}
	piece := console.do("GET", "/install/dbi/Games/"+url.PathEscape(name), nil, "Authorization", auth, "Range", "bytes=5-6")
	if piece.Code != http.StatusPartialContent || piece.Body.String() != "of" {
		t.Fatalf("a piece: %d %q", piece.Code, piece.Body.String())
	}

	var index netTinfoilIndex
	tinfoil := console.do("GET", "/install/tinfoil", nil, "Authorization", auth)
	if err := json.Unmarshal(tinfoil.Body.Bytes(), &index); err != nil || len(index.Files) != 3 || index.Success != "Hello" {
		t.Fatalf("the index: %v %s", err, tinfoil.Body.String())
	}
	for _, file := range index.Files {
		address, _ := url.Parse(file.Url)
		if !strings.Contains(address.Fragment, "[0100000000") || file.Size <= 0 {
			t.Fatalf("a file of the index: %+v", file)
		}
		got := console.do("GET", address.EscapedPath(), nil, "Authorization", auth)
		if got.Code != http.StatusOK || int64(got.Body.Len()) != file.Size {
			t.Fatalf("%s: %d %d", file.Url, got.Code, got.Body.Len())
		}
	}

}

// Every update is listed when asked, not only the newest.
func TestNetInstallEveryUpdate(t *testing.T) {
	web, admin, key := netWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.AllUpdates = true })
	console := &client{t: t, handler: admin.handler}
	updates := console.do("GET", "/install/dbi/Updates/", nil, "Authorization", basic("switch", key)).Body.String()
	if !strings.Contains(updates, "[v131072].nsz") || !strings.Contains(updates, "[v65536].nsp") {
		t.Fatalf("every update: %s", updates)
	}
}

// A console key lists and downloads, nothing else; the other users keep their roles, a guest
// does not download, and a wrong key is a failed sign-in.
func TestNetInstallKeysAndRoles(t *testing.T) {
	web, admin, key := netWeb(t)
	console := &client{t: t, handler: admin.handler}
	auth := basic("anything", key)
	if code := console.do("GET", "/index.html", nil, "Authorization", auth).Code; code != http.StatusForbidden {
		t.Fatalf("a console opens no page: %d", code)
	}
	if code := console.do("POST", "/sync", nil, "Authorization", auth).Code; code != http.StatusForbidden {
		t.Fatalf("a console changes nothing: %d", code)
	}
	if code := console.do("GET", "/install/dbi/", nil, "Authorization", bearer(key)).Code; code != http.StatusOK {
		t.Fatalf("the key as a bearer token too: %d", code)
	}
	if code := admin.do("GET", "/install/dbi/", nil).Code; code != http.StatusOK {
		t.Fatalf("an administrator sees what the console sees: %d", code)
	}
	if code := console.do("GET", "/install/dbi/", nil, "Authorization", basic("switch", "slm_"+strings.Repeat("A", apiKeyLength))).Code; code != http.StatusUnauthorized {
		t.Fatalf("a wrong key: %d", code)
	}

	// a guest does not download, nor the key of a user who became one
	web.auth.users.Add("dave", "delta-secret-44", ROLE_GUEST)
	guest := &client{t: t, handler: admin.handler}
	guest.do("POST", "/login.html", url.Values{"name": {"dave"}, "password": {"delta-secret-44"}})
	if code := guest.do("GET", "/install/dbi/", nil).Code; code != http.StatusForbidden {
		t.Fatalf("a guest: %d", code)
	}
	admin.do("POST", "/users/role", url.Values{"name": {"bob"}, "role": {ROLE_GUEST}})
	web.auth.forget()
	if code := console.do("GET", "/install/dbi/", nil, "Authorization", auth).Code; code != http.StatusForbidden {
		t.Fatalf("the console key of a guest: %d", code)
	}
}

// It is off by default, only on the local network unless allowed, and never without users.
func TestNetInstallGate(t *testing.T) {
	web, admin, key := netWeb(t)
	console := &client{t: t, handler: admin.handler}
	auth := basic("switch", key)

	console.ip = "203.0.113.5"
	if code := console.do("GET", "/install/tinfoil", nil, "Authorization", auth).Code; code != http.StatusForbidden {
		t.Fatalf("from outside: %d", code)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Remote = true })
	if code := console.do("GET", "/install/tinfoil", nil, "Authorization", auth).Code; code != http.StatusOK {
		t.Fatalf("from outside, allowed: %d", code)
	}
	console.ip = ""

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	if code := console.do("GET", "/install/tinfoil", nil, "Authorization", auth).Code; code != http.StatusNotFound {
		t.Fatalf("turned off: %d", code)
	}

	// without users everybody on the local network is an administrator: never without a password
	open := usersWeb(t)
	open.HandleNetInstall()
	settings.UpdateSettings(open.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = true })
	anyone := &client{t: t, handler: open.auth.middleware(open.router)}
	if code := anyone.do("GET", "/install/dbi/", nil).Code; code != http.StatusForbidden {
		t.Fatalf("without users: %d", code)
	}
}

// A path that climbs out of the listing finds nothing.
func TestNetInstallPathsStayInside(t *testing.T) {
	_, admin, key := netWeb(t)
	console := &client{t: t, handler: admin.handler}
	for _, path := range []string{"/install/dbi/Games/..%2f..%2fsettings.json", "/install/dbi/Nothing/", "/install/f/0000000000000000/x.nsp"} {
		if code := console.do("GET", path, nil, "Authorization", basic("switch", key)).Code; code == http.StatusOK {
			t.Fatalf("%s: %d", path, code)
		}
	}
}
