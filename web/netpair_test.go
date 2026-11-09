package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// askPair is a console asking to be paired, from an address, with the name it gives.
func askPair(t *testing.T, handler http.Handler, ip string, console string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest("POST", "/install/pair", strings.NewReader(fmt.Sprintf(`{"console":%q}`, console)))
	request.RemoteAddr = ip + ":5000"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	answer := map[string]any{}
	json.Unmarshal(recorder.Body.Bytes(), &answer)
	return recorder.Code, answer
}

func waitPair(c *client, id string) (int, map[string]any) {
	got := c.do("GET", "/install/pair/"+id, nil)
	answer := map[string]any{}
	json.Unmarshal(got.Body.Bytes(), &answer)
	return got.Code, answer
}

func pairsOf(t *testing.T, c *client) []netPairRow {
	t.Helper()
	rows := []netPairRow{}
	got := c.do("GET", "/api/install/pairs", nil)
	if err := json.Unmarshal(got.Body.Bytes(), &rows); err != nil {
		t.Fatalf("the consoles waiting: %d %s", got.Code, got.Body.String())
	}
	return rows
}

func signedInBob(t *testing.T, handler http.Handler) *client {
	bob := &client{t: t, handler: handler}
	bob.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	return bob
}

// A console finds the app without a key: who it is, the same every time, and nothing more.
func TestNetPairHello(t *testing.T) {
	web, admin, _ := netWeb(t)
	console := &client{t: t, handler: admin.handler}
	got := console.do("GET", "/install/hello", nil)
	var hello netHelloAnswer
	if err := json.Unmarshal(got.Body.Bytes(), &hello); err != nil || got.Code != http.StatusOK {
		t.Fatalf("hello: %d %s", got.Code, got.Body.String())
	}
	if hello.App != netAppName || len(hello.Id) != 32 || !hello.Enabled || hello.Version != settings.SLM_WEB_VERSION {
		t.Fatalf("hello: %+v", hello)
	}
	if again := console.do("GET", "/install/hello", nil).Body.String(); !strings.Contains(again, hello.Id) {
		t.Fatalf("the id changed: %s", again)
	}
	if kept, _ := os.ReadFile(filepath.Join(web.dataFolder, "instance-id")); strings.TrimSpace(string(kept)) != hello.Id {
		t.Fatalf("the id is not kept: %q", kept)
	}
	outside := &client{t: t, handler: admin.handler, ip: "203.0.113.9"}
	if got := outside.do("GET", "/install/hello", nil); got.Code != http.StatusForbidden {
		t.Fatalf("from outside the local network: %d", got.Code)
	}
	// only hello and the pairing are open: the rest still needs a key
	if got := console.do("GET", "/install/tinfoil", nil); got.Code == http.StatusOK {
		t.Fatalf("the index without a key: %d", got.Code)
	}
}

// A console asks, a player accepts it, and the console takes its key once; the key installs.
func TestNetPairAccepted(t *testing.T) {
	_, admin, _ := netWeb(t)
	code, asked := askPair(t, admin.handler, "192.168.1.50", "Switch del salón")
	id, _ := asked["id"].(string)
	if code != http.StatusCreated || !regexp.MustCompile(`^[A-Z2-9]{4}-[A-Z2-9]{4}$`).MatchString(fmt.Sprint(asked["code"])) || len(id) != 32 {
		t.Fatalf("asking: %d %v", code, asked)
	}
	console := &client{t: t, handler: admin.handler, ip: "192.168.1.50"}
	if code, answer := waitPair(console, id); code != http.StatusAccepted || answer["state"] != "waiting" {
		t.Fatalf("waiting: %d %v", code, answer)
	}

	if anonymous := (&client{t: t, handler: admin.handler}).do("GET", "/api/install/pairs", nil); anonymous.Code == http.StatusOK && strings.Contains(anonymous.Body.String(), id) {
		t.Fatal("the consoles waiting shown to someone not signed in")
	}
	bob := signedInBob(t, admin.handler)
	rows := pairsOf(t, bob)
	if len(rows) != 1 || rows[0].Id != id || rows[0].Code != asked["code"] || rows[0].Console != "Switch del salón" || rows[0].CanEnable {
		t.Fatalf("the consoles waiting: %+v", rows)
	}

	// another address cannot take the key of this console
	other := &client{t: t, handler: admin.handler, ip: "192.168.1.51"}
	accepted := bob.do("POST", "/api/install/pairs/"+id, url.Values{"action": {"accept"}})
	if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), "Switch del salón") {
		t.Fatalf("accepting: %d %s", accepted.Code, accepted.Body.String())
	}
	if code, _ := waitPair(other, id); code != http.StatusGone {
		t.Fatalf("taken from another address: %d", code)
	}
	code, answer := waitPair(console, id)
	key, _ := answer["key"].(string)
	if code != http.StatusOK || answer["state"] != "accepted" || !strings.HasPrefix(key, apiKeyPrefix) || answer["user"] != "bob" || answer["enabled"] != true || answer["name"] != "Switch del salón" {
		t.Fatalf("the key: %d %v", code, answer)
	}
	if code, _ := waitPair(console, id); code != http.StatusGone {
		t.Fatalf("the key given twice: %d", code)
	}
	if len(pairsOf(t, bob)) != 0 {
		t.Fatal("still shown as waiting")
	}
	speed := console.do("GET", "/install/speedtest?size=100", nil, "Authorization", basic("nxdock", key))
	if speed.Code != http.StatusOK || speed.Body.Len() != 100 {
		t.Fatalf("the key does not install: %d", speed.Code)
	}
}

