package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
)

// consoleInventory is what NXDock tells of a console with the game of netWeb and its first update
// installed, without its DLC, and a game the library does not have.
const consoleInventory = `{"items":[{"titleId":"0100000000010000","type":"game","version":0,"storage":"sd"},` +
	`{"titleId":"0100000000010800","type":"update","version":65536,"storage":"sd"},` +
	`{"titleId":"0100000000020000","type":"game","version":0,"storage":"nand"}],` +
	`"console":{"firmware":" 19.0.1 ","atmosphere":"1.8.0","emummc":true,"sdFree":1000000,"sdTotal":2000000,"nandFree":789,"nandTotal":1011,"sigpatches":"sys-patch"},` +
	`"play":[{"titleId":"0100000000010000","minutes":2040,"launches":57,"lastPlayed":1763400000},` +
	`{"titleId":"0100000000020000","minutes":30,"launches":2,"lastPlayed":1763300000}]}`

// keptSizes are the sizes of the files of the library that may be sent, by their kind.
func keptSizes(web *Web) map[string]int64 {
	sizes := map[string]int64{}
	kept, _ := web.netFilesKept(settings.ReadSettings(web.dataFolder).NetInstall)
	for _, file := range kept {
		sizes[netCatalogKinds[file.kind]] = file.size
	}
	return sizes
}

// An older NXDock tells only the titles: kept as before, without a system or time played.
func TestNetInventoryOldStillAccepted(t *testing.T) {
	web, _, bob, console, auth, keyId, _ := pushWeb(t)
	got := sendJSON(console, "POST", "/install/inventory", `{"items":[{"titleId":"0100000000010000","type":"game","version":0,"storage":"sd"}]}`, "Authorization", auth)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"stored":1`) {
		t.Fatalf("stored: %d %s", got.Code, got.Body.String())
	}
	inventory, ok := web.readNetInventory(keyId)
	if !ok || len(inventory.Items) != 1 || inventory.System != nil || inventory.Play != nil {
		t.Fatalf("the inventory: %+v", inventory)
	}
	if _, err := os.Stat(web.netPlayHistoryPath(keyId)); !os.IsNotExist(err) {
		t.Fatalf("no time played to keep: %v", err)
	}
	var consoles struct {
		Consoles []netConsoleRow `json:"consoles"`
	}
	json.Unmarshal(bob.do("GET", "/api/install/consoles", nil).Body.Bytes(), &consoles)
	if len(consoles.Consoles) != 1 || consoles.Consoles[0].System != nil || consoles.Consoles[0].Inventory == nil || consoles.Consoles[0].Inventory.Titles != 1 {
		t.Fatalf("the consoles: %+v", consoles)
	}
}

// The system and the time played are kept, cleaned: texts trimmed, title IDs in capitals, the
// titles played told wrong or twice left out.
func TestNetInventoryExtendedStoredAndCleaned(t *testing.T) {
	web, _, _, console, auth, keyId, _ := pushWeb(t)
	body := `{"items":[{"titleId":"0100000000010000","type":"game","version":0,"storage":"sd"}],` +
		`"console":{"firmware":" 19.0.1 ","atmosphere":"1.8.0","emummc":true,"sdFree":123,"sdTotal":456,"nandFree":789,"nandTotal":1011,"sigpatches":"sys-patch"},` +
		`"play":[{"titleId":"0100000000010000","minutes":2040,"launches":57,"lastPlayed":1763400000},` +
		`{"titleId":"0100000000010000","minutes":1,"launches":1,"lastPlayed":1},` +
		`{"titleId":"0100000000aa0000","minutes":5,"launches":1,"lastPlayed":1763000000},` +
		`{"titleId":"nonsense","minutes":5},{"titleId":"0100000000030000","minutes":-5},` +
		`{"titleId":"0100000000040000","minutes":0,"launches":0,"lastPlayed":0}]}`
	got := sendJSON(console, "POST", "/install/inventory", body, "Authorization", auth)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"played":2`) {
		t.Fatalf("stored: %d %s", got.Code, got.Body.String())
	}
	inventory, ok := web.readNetInventory(keyId)
	if !ok || inventory.System == nil {
		t.Fatalf("the inventory: %+v", inventory)
	}
	want := netConsoleFacts{Firmware: "19.0.1", Atmosphere: "1.8.0", Emummc: true, SdFree: 123, SdTotal: 456, NandFree: 789, NandTotal: 1011, Sigpatches: "sys-patch"}
	if *inventory.System != want {
		t.Fatalf("the system: %+v", *inventory.System)
	}
	if len(inventory.Play) != 2 || inventory.Play[0] != (netPlayEntry{TitleId: "0100000000010000", Minutes: 2040, Launches: 57, LastPlayed: 1763400000}) ||
		inventory.Play[1].TitleId != "0100000000AA0000" {
		t.Fatalf("the time played: %+v", inventory.Play)
	}
	history, ok := web.readNetPlayHistory(keyId)
	today := time.Now().UTC().Format(time.DateOnly)
	if !ok || len(history.Days) != 1 || history.Days[today]["0100000000010000"] != 2040 || len(history.Days[today]) != 2 {
		t.Fatalf("the day played: %+v", history)
	}

	// the console says no more: the time played goes with the rest
	sendJSON(console, "POST", "/install/inventory", `{"off":true}`, "Authorization", auth)
	if _, err := os.Stat(web.netPlayHistoryPath(keyId)); !os.IsNotExist(err) {
		t.Fatalf("the days played forgotten: %v", err)
	}
}

