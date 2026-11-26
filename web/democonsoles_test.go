package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
)

// The demo shows its made-up consoles: what is installed on them, the files of a game that can
// be sent, a send going on and one that ended, and the wishlist; its installers answer nothing.
func TestDemoConsoles(t *testing.T) {
	t.Setenv("SLM_DEMO", "true")
	web := newTestWeb(t)
	web.loadDemo()
	web.router.HandleFunc(netInstallConsoles, web.handleNetConsoles)
	web.router.HandleFunc(netInstallGame+"{titleId:[0-9A-Fa-f]{16}}", web.handleNetGame)
	get := func(path string) map[string]any {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		answer := map[string]any{}
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &answer) != nil {
			t.Fatalf("%s: %d %s", path, recorder.Code, recorder.Body.String())
		}
		return answer
	}

	if principal, ok := netPairer(httptest.NewRequest(http.MethodGet, "/", nil)); !ok || principal.Name != DEMO_PLAYER {
		t.Fatalf("the user of the demo pairs the consoles: %v %v", principal, ok)
	}
	consoles := get(netInstallConsoles)
	list, _ := consoles["consoles"].([]any)
	if consoles["enabled"] != true || len(list) != len(demoConsoles) {
		t.Fatalf("the consoles of the demo: %v", consoles)
	}
	first, _ := list[0].(map[string]any)
	inventory, _ := first["inventory"].(map[string]any)
	if first["system"] == nil || inventory == nil || inventory["outdated"] != float64(1) {
		t.Fatalf("the first console has its system and one game out of date: %v", first)
	}
	game := get(netInstallGame + demoTitleId(2))
	if files, _ := game["files"].([]any); len(files) == 0 {
		t.Fatalf("a game of the demo can be sent: %v", game)
	}

	recorder := httptest.NewRecorder()
	if _, ok := web.netInstallGate(recorder, httptest.NewRequest(http.MethodGet, netInstallDbi, nil)); ok || recorder.Code != http.StatusNotFound {
		t.Fatalf("the installers answer nothing in the demo: %d", recorder.Code)
	}

	web.demoNetSend(time.Now())
	live, _ := web.netSends.views(time.Now())
	if len(live) != 1 || live[0].Current() == nil || live[0].Current().Speed <= 0 || live[0].Left <= 0 {
		t.Fatalf("a send goes on: %+v", live)
	}
	ended := 0
	for _, task := range web.taskLog().Snapshot() {
		if task.Kind == tasklog.TASK_NET_INSTALL && task.Status == tasklog.TASK_SUCCESS && len(task.Sent) > 0 {
			ended++
		}
	}
	web.writeDemoSendTask(time.Now())
	if ended != 1 || len(web.taskLog().Snapshot()) != 1 {
		t.Fatalf("one send that ended is kept, once: %d", ended)
	}
	if web.wishes().Count() != len(demoWished) {
		t.Fatalf("%d games wished", web.wishes().Count())
	}
}

// Without the demo, a request without a user pairs no console.
func TestNoDemoPairsNothingWithoutUser(t *testing.T) {
	t.Setenv("SLM_DEMO", "")
	if _, ok := netPairer(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Fatal("no user, no consoles")
	}
	if keys := demoConsoleKeys("someone"); len(keys) != 0 || !strings.EqualFold(demoConsoleKeys(DEMO_PLAYER)[0].Name, demoConsoles[0].key.Name) {
		t.Fatal("the consoles of the demo are its user's")
	}
}