// A console refused is told so, and gets no key.
func TestNetPairRefused(t *testing.T) {
	_, admin, _ := netWeb(t)
	_, asked := askPair(t, admin.handler, "192.168.1.60", "")
	id := asked["id"].(string)
	bob := signedInBob(t, admin.handler)
	if rows := pairsOf(t, bob); len(rows) != 1 || rows[0].Console != "NXDock" {
		t.Fatalf("a console without a name: %+v", rows)
	}
	if got := bob.do("POST", "/api/install/pairs/"+id, url.Values{"action": {"reject"}}); got.Code != http.StatusOK {
		t.Fatalf("refusing: %d %s", got.Code, got.Body.String())
	}
	console := &client{t: t, handler: admin.handler, ip: "192.168.1.60"}
	if code, answer := waitPair(console, id); code != http.StatusForbidden || answer["state"] != "rejected" || answer["key"] != nil {
		t.Fatalf("refused: %d %v", code, answer)
	}
	if got := bob.do("POST", "/api/install/pairs/"+id, url.Values{"action": {"accept"}}); got.Code != http.StatusGone {
		t.Fatalf("accepted after being refused: %d", got.Code)
	}
}

// With the network install off, an administrator accepts and turns it on at once; a player
// only sees that an administrator has to.
func TestNetPairTurnsOn(t *testing.T) {
	web, admin, _ := netWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	_, asked := askPair(t, admin.handler, "192.168.1.70", "Switch")
	id := asked["id"].(string)
	if rows := pairsOf(t, signedInBob(t, admin.handler)); len(rows) != 1 || rows[0].Enabled || rows[0].CanEnable {
		t.Fatalf("for a player: %+v", rows)
	}
	if rows := pairsOf(t, admin); len(rows) != 1 || rows[0].Enabled || !rows[0].CanEnable {
		t.Fatalf("for an administrator: %+v", rows)
	}
	if got := admin.do("POST", "/api/install/pairs/"+id, url.Values{"action": {"accept"}, "enable": {"true"}}); got.Code != http.StatusOK {
		t.Fatalf("accepting: %d %s", got.Code, got.Body.String())
	}
	if !settings.ReadSettings(web.dataFolder).NetInstall.Enabled {
		t.Fatal("the network install was not turned on")
	}
}

// One request waits per address, and only a few at once.
func TestNetPairLimits(t *testing.T) {
	_, admin, _ := netWeb(t)
	askPair(t, admin.handler, "192.168.1.80", "first")
	askPair(t, admin.handler, "192.168.1.80", "second")
	bob := signedInBob(t, admin.handler)
	if rows := pairsOf(t, bob); len(rows) != 1 || rows[0].Console != "second" {
		t.Fatalf("one per address: %+v", rows)
	}
	for i := 0; i < netPairMax-1; i++ {
		if code, _ := askPair(t, admin.handler, fmt.Sprintf("192.168.1.%d", 100+i), "c"); code != http.StatusCreated {
			t.Fatalf("console %d: %d", i, code)
		}
	}
	if code, _ := askPair(t, admin.handler, "192.168.1.200", "one too many"); code != http.StatusTooManyRequests {
		t.Fatalf("too many: %d", code)
	}
	if code, _ := askPair(t, admin.handler, "203.0.113.5", "outside"); code != http.StatusForbidden {
		t.Fatalf("from outside: %d", code)
	}
}

func TestNetPairPathsArePlain(t *testing.T) {
	for _, path := range []string{"/install/pair/../hello", "/install/pair/ABC", "/install/hello/x", "/install/pairx"} {
		request := httptest.NewRequest("GET", "http://app"+path, nil)
		if isConsolePublicRequest(request) {
			t.Errorf("%s is taken for a console's request", path)
		}
	}
}
