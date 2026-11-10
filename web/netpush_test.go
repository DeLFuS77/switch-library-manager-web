package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// sendJSON is client.do with a JSON body.
func sendJSON(c *client, method string, path string, body string, headers ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
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

// pushWeb is netWeb with bob signed in, the id of his console key and the ids of the files.
func pushWeb(t *testing.T) (web *Web, admin *client, bob *client, console *client, auth string, keyId string, files map[string]string) {
	t.Helper()
	web, admin, key := netWeb(t)
	bob = &client{t: t, handler: admin.handler}
	bob.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	for _, row := range web.auth.users.KeyRows("bob") {
		if row.Role == ROLE_CONSOLE {
			keyId = row.ID
		}
	}
	if keyId == "" {
		t.Fatal("the key of the console of bob")
	}
	files = map[string]string{}
	kept, _ := web.netFilesKept(settings.ReadSettings(web.dataFolder).NetInstall)
	for _, file := range kept {
		files[netCatalogKinds[file.kind]] = file.id
	}
	return web, admin, bob, &client{t: t, handler: admin.handler}, basic("switch", key), keyId, files
}

func pushBody(keyId string, ids ...string) string {
	data, _ := json.Marshal(map[string]any{"keyId": keyId, "fileIds": ids})
	return string(data)
}

type pushWaitAnswer struct {
	Items []netPushItem `json:"items"`
}

// A user sends files to their console, which gets them once, with the address the catalog gives;
// nothing waits then, and the wait ends with 204 at the end of its time.
func TestNetPushSentAndTakenOnce(t *testing.T) {
	web, _, bob, console, auth, keyId, files := pushWeb(t)
	sent := sendJSON(bob, "POST", "/api/install/push", pushBody(keyId, files["game"], files["update"], files["game"]))
	if sent.Code != http.StatusOK || !strings.Contains(sent.Body.String(), `"sent":2`) {
		t.Fatalf("sent: %d %s", sent.Code, sent.Body.String())
	}
	got := console.do("GET", "/install/push/wait?timeout=1", nil, "Authorization", auth)
	var answer pushWaitAnswer
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &answer) != nil || len(answer.Items) != 2 {
		t.Fatalf("taken: %d %s", got.Code, got.Body.String())
	}
	game := answer.Items[0]
	if game.FileId != files["game"] || game.Kind != "game" || game.TitleId != "0100000000010000" || game.Game != "Game" || game.From != "bob" ||
		game.Format != "nsp" || game.Size <= 0 || len(game.Id) != 32 || game.Url != netInstallFiles+game.FileId+"/"+url.PathEscape(game.Name) {
		t.Fatalf("the game sent: %+v", game)
	}
	if update := answer.Items[1]; update.Kind != "update" || update.Version != 131072 || update.Format != "nsz" {
		t.Fatalf("the update sent: %+v", update)
	}
	if download := console.do("GET", game.Url, nil, "Authorization", auth); download.Code != http.StatusOK {
		t.Fatalf("the address sent: %d", download.Code)
	}

	started := time.Now()
	again := console.do("GET", "/install/push/wait?timeout=1", nil, "Authorization", auth)
	if again.Code != http.StatusNoContent || time.Since(started) < 900*time.Millisecond {
		t.Fatalf("nothing waits: %d after %v", again.Code, time.Since(started))
	}
	noted := false
	for _, entry := range web.activities().List() {
		if entry.Action == activity.ACTION_NET_PUSH && entry.User == "bob" && entry.Detail == `"Switch": Game` {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("the send is noted: %+v", web.activities().List())
	}
}

// Only the consoles of the user: an administrator does not send to the console of another user,
// a file not in the catalog is refused, and so is a list without files.
func TestNetPushOnlyToOwnConsoles(t *testing.T) {
	_, admin, bob, _, _, keyId, files := pushWeb(t)
	if code := sendJSON(admin, "POST", "/api/install/push", pushBody(keyId, files["game"])).Code; code != http.StatusNotFound {
		t.Fatalf("the console of another user: %d", code)
	}
	if code := sendJSON(bob, "POST", "/api/install/push", pushBody(keyId, "0000000000000000")).Code; code != http.StatusNotFound {
		t.Fatalf("a file not in the catalog: %d", code)
	}
	if code := sendJSON(bob, "POST", "/api/install/push", pushBody(keyId)).Code; code != http.StatusBadRequest {
		t.Fatalf("no file: %d", code)
	}
	if code := (&client{t: t, handler: admin.handler}).do("POST", "/api/install/push", nil).Code; code == http.StatusOK {
		t.Fatalf("signed out: %d", code)
	}
}

// A console waiting gets what is sent at once, not at the end of its time.
func TestNetPushWaitReturnsOnPush(t *testing.T) {
	_, _, bob, console, auth, keyId, files := pushWeb(t)
	done := make(chan *httptest.ResponseRecorder, 1)
	started := time.Now()
	go func() {
		done <- console.do("GET", "/install/push/wait?timeout=10", nil, "Authorization", auth)
	}()
	time.Sleep(100 * time.Millisecond)
	if code := sendJSON(bob, "POST", "/api/install/push", pushBody(keyId, files["dlc"])).Code; code != http.StatusOK {
		t.Fatalf("sent: %d", code)
	}
	select {
	case got := <-done:
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"kind":"dlc"`) || time.Since(started) > 5*time.Second {
			t.Fatalf("the wait: %d %s", got.Code, got.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not end with the push")
	}
}

// A wait ends when the console goes away, and when the app stops.
func TestNetPushWaitEndsWithTheClient(t *testing.T) {
	web, admin, _, _, auth, _, _ := pushWeb(t)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/install/push/wait?timeout=30", nil).WithContext(ctx)
	request.RemoteAddr = "192.168.1.10:1234"
	request.Header.Set("Authorization", auth)
	done := make(chan struct{})
	go func() {
		admin.handler.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the wait goes on without its console")
	}

	stopped := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		stopped <- (&client{t: t, handler: admin.handler}).do("GET", "/install/push/wait?timeout=30", nil, "Authorization", auth)
	}()
	time.Sleep(100 * time.Millisecond)
	web.closingPages()
	web.closedOnce.Do(func() { close(web.closing) })
	select {
	case got := <-stopped:
		if got.Code != http.StatusNoContent {
			t.Fatalf("the app stops: %d", got.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the wait goes on while the app stops")
	}
}

// What waits too long is forgotten, and a console has 50 installs waiting at most.
func TestNetPushQueueExpiresAndIsCapped(t *testing.T) {
	queue := &netPushQueue{}
	old := &netPush{id: strings.Repeat("a", 32), keyId: "k", made: time.Now().Add(-netPushLife - time.Minute)}
	if err := queue.add([]*netPush{old}); err != nil {
		t.Fatal(err)
	}
	if taken, signal := queue.take("k"); len(taken) != 0 || signal == nil {
		t.Fatalf("an old push is forgotten: %d", len(taken))
	}
	many := []*netPush{}
	for i := 0; i < netPushMax+1; i++ {
		many = append(many, &netPush{id: strings.Repeat("b", 32), keyId: "k", made: time.Now()})
	}
	if err := queue.add(many); err != errNetPushFull {
		t.Fatalf("more than 50: %v", err)
	}
	if err := queue.add(many[:netPushMax]); err != nil || queue.waiting("k") != netPushMax {
		t.Fatalf("50: %v %d", err, queue.waiting("k"))
	}
	if err := queue.add(many[:1]); err != errNetPushFull {
		t.Fatalf("one more: %v", err)
	}
}

// The answer of the console is noted in the activity, once, and only from that console.
func TestNetPushAnswerNoted(t *testing.T) {
	web, _, bob, console, auth, keyId, files := pushWeb(t)
	sendJSON(bob, "POST", "/api/install/push", pushBody(keyId, files["game"], files["dlc"]))
	var answer pushWaitAnswer
	json.Unmarshal(console.do("GET", "/install/push/wait?timeout=1", nil, "Authorization", auth).Body.Bytes(), &answer)
	if len(answer.Items) != 2 {
		t.Fatalf("taken: %+v", answer)
	}
	body := func(id string, accepted bool) string {
		data, _ := json.Marshal(map[string]any{"id": id, "accepted": accepted})
		return string(data)
	}
	if code := sendJSON(bob, "POST", "/install/push/answer", body(answer.Items[0].Id, true)).Code; code != http.StatusForbidden {
		t.Fatalf("only the console answers: %d", code)
	}
	if got := sendJSON(console, "POST", "/install/push/answer", body(answer.Items[0].Id, true), "Authorization", auth); got.Code != http.StatusOK {
		t.Fatalf("accepted: %d %s", got.Code, got.Body.String())
	}
	if code := sendJSON(console, "POST", "/install/push/answer", body(answer.Items[0].Id, true), "Authorization", auth).Code; code != http.StatusNotFound {
		t.Fatalf("answered twice: %d", code)
	}
	sendJSON(console, "POST", "/install/push/answer", body(answer.Items[1].Id, false), "Authorization", auth)
	accepted, declined := 0, 0
	for _, entry := range web.activities().List() {
		switch {
		case entry.Action == activity.ACTION_NET_PUSH_YES && entry.User == "bob" && entry.Detail == `"Switch": `+answer.Items[0].Name:
			accepted++
		case entry.Action == activity.ACTION_NET_PUSH_NO && entry.User == "bob" && entry.Detail == `"Switch": `+answer.Items[1].Name:
			declined++
		}
	}
	if accepted != 1 || declined != 1 {
		t.Fatalf("noted: %+v", web.activities().List())
	}
}

// The console tells what is installed on it: kept in a file of its own, in place of what it told
// before, shown to its user with the consoles and on the page of the game, and forgotten when
// asked by the console or by the user.
func TestNetInventoryStoredReplacedAndForgotten(t *testing.T) {
	web, _, bob, console, auth, keyId, _ := pushWeb(t)
	items := `{"items":[{"titleId":"0100000000010000","type":"game","version":0,"storage":"sd"},` +
		`{"titleId":"0100000000010800","type":"update","version":65536,"storage":"sd"},` +
		`{"titleId":"0100000000011001","type":"dlc","version":0,"storage":"nand"},` +
		`{"titleId":"0100000000020000","type":"game","version":0,"storage":"nand"},` +
		`{"titleId":"nonsense","type":"game"}]}`
	if got := sendJSON(console, "POST", "/install/inventory", items, "Authorization", auth); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"stored":4`) {
		t.Fatalf("stored: %d %s", got.Code, got.Body.String())
	}
	if _, err := os.Stat(web.netInventoryPath(keyId)); err != nil {
		t.Fatalf("the file of the console: %v", err)
	}
	var consoles struct {
		Enabled  bool            `json:"enabled"`
		Consoles []netConsoleRow `json:"consoles"`
	}
	json.Unmarshal(bob.do("GET", "/api/install/consoles", nil).Body.Bytes(), &consoles)
	if !consoles.Enabled || len(consoles.Consoles) != 1 || consoles.Consoles[0].Inventory == nil || consoles.Consoles[0].Inventory.Titles != 2 {
		t.Fatalf("the consoles: %+v", consoles)
	}
	var game struct {
		Consoles []netConsoleRow `json:"consoles"`
		Files    []netGameFile   `json:"files"`
	}
	json.Unmarshal(bob.do("GET", "/api/install/game/0100000000010000", nil).Body.Bytes(), &game)
	if len(game.Consoles) != 1 || game.Consoles[0].Installed == nil || *game.Consoles[0].Installed != (netGameOnConsole{Version: 65536, Storage: "sd", Dlc: 1}) {
		t.Fatalf("on the console: %+v", game.Consoles)
	}
	checked := 0
	for _, file := range game.Files {
		if file.Checked {
			checked++
			if file.Kind == "dlc" || (file.Kind == "update" && file.Version != 131072) {
				t.Fatalf("ticked: %+v", file)
			}
		}
	}
	if len(game.Files) != 3 || checked != 2 {
		t.Fatalf("the files of the game: %+v", game.Files)
	}

	sendJSON(console, "POST", "/install/inventory", `{"items":[{"titleId":"0100000000030000","type":"game","version":0,"storage":"sd"}]}`, "Authorization", auth)
	game.Consoles = nil
	json.Unmarshal(bob.do("GET", "/api/install/game/0100000000010000", nil).Body.Bytes(), &game)
	if game.Consoles[0].Installed != nil {
		t.Fatalf("replaced: %+v", game.Consoles[0])
	}

	if got := sendJSON(console, "POST", "/install/inventory", `{"off":true}`, "Authorization", auth); got.Code != http.StatusOK {
		t.Fatalf("off: %d", got.Code)
	}
	if _, err := os.Stat(web.netInventoryPath(keyId)); !os.IsNotExist(err) {
		t.Fatalf("forgotten by the console: %v", err)
	}

	sendJSON(console, "POST", "/install/inventory", `{"items":[]}`, "Authorization", auth)
	if code := bob.do("DELETE", "/api/install/inventory/"+keyId, nil).Code; code != http.StatusOK {
		t.Fatalf("forgotten by the user: %d", code)
	}
	if _, err := os.Stat(web.netInventoryPath(keyId)); !os.IsNotExist(err) {
		t.Fatalf("forgotten by the user: %v", err)
	}

	many := make([]netInventoryItem, netInventoryMax+1)
	data, _ := json.Marshal(map[string]any{"items": many})
	if code := sendJSON(console, "POST", "/install/inventory", string(data), "Authorization", auth).Code; code != http.StatusRequestEntityTooLarge {
		t.Fatalf("more than 5000: %d", code)
	}
}

// Without the key of a console, or with the network install off, nothing is taken.
func TestNetPushNeedsAKeyAndTheNetworkInstall(t *testing.T) {
	web, admin, bob, console, auth, keyId, files := pushWeb(t)
	anyone := &client{t: t, handler: admin.handler}
	if code := anyone.do("GET", "/install/push/wait?timeout=1", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("wait without a key: %d", code)
	}
	if code := sendJSON(anyone, "POST", "/install/inventory", `{"items":[]}`).Code; code != http.StatusUnauthorized {
		t.Fatalf("inventory without a key: %d", code)
	}
	if code := sendJSON(anyone, "POST", "/install/push/answer", `{"id":"`+strings.Repeat("a", 32)+`","accepted":true}`).Code; code != http.StatusUnauthorized {
		t.Fatalf("answer without a key: %d", code)
	}
	if code := bob.do("GET", "/install/push/wait?timeout=1", nil).Code; code != http.StatusForbidden {
		t.Fatalf("a user is not a console: %d", code)
	}
	if code := console.do("POST", "/install/catalog", nil, "Authorization", auth).Code; code == http.StatusOK {
		t.Fatalf("a console posts only its answers and what it has: %d", code)
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	if code := console.do("GET", "/install/push/wait?timeout=1", nil, "Authorization", auth).Code; code != http.StatusNotFound {
		t.Fatalf("wait, turned off: %d", code)
	}
	if code := sendJSON(console, "POST", "/install/inventory", `{"items":[]}`, "Authorization", auth).Code; code != http.StatusNotFound {
		t.Fatalf("inventory, turned off: %d", code)
	}
	if code := sendJSON(bob, "POST", "/api/install/push", pushBody(keyId, files["game"])).Code; code != http.StatusConflict {
		t.Fatalf("send, turned off: %d", code)
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = true })
	console.ip = "203.0.113.5"
	if code := console.do("GET", "/install/push/wait?timeout=1", nil, "Authorization", auth).Code; code != http.StatusForbidden {
		t.Fatalf("from outside: %d", code)
	}
}