// A fact of the system told wrong is left out, with its free space or total; the other facts, the
// titles installed and the time played are kept. A title played told wrong goes alone. Too many
// titles played refuse the whole inventory, which stays as it was.
func TestNetInventoryBadFactsLeftOut(t *testing.T) {
	web, _, _, console, auth, keyId, _ := pushWeb(t)
	items := `"items":[{"titleId":"0100000000010000","type":"game","version":0,"storage":"sd"}]`
	for name, c := range map[string]struct {
		system string
		want   netConsoleFacts
	}{
		"a number below 0":     {`{"firmware":"18.0.0","sdFree":-1,"sdTotal":456,"nandFree":1,"nandTotal":2}`, netConsoleFacts{Firmware: "18.0.0", NandFree: 1, NandTotal: 2}},
		"more free than total": {`{"firmware":"18.0.0","nandFree":2000,"nandTotal":1000,"sdFree":5,"sdTotal":9}`, netConsoleFacts{Firmware: "18.0.0", SdFree: 5, SdTotal: 9}},
		"a long text":          {`{"firmware":"` + strings.Repeat("1", 33) + `","atmosphere":"1.8.0"}`, netConsoleFacts{Atmosphere: "1.8.0"}},
		"markup":               {`{"firmware":"18.0.0","atmosphere":"<b>1.8.0</b>"}`, netConsoleFacts{Firmware: "18.0.0"}},
		"a line break":         {`{"firmware":"18.0.0","sigpatches":"sys-patch\nmore","emummc":true}`, netConsoleFacts{Firmware: "18.0.0", Emummc: true}},
		"not a number":         {`{"firmware":"18.0.0","sdFree":"lots","sdTotal":9}`, netConsoleFacts{Firmware: "18.0.0"}},
		"a fraction":           {`{"firmware":"18.0.0","sdFree":1.5,"sdTotal":9}`, netConsoleFacts{Firmware: "18.0.0"}},
		"not a yes or no":      {`{"firmware":"18.0.0","emummc":"yes"}`, netConsoleFacts{Firmware: "18.0.0"}},
	} {
		body := `{` + items + `,"console":` + c.system + `,"play":[{"titleId":"0100000000010000","minutes":"lots"},{"titleId":"0100000000020000","minutes":7}]}`
		got := sendJSON(console, "POST", "/install/inventory", body, "Authorization", auth)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"stored":1`) || !strings.Contains(got.Body.String(), `"played":1`) {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
			continue
		}
		inventory, ok := web.readNetInventory(keyId)
		if !ok || len(inventory.Items) != 1 || inventory.System == nil || *inventory.System != c.want || len(inventory.Play) != 1 || inventory.Play[0].Minutes != 7 {
			t.Errorf("%s: the inventory: %+v %+v", name, inventory, inventory.System)
		}
	}
	// a system that is not an object, or tells nothing right: none, and the titles all the same
	for _, system := range []string{`"nonsense"`, `[1,2]`, `{"firmware":"<b>"}`} {
		if code := sendJSON(console, "POST", "/install/inventory", `{`+items+`,"console":`+system+`}`, "Authorization", auth).Code; code != http.StatusOK {
			t.Errorf("%s: %d", system, code)
		}
		if inventory, ok := web.readNetInventory(keyId); !ok || len(inventory.Items) != 1 || inventory.System != nil {
			t.Errorf("%s: the inventory: %+v", system, inventory)
		}
	}

	if code := sendJSON(console, "POST", "/install/inventory", consoleInventory, "Authorization", auth).Code; code != http.StatusOK {
		t.Fatalf("stored: %d", code)
	}
	many := make([]netPlayEntry, netPlayMax+1)
	for i := range many {
		many[i] = netPlayEntry{TitleId: fmt.Sprintf("01000000%04X0000", i), Minutes: 1}
	}
	data, _ := json.Marshal(map[string]any{"items": []any{}, "play": many})
	if code := sendJSON(console, "POST", "/install/inventory", string(data), "Authorization", auth).Code; code != http.StatusRequestEntityTooLarge {
		t.Fatalf("more than 5000 titles played: %d", code)
	}
	if inventory, ok := web.readNetInventory(keyId); !ok || len(inventory.Items) != 3 || inventory.System == nil || inventory.System.Firmware != "19.0.1" {
		t.Fatalf("the inventory as it was: %+v", inventory)
	}
}

// The time played is kept once a day: the last told of each day, for 400 days.
func TestNetPlayHistoryOneSnapshotPerDay(t *testing.T) {
	web, _, _, console, auth, keyId, _ := pushWeb(t)
	for _, minutes := range []int{10, 25} {
		body := fmt.Sprintf(`{"items":[],"play":[{"titleId":"0100000000010000","minutes":%d,"launches":1,"lastPlayed":1763400000}]}`, minutes)
		if code := sendJSON(console, "POST", "/install/inventory", body, "Authorization", auth).Code; code != http.StatusOK {
			t.Fatalf("stored: %d", code)
		}
	}
	history, _ := web.readNetPlayHistory(keyId)
	if len(history.Days) != 1 || history.Days[time.Now().UTC().Format(time.DateOnly)]["0100000000010000"] != 25 {
		t.Fatalf("one day, the last told: %+v", history)
	}

	start := time.Date(2026, 1, 1, 22, 0, 0, 0, time.UTC)
	os.Remove(web.netPlayHistoryPath(keyId))
	play := func(minutes int64) []netPlayEntry {
		return []netPlayEntry{{TitleId: "0100000000010000", Minutes: minutes}, {TitleId: "0100000000020000", Launches: 1}}
	}
	web.keepNetPlayDay(keyId, play(100), start)
	web.keepNetPlayDay(keyId, play(160), start.Add(90*time.Minute))
	web.keepNetPlayDay(keyId, play(200), start.Add(30*time.Hour))
	history, _ = web.readNetPlayHistory(keyId)
	if len(history.Days) != 2 || history.Days["2026-01-01"]["0100000000010000"] != 160 || history.Days["2026-01-03"]["0100000000010000"] != 200 ||
		len(history.Days["2026-01-03"]) != 1 {
		t.Fatalf("two days: %+v", history.Days)
	}
	web.keepNetPlayDay(keyId, play(900), start.AddDate(0, 0, netPlayDays+2))
	history, _ = web.readNetPlayHistory(keyId)
	if len(history.Days) != 1 {
		t.Fatalf("the days older than 400 go: %+v", history.Days)
	}
	data, _ := os.ReadFile(web.netPlayHistoryPath(keyId))
	if strings.Contains(string(data), "\n") {
		t.Fatalf("kept compact: %s", data)
	}
}

// The consoles tell what the library has newer for them; the page of a console tells it game by
// game, with the files that bring it up to date and whether they fit on its SD card.
func TestNetConsoleDetailAndOutdated(t *testing.T) {
	web, admin, bob, console, auth, keyId, files := pushWeb(t)
	switchDB, localDB := web.state.get()
	switchDB.TitlesMap["0100000000020"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Name: "Other Game"}}
	web.state.set(switchDB, localDB)
	if code := sendJSON(console, "POST", "/install/inventory", consoleInventory, "Authorization", auth).Code; code != http.StatusOK {
		t.Fatalf("stored: %d", code)
	}
	sizes := keptSizes(web)

	var consoles struct {
		Consoles []netConsoleRow `json:"consoles"`
	}
	json.Unmarshal(bob.do("GET", "/api/install/consoles", nil).Body.Bytes(), &consoles)
	row := consoles.Consoles[0]
	if row.Inventory == nil || row.Inventory.Titles != 2 || row.Inventory.Outdated == nil || *row.Inventory.Outdated != 1 ||
		*row.Inventory.ToUpdateSize != sizes["update"]+sizes["dlc"] || row.System == nil || row.System.Firmware != "19.0.1" || !row.System.Emummc {
		t.Fatalf("the console: %+v %+v", row, row.Inventory)
	}

	var detail netConsoleDetail
	got := bob.do("GET", "/api/install/consoles/"+keyId, nil)
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &detail) != nil {
		t.Fatalf("the page of the console: %d %s", got.Code, got.Body.String())
	}
	if !detail.Enabled || detail.Id != keyId || detail.Name != "Switch" || len(detail.Games) != 2 || detail.Fits == nil || !*detail.Fits {
		t.Fatalf("the console: %s", got.Body.String())
	}
	game, other := detail.Games[0], detail.Games[1]
	if game.TitleId != "0100000000010000" || game.Name != "Game" || game.Version != 65536 || game.Newest != 131072 || !game.InLibrary || !game.Outdated ||
		game.Storage != "sd" || game.Dlc != 0 || len(game.MissingDlc) != 1 || game.MissingDlc[0].TitleId != "0100000000011001" || game.MissingDlc[0].FileId != files["dlc"] ||
		game.Play == nil || *game.Play != (netPlayStats{Minutes: 2040, Launches: 57, LastPlayed: 1763400000}) {
		t.Fatalf("the game: %+v", game)
	}
	if other.TitleId != "0100000000020000" || other.Name != "Other Game" || other.InLibrary || other.Outdated || other.Storage != "nand" || other.Play == nil || other.Play.Minutes != 30 {
		t.Fatalf("the game the library lacks: %+v", other)
	}
	if len(detail.ToUpdate) != 2 || detail.ToUpdate[0].Kind != "update" || detail.ToUpdate[0].Version != 131072 || detail.ToUpdate[0].FileId != files["update"] ||
		detail.ToUpdate[1].Kind != "dlc" || detail.ToUpdate[1].FileId != files["dlc"] || detail.ToUpdate[0].Size != sizes["update"] {
		t.Fatalf("the files to send: %+v", detail.ToUpdate)
	}

	// a SD card too full, and a console up to date
	full := strings.Replace(consoleInventory, `"sdFree":1000000`, `"sdFree":10`, 1)
	sendJSON(console, "POST", "/install/inventory", full, "Authorization", auth)
	json.Unmarshal(bob.do("GET", "/api/install/consoles/"+keyId, nil).Body.Bytes(), &detail)
	if detail.Fits == nil || *detail.Fits {
		t.Fatalf("does not fit: %+v", detail.Fits)
	}
	upToDate := `{"items":[{"titleId":"0100000000010000","type":"game","version":0,"storage":"sd"},` +
		`{"titleId":"0100000000010800","type":"update","version":131072,"storage":"sd"},` +
		`{"titleId":"0100000000011001","type":"dlc","version":0,"storage":"sd"}]}`
	sendJSON(console, "POST", "/install/inventory", upToDate, "Authorization", auth)
	detail = netConsoleDetail{}
	json.Unmarshal(bob.do("GET", "/api/install/consoles/"+keyId, nil).Body.Bytes(), &detail)
	if len(detail.ToUpdate) != 0 || detail.Games[0].Outdated || detail.Games[0].Dlc != 1 || *detail.Inventory.Outdated != 0 || detail.Fits != nil || detail.System != nil {
		t.Fatalf("up to date: %+v", detail)
	}

	// only the consoles of the user
	if code := admin.do("GET", "/api/install/consoles/"+keyId, nil).Code; code != http.StatusNotFound {
		t.Fatalf("the console of another user: %d", code)
	}
	if code := bob.do("GET", "/api/install/consoles/0000000000000000", nil).Code; code != http.StatusNotFound {
		t.Fatalf("no such console: %d", code)
	}
}

// Bringing a console up to date sends its files as Send to console does, as many as may wait for
// it: the rest are left for later.
func TestNetConsoleUpdateQueue(t *testing.T) {
	web, admin, bob, console, auth, keyId, files := pushWeb(t)
	sendJSON(console, "POST", "/install/inventory", consoleInventory, "Authorization", auth)
	if code := sendJSON(admin, "POST", "/api/install/consoles/"+keyId+"/update", "").Code; code != http.StatusNotFound {
		t.Fatalf("the console of another user: %d", code)
	}

	got := sendJSON(bob, "POST", "/api/install/consoles/"+keyId+"/update", "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"queued":2`) || !strings.Contains(got.Body.String(), `"left":0`) {
		t.Fatalf("sent: %d %s", got.Code, got.Body.String())
	}
	var answer pushWaitAnswer
	json.Unmarshal(console.do("GET", "/install/push/wait?timeout=1", nil, "Authorization", auth).Body.Bytes(), &answer)
	if len(answer.Items) != 2 || answer.Items[0].FileId != files["update"] || answer.Items[1].FileId != files["dlc"] {
		t.Fatalf("the console gets them: %+v", answer.Items)
	}
	noted := false
	for _, entry := range web.activities().List() {
		if entry.Action == activity.ACTION_NET_PUSH && entry.User == "bob" && entry.Detail == `"Switch": Game` {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("noted as a send: %+v", web.activities().List())
	}

	// 49 waiting: one more fits, one is left
	waiting := []*netPush{}
	for i := 0; i < netPushMax-1; i++ {
		waiting = append(waiting, &netPush{id: fmt.Sprintf("%032x", i), keyId: keyId, fileId: files["game"], made: time.Now()})
	}
	if err := web.netPushes.add(waiting); err != nil {
		t.Fatal(err)
	}
	got = sendJSON(bob, "POST", "/api/install/consoles/"+keyId+"/update", `{"compressed":false}`)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"queued":1`) || !strings.Contains(got.Body.String(), `"left":1`) {
		t.Fatalf("one fits: %d %s", got.Code, got.Body.String())
	}
	if web.netPushes.waiting(keyId) != netPushMax {
		t.Fatalf("waiting: %d", web.netPushes.waiting(keyId))
	}
	if code := sendJSON(bob, "POST", "/api/install/consoles/"+keyId+"/update", "").Code; code != http.StatusTooManyRequests {
		t.Fatalf("full: %d", code)
	}
	if code := sendJSON(bob, "POST", "/api/install/consoles/"+keyId+"/update", "nonsense").Code; code != http.StatusBadRequest {
		t.Fatalf("a body that is not JSON: %d", code)
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	if code := sendJSON(bob, "POST", "/api/install/consoles/"+keyId+"/update", "").Code; code != http.StatusConflict {
		t.Fatalf("turned off: %d", code)
	}
}

// A console that has told nothing has nothing to bring up to date.
func TestNetConsoleUpdateNeedsAnInventory(t *testing.T) {
	_, _, bob, _, _, keyId, _ := pushWeb(t)
	if code := sendJSON(bob, "POST", "/api/install/consoles/"+keyId+"/update", "").Code; code != http.StatusConflict {
		t.Fatalf("nothing told: %d", code)
	}
	var detail netConsoleDetail
	got := bob.do("GET", "/api/install/consoles/"+keyId, nil)
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &detail) != nil || detail.Inventory != nil || len(detail.Games) != 0 || detail.Fits != nil {
		t.Fatalf("nothing told: %d %s", got.Code, got.Body.String())
	}
}

// The time a user played a game sums their consoles, and the firmware of each console is known.
func TestNetTitlePlayAndFirmwares(t *testing.T) {
	web, _, bob, console, auth, _, _ := pushWeb(t)
	lite := createKey(t, bob, "Switch Lite", ROLE_CONSOLE)
	sendJSON(console, "POST", "/install/inventory", consoleInventory, "Authorization", auth)
	other := `{"items":[],"console":{"firmware":"18.1.0"},"play":[{"titleId":"0100000000010000","minutes":60,"launches":3,"lastPlayed":1763500000}]}`
	if code := sendJSON(console, "POST", "/install/inventory", other, "Authorization", basic("switch", lite.Key)).Code; code != http.StatusOK {
		t.Fatalf("stored: %d", code)
	}
	stats, ok := web.netTitlePlay("bob", "0100000000010800")
	if !ok || stats != (netPlayStats{Minutes: 2100, Launches: 60, LastPlayed: 1763500000}) {
		t.Fatalf("played: %v %+v", ok, stats)
	}
	if _, ok := web.netTitlePlay("bob", "0100000000050000"); ok {
		t.Fatal("a game never played")
	}
	if _, ok := web.netTitlePlay("alice", "0100000000010000"); ok {
		t.Fatal("the consoles of another user")
	}
	firmwares := map[string]netConsoleFirmware{}
	for _, firmware := range web.netUserFirmwares("bob") {
		firmwares[firmware.Name] = firmware
	}
	if len(firmwares) != 2 || firmwares["Switch"].Firmware != "19.0.1" || firmwares["Switch"].Code != 19<<26|1<<16 || firmwares["Switch Lite"].Code != 18<<26|1<<20 {
		t.Fatalf("the firmwares: %+v", firmwares)
	}
}

// The paths of the consoles and of their reports are described in openapi.json.
func TestOpenApiDocumentsTheConsoles(t *testing.T) {
	data, err := os.ReadFile("../resources/static/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	web, _, _ := netWeb(t)
	documented := 0
	web.router.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		path, _ := route.GetPathTemplate()
		methods, _ := route.GetMethods()
		if !strings.HasPrefix(path, netInstallConsoles) && !strings.HasPrefix(path, netReportsApi) && path != netInstallInventory && path != netInstallReport {
			return nil
		}
		path = withoutPatterns(path)
		for _, method := range methods {
			if _, ok := spec.Paths[path][strings.ToLower(method)]; !ok && method != http.MethodHead {
				t.Errorf("%s %s is not documented", method, path)
			}
		}
		documented++
		return nil
	})
	if documented < 7 {
		t.Fatalf("only %d routes checked", documented)
	}
}

// withoutPatterns is a path of the router as the documentation names it: its parameters without
// their patterns, {id:[0-9a-f]{16}} as {id}.
func withoutPatterns(path string) string {
	var out strings.Builder
	depth, pattern := 0, false
	for _, r := range path {
		switch {
		case r == '{':
			depth++
			if depth == 1 {
				out.WriteRune(r)
			}
		case r == '}':
			depth--
			if depth == 0 {
				out.WriteRune(r)
				pattern = false
			}
		case depth == 1 && r == ':':
			pattern = true
		case !pattern:
			out.WriteRune(r)
		}
	}
	return out.String()
}
