package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// noted tells whether the activity has an action of a user with a detail.
func noted(web *Web, action string, user string, detail string) bool {
	for _, entry := range web.activities().List() {
		if entry.Action == action && entry.User == user && entry.Detail == detail {
			return true
		}
	}
	return false
}

// removeBody is the body that asks to remove a game.
func removeBody(titleId string) string {
	return `{"titleId":"` + titleId + `"}`
}

// The user asks a console of theirs to remove a game installed on it: the console gets it once,
// with what is sent to it, and the page of the console tells it is asked until it answers. Once it
// removed the game, the game leaves what is installed on it, and the time played stays.
func TestNetConsoleRemoveAskedAndRemoved(t *testing.T) {
	web, admin, bob, console, auth, keyId, _ := pushWeb(t)
	sendJSON(console, "POST", "/install/inventory", consoleInventory, "Authorization", auth)
	path := "/api/install/consoles/" + keyId + "/remove"
	if code := sendJSON(admin, "POST", path, removeBody("0100000000010000")).Code; code != http.StatusNotFound {
		t.Fatalf("the console of another user: %d", code)
	}
	for body, want := range map[string]int{
		removeBody("0100000000010800"): http.StatusBadRequest, // an update, not a game
		removeBody("0100000000011001"): http.StatusBadRequest, // a DLC
		removeBody("nonsense"):         http.StatusBadRequest,
		"nonsense":                     http.StatusBadRequest,
		removeBody("0100000000050000"): http.StatusConflict, // not installed
	} {
		if code := sendJSON(bob, "POST", path, body).Code; code != want {
			t.Errorf("%s: %d, want %d", body, code, want)
		}
	}

	got := sendJSON(bob, "POST", path, removeBody("0100000000010000"))
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"state":"asked"`) || !strings.Contains(got.Body.String(), `"game":"Game"`) {
		t.Fatalf("asked: %d %s", got.Code, got.Body.String())
	}
	// asked again: once is enough
	if code := sendJSON(bob, "POST", path, removeBody("0100000000010000")).Code; code != http.StatusOK || web.netPushes.waiting(keyId) != 1 {
		t.Fatalf("asked twice: %d, %d waiting", code, web.netPushes.waiting(keyId))
	}
	if !noted(web, activity.ACTION_NET_REMOVE, "bob", `"Switch": Game`) {
		t.Fatalf("noted: %+v", web.activities().List())
	}
	var detail netConsoleDetail
	json.Unmarshal(bob.do("GET", "/api/install/consoles/"+keyId, nil).Body.Bytes(), &detail)
	if len(detail.Games) != 2 || !detail.Games[0].Removing || detail.Games[1].Removing {
		t.Fatalf("asked, on the page of the console: %+v", detail.Games)
	}

	var answer pushWaitAnswer
	json.Unmarshal(console.do("GET", "/install/push/wait?timeout=1", nil, "Authorization", auth).Body.Bytes(), &answer)
	if len(answer.Items) != 1 {
		t.Fatalf("the console gets it: %+v", answer.Items)
	}
	item := answer.Items[0]
	if item.Action != "remove" || item.TitleId != "0100000000010000" || item.Game != "Game" || item.From != "bob" || len(item.Id) != 32 || item.Url != "" || item.FileId != "" {
		t.Fatalf("what the console gets: %+v", item)
	}
	// given and not answered yet: still asked
	json.Unmarshal(bob.do("GET", "/api/install/consoles/"+keyId, nil).Body.Bytes(), &detail)
	if !detail.Games[0].Removing {
		t.Fatal("asked until the console answers")
	}

	answered := sendJSON(console, "POST", "/install/push/answer", `{"id":"`+item.Id+`","accepted":true,"result":"removed"}`, "Authorization", auth)
	if answered.Code != http.StatusOK || !strings.Contains(answered.Body.String(), "noted") {
		t.Fatalf("answered: %d %s", answered.Code, answered.Body.String())
	}
	if !noted(web, activity.ACTION_NET_REMOVE_YES, "bob", `"Switch": Game`) {
		t.Fatalf("the answer noted: %+v", web.activities().List())
	}
	inventory, _ := web.readNetInventory(keyId)
	if len(inventory.Items) != 1 || inventory.Items[0].TitleId != "0100000000020000" || len(inventory.Play) != 2 || inventory.System == nil {
		t.Fatalf("the game leaves what is installed: %+v", inventory)
	}
	detail = netConsoleDetail{}
	json.Unmarshal(bob.do("GET", "/api/install/consoles/"+keyId, nil).Body.Bytes(), &detail)
	if len(detail.Games) != 1 || detail.Games[0].Removing {
		t.Fatalf("removed, on the page of the console: %+v", detail.Games)
	}
	if code := sendJSON(bob, "POST", path, removeBody("0100000000010000")).Code; code != http.StatusConflict {
		t.Fatalf("no longer installed: %d", code)
	}
	if code := sendJSON(console, "POST", "/install/push/answer", `{"id":"`+item.Id+`","accepted":true,"result":"removed"}`, "Authorization", auth).Code; code != http.StatusNotFound {
		t.Fatalf("answered twice: %d", code)
	}
}

// What the console did is noted: declined, could not, or did not find the game, which then leaves
// what is installed on it; the activity tells the last one in the language of the page.
func TestNetConsoleRemoveAnswers(t *testing.T) {
	web, _, bob, console, auth, keyId, _ := pushWeb(t)
	sendJSON(console, "POST", "/install/inventory", consoleInventory, "Authorization", auth)
	path := "/api/install/consoles/" + keyId + "/remove"
	other := `"Switch": 0100000000020000`
	ask := func(answer string) {
		t.Helper()
		if code := sendJSON(bob, "POST", path, removeBody("0100000000020000")).Code; code != http.StatusOK {
			t.Fatalf("asked: %d", code)
		}
		var waiting pushWaitAnswer
		json.Unmarshal(console.do("GET", "/install/push/wait?timeout=1", nil, "Authorization", auth).Body.Bytes(), &waiting)
		if len(waiting.Items) != 1 {
			t.Fatalf("the console gets it: %+v", waiting.Items)
		}
		if code := sendJSON(console, "POST", "/install/push/answer", `{"id":"`+waiting.Items[0].Id+`",`+answer+`}`, "Authorization", auth).Code; code != http.StatusOK {
			t.Fatalf("answered: %d", code)
		}
	}
	installed := func() int {
		inventory, _ := web.readNetInventory(keyId)
		return len(inventory.Items)
	}

	ask(`"accepted":false`)
	if !noted(web, activity.ACTION_NET_REMOVE_NO, "bob", other) || installed() != 3 {
		t.Fatalf("declined: %+v", web.activities().List())
	}
	ask(`"accepted":true,"result":"failed"`)
	if !noted(web, activity.ACTION_NET_REMOVE_FAIL, "bob", other) || installed() != 3 {
		t.Fatalf("could not: %+v", web.activities().List())
	}
	ask(`"accepted":true,"result":"missing"`)
	if !noted(web, activity.ACTION_NET_REMOVE_FAIL, "bob", other+netRemoveMissingPhrase) || installed() != 2 {
		t.Fatalf("did not find it: %+v", web.activities().List())
	}
	shown := translateActivity("es", web.activities().List()[:1])
	if !strings.HasSuffix(shown[0].Detail, ", no estaba en la consola") {
		t.Fatalf("in Spanish: %q", shown[0].Detail)
	}
}

// Removing asks as sending does: with the network install on, a console that told what it has,
// and room in what waits for it; it waits with the files sent.
func TestNetConsoleRemoveLimits(t *testing.T) {
	web, _, bob, console, auth, keyId, files := pushWeb(t)
	path := "/api/install/consoles/" + keyId + "/remove"
	if code := sendJSON(bob, "POST", path, removeBody("0100000000010000")).Code; code != http.StatusConflict {
		t.Fatalf("nothing told: %d", code)
	}
	sendJSON(console, "POST", "/install/inventory", consoleInventory, "Authorization", auth)

	// a file sent and a game to remove come together
	sendJSON(bob, "POST", "/api/install/push", pushBody(keyId, files["dlc"]))
	sendJSON(bob, "POST", path, removeBody("0100000000020000"))
	var answer pushWaitAnswer
	json.Unmarshal(console.do("GET", "/install/push/wait?timeout=1", nil, "Authorization", auth).Body.Bytes(), &answer)
	if len(answer.Items) != 2 || answer.Items[0].FileId != files["dlc"] || answer.Items[0].Action != "" || answer.Items[1].Action != "remove" {
		t.Fatalf("both: %+v", answer.Items)
	}

	waiting := []*netPush{}
	for i := 0; i < netPushMax; i++ {
		waiting = append(waiting, &netPush{id: fmt.Sprintf("%032x", i), keyId: keyId, fileId: files["game"], made: time.Now()})
	}
	web.netPushes.add(waiting)
	if code := sendJSON(bob, "POST", path, removeBody("0100000000010000")).Code; code != http.StatusTooManyRequests {
		t.Fatalf("full: %d", code)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	if code := sendJSON(bob, "POST", path, removeBody("0100000000010000")).Code; code != http.StatusConflict {
		t.Fatalf("turned off: %d", code)
	}
}
