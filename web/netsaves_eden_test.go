package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/edensync"
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
)

// made-up profiles of Eden
var (
	edenPlayer = edensync.ProfileID{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x01}
	edenKid    = edensync.ProfileID{0x21, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x02}
)

// pairPc pairs a computer for bob, as NXPress does, and gives its key and the id of the key.
func pairPc(t *testing.T, web *Web, bob *client, name string, ip string) (string, string) {
	t.Helper()
	request := httptest.NewRequest("POST", "/install/pair", strings.NewReader(fmt.Sprintf(`{"console":%q,"kind":"pc"}`, name)))
	request.RemoteAddr = ip + ":5000"
	recorder := httptest.NewRecorder()
	bob.handler.ServeHTTP(recorder, request)
	var asked struct{ Id string }
	if recorder.Code != http.StatusCreated || json.Unmarshal(recorder.Body.Bytes(), &asked) != nil {
		t.Fatalf("asking: %d %s", recorder.Code, recorder.Body.String())
	}
	found := false
	for _, row := range pairsOf(t, bob) {
		if row.Id == asked.Id && row.Kind == netPairPc && row.Console == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("the computer is not shown waiting: %+v", pairsOf(t, bob))
	}
	if got := bob.do("POST", "/api/install/pairs/"+asked.Id, url.Values{"action": {"accept"}}); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"kind":"pc"`) {
		t.Fatalf("accepted: %d %s", got.Code, got.Body.String())
	}
	code, answer := waitPair(&client{t: t, handler: bob.handler, ip: ip}, asked.Id)
	key, _ := answer["key"].(string)
	if code != http.StatusOK || key == "" {
		t.Fatalf("the key: %d %v", code, answer)
	}
	for _, row := range web.auth.users.KeyRows("bob") {
		if row.Role == ROLE_PC && row.Name == name {
			return key, row.ID
		}
	}
	t.Fatal("no key of the role PC")
	return "", ""
}

// pcUpload uploads a save as a computer.
func pcUpload(t *testing.T, c *client, key, title, query string, body []byte) netSave {
	t.Helper()
	got := sendBody(c, "POST", "/install/saves/"+title+"?"+query, body, "Authorization", bearer(key))
	var answer struct{ Id string }
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &answer) != nil {
		t.Fatalf("uploaded: %d %s", got.Code, got.Body.String())
	}
	return netSave{Id: answer.Id, TitleId: title}
}

func savesBySource(saves []netSave) map[string]int {
	counted := map[string]int{}
	for _, save := range saves {
		counted[netSaveSource(save)]++
	}
	return counted
}

// A computer is paired with a code as a console is, and gets a key of its own role: it is never
// one of the consoles, and it may only keep saves.
func TestEdenComputerPairedKeepsOnlySaves(t *testing.T) {
	web, _, bob, _, _, _, _ := pushWeb(t)
	key, keyId := pairPc(t, web, bob, "NXPress on DESK-1", "192.168.1.60")
	if consoles := web.netUserConsoles("bob"); len(consoles) != 1 || consoles[0].ID == keyId {
		t.Fatalf("a computer is not a console: %+v", consoles)
	}
	for _, row := range web.auth.users.KeyRows("bob") {
		if row.ID == keyId && (row.Icon() != "bi-pc-display" || row.RoleName() != "Computer") {
			t.Fatalf("the key of the computer: %+v", row)
		}
	}
	pc := &client{t: t, handler: bob.handler}
	if got := pc.do("GET", "/install/saves", nil, "Authorization", bearer(key)); got.Code != http.StatusOK {
		t.Fatalf("the saves: %d %s", got.Code, got.Body.String())
	}
	for _, path := range []string{"/install/dbi/", "/install/tinfoil", "/install/ownfoil", "/index.html", "/saves", "/api/saves", "/install/push/wait?timeout=1", "/install/saves/../dbi/"} {
		if got := pc.do("GET", path, nil, "Authorization", bearer(key)); got.Code == http.StatusOK {
			t.Errorf("a computer opened %s", path)
		}
	}
	if got := sendBody(pc, "POST", "/install/inventory", []byte(`{"titles":[]}`), "Authorization", bearer(key)); got.Code == http.StatusOK {
		t.Error("a computer tells what is installed")
	}
	// a key of a computer is made only by pairing one
	if got := bob.do("POST", "/api/keys", url.Values{"name": {"Mine"}, "role": {ROLE_PC}}); got.Code != http.StatusBadRequest {
		t.Fatalf("a key of a computer by hand: %d", got.Code)
	}
}

// A computer uploads the save of a profile of Eden, marked with the emulator, the profile, the
// computer and that it came by itself, and sees the hash of the files of every save; the consoles
// do not. Ten are kept of each source, so the saves of Eden never push out the ones of a console.
func TestEdenComputerUploadAndKeepsPerSource(t *testing.T) {
	web, _, bob, console, auth, _, _ := pushWeb(t)
	key, _ := pairPc(t, web, bob, "NXPress on DESK-1", "192.168.1.60")
	pc := &client{t: t, handler: bob.handler}
	body := saveZip(t, "save.dat", "slot/1.bin")
	sent := pcUpload(t, pc, key, "0100000000010000", "emulator=eden&account=Player&auto=1&profile="+strings.ToUpper(edenPlayer.Raw()), body)
	listed := listSaves(t, pc, "/install/saves", "Authorization", bearer(key))
	hash, _ := edensync.HashZip(bytes.NewReader(body), int64(len(body)))
	if len(listed) != 1 || listed[0].Id != sent.Id || !listed[0].Emulator || listed[0].Console != "Eden" || listed[0].Profile != edenPlayer.String() ||
		listed[0].Device != "NXPress on DESK-1" || !listed[0].Auto || listed[0].Account != "Player" || listed[0].Hash != hash {
		t.Fatalf("listed for the computer: %+v", listed)
	}
	if forConsole := listSaves(t, console, "/install/saves", "Authorization", auth); len(forConsole) != 1 || forConsole[0].Hash != "" || !forConsole[0].Emulator {
		t.Fatalf("listed for the console: %+v", forConsole)
	}
	for query, want := range map[string]int{"emulator=ryujinx": http.StatusBadRequest, "profile=not-a-profile": http.StatusBadRequest, "emulator=CITRON": http.StatusOK} {
		if got := sendBody(pc, "POST", "/install/saves/0100000000010000?"+query, saveZip(t, "a"), "Authorization", bearer(key)); got.Code != want {
			t.Errorf("%s: %d", query, got.Code)
		}
	}
	// a console stays a console whatever it tells
	sendBody(console, "POST", "/install/saves/0100000000010000?emulator=eden&profile="+edenPlayer.String(), saveZip(t, "a"), "Authorization", auth)
	for _, save := range listSaves(t, console, "/install/saves", "Authorization", auth) {
		if save.Console == "Switch" && (save.Emulator || save.Profile != "") {
			t.Fatalf("a console upload marked as an emulator: %+v", save)
		}
	}

	for i := 0; i < netSavesKeep; i++ {
		sendBody(console, "POST", "/install/saves/0100000000020000", saveZip(t, "a"), "Authorization", auth)
	}
	for i := 0; i < netSavesKeep+3; i++ {
		pcUpload(t, pc, key, "0100000000020000", "emulator=eden&auto=1&profile="+edenPlayer.String(), saveZip(t, "a"))
	}
	saves, _ := web.netSavesOf("bob", "0100000000020000")
	if counted := savesBySource(saves); counted["console"] != netSavesKeep || counted["emulator:eden"] != netSavesKeep {
		t.Fatalf("kept per source: %v", counted)
	}
	// the old index, without sources of computers, reads as before
	if all, err := web.netSavesOf("bob", ""); err != nil || len(all) < 2*netSavesKeep {
		t.Fatalf("the index: %d %v", len(all), err)
	}
}

// The profiles of Eden of a computer are paired with the accounts of the consoles of the user, by
// the computer or on the web; a console cannot touch them, and the computer of another user sees
// none of them, nor the saves.
func TestEdenProfilesAndPermissions(t *testing.T) {
	web, admin, bob, console, auth, _, _ := pushWeb(t)
	key, keyId := pairPc(t, web, bob, "NXPress on DESK-1", "192.168.1.60")
	pc := &client{t: t, handler: bob.handler}
	sendBody(console, "POST", "/install/saves/0100000000010000?account=Bob", saveZip(t, "a"), "Authorization", auth)
	sendBody(console, "POST", "/install/saves/0100000000010000?account=Kid", saveZip(t, "a"), "Authorization", auth)

	report := `{"emulator":"eden","profiles":[{"profile":"` + edenPlayer.String() + `","name":"Player"},{"profile":"` + edenKid.Raw() + `","name":"Kid","account":"Kid"}]}`
	got := sendJSON(pc, "PUT", "/install/saves/profiles", report, "Authorization", bearer(key))
	var pairs edensync.Pairs
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &pairs) != nil {
		t.Fatalf("told: %d %s", got.Code, got.Body.String())
	}
	if strings.Join(pairs.Accounts, ",") != "Bob,Kid" || len(pairs.Profiles) != 2 || !pairs.Profiles[0].Mine || pairs.Profiles[0].Computer != "NXPress on DESK-1" ||
		pairs.Profiles[0].Account != "" || pairs.Profiles[1].Account != "Kid" || !pairs.Profiles[1].Chosen || pairs.Profiles[1].Profile != edenKid.String() {
		t.Fatalf("the pairs: %+v", pairs)
	}
	if got := sendJSON(bob, "POST", "/api/saves/profiles", `{"device":"`+keyId+`","profile":"`+edenPlayer.String()+`","account":"Bob"}`); got.Code != http.StatusOK {
		t.Fatalf("paired on the web: %d %s", got.Code, got.Body.String())
	}
	if got := sendJSON(bob, "POST", "/api/saves/profiles", `{"device":"`+keyId+`","profile":"`+edenPlayer.String()+`","account":"Nobody"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("an account no console has: %d", got.Code)
	}
	if got := sendJSON(bob, "POST", "/api/saves/profiles", `{"device":"`+keyId+`","profile":"00000000-0000-0000-0000-000000000009","account":"Bob"}`); got.Code != http.StatusNotFound {
		t.Fatalf("an unknown profile: %d", got.Code)
	}
	// told again with only one profile, without its account: the other is forgotten, the account stays
	got = sendJSON(pc, "PUT", "/install/saves/profiles", `{"emulator":"eden","profiles":[{"profile":"`+edenPlayer.String()+`","name":"Player 1"}]}`, "Authorization", bearer(key))
	json.Unmarshal(got.Body.Bytes(), &pairs)
	if len(pairs.Profiles) != 1 || pairs.Profiles[0].Account != "Bob" || pairs.Profiles[0].Name != "Player 1" {
		t.Fatalf("told again: %+v", pairs)
	}
	if got := sendJSON(pc, "PUT", "/install/saves/profiles", `{"emulator":"ryujinx","profiles":[]}`, "Authorization", bearer(key)); got.Code != http.StatusBadRequest {
		t.Fatalf("an unknown emulator: %d", got.Code)
	}

	// a console key cannot tell profiles nor conflicts
	if got := console.do("GET", "/install/saves/profiles", nil, "Authorization", auth); got.Code != http.StatusForbidden {
		t.Fatalf("a console reads the profiles: %d", got.Code)
	}
	if got := sendJSON(console, "PUT", "/install/saves/profiles", report, "Authorization", auth); got.Code != http.StatusForbidden {
		t.Fatalf("a console tells profiles: %d", got.Code)
	}
	if got := sendJSON(console, "POST", "/install/saves/conflicts", `{}`, "Authorization", auth); got.Code != http.StatusForbidden {
		t.Fatalf("a console tells a conflict: %d", got.Code)
	}

	// the computer of another user
	secret, _, err := web.auth.users.AddAPIKey("alice", "NXPress on OTHER", ROLE_PC)
	if err != nil {
		t.Fatal(err)
	}
	other := &client{t: t, handler: admin.handler}
	if saves := listSaves(t, other, "/install/saves", "Authorization", bearer(secret)); len(saves) != 0 {
		t.Fatalf("the saves of another user: %+v", saves)
	}
	bobs := listSaves(t, pc, "/install/saves", "Authorization", bearer(key))
	if got := other.do("GET", "/install/saves/0100000000010000/"+bobs[0].Id, nil, "Authorization", bearer(secret)); got.Code != http.StatusNotFound {
		t.Fatalf("a save of another user: %d", got.Code)
	}
	got = other.do("GET", "/install/saves/profiles", nil, "Authorization", bearer(secret))
	if json.Unmarshal(got.Body.Bytes(), &pairs); len(pairs.Profiles) != 0 || len(pairs.Accounts) != 0 {
		t.Fatalf("the profiles of another user: %+v", pairs)
	}
	conflict := `{"titleId":"0100000000010000","profile":"` + edenPlayer.String() + `","consoleSave":"` + bobs[0].Id + `","emulatorSave":"` + bobs[1].Id + `"}`
	if got := sendJSON(other, "POST", "/install/saves/conflicts", conflict, "Authorization", bearer(secret)); got.Code != http.StatusNotFound {
		t.Fatalf("a conflict with the saves of another user: %d", got.Code)
	}
}

// A computer tells that both saves of a game changed; the user chooses on the web which one the
// emulator keeps, the computer reads the choice and closes it. Only the computer that told it
// sees and closes it, both saves stay, and the activity tells both steps.
func TestEdenConflicts(t *testing.T) {
	web, admin, bob, console, auth, _, _ := pushWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleNetSavesPage()
	key, _ := pairPc(t, web, bob, "NXPress on DESK-1", "192.168.1.60")
	otherKey, _ := pairPc(t, web, bob, "NXPress on DESK-2", "192.168.1.61")
	pc := &client{t: t, handler: bob.handler}
	sendBody(console, "POST", "/install/saves/0100000000010000?account=Bob", saveZip(t, "console.dat"), "Authorization", auth)
	pcUpload(t, pc, key, "0100000000010000", "emulator=eden&auto=1&profile="+edenPlayer.String(), saveZip(t, "eden.dat"))
	saves := listSaves(t, pc, "/install/saves", "Authorization", bearer(key))
	emulatorSave, consoleSave := saves[0].Id, saves[1].Id
	if !saves[0].Emulator {
		emulatorSave, consoleSave = consoleSave, emulatorSave
	}
	body := `{"titleId":"0100000000010000","profile":"` + edenPlayer.String() + `","consoleSave":"` + consoleSave + `","emulatorSave":"` + emulatorSave + `"}`
	got := sendJSON(pc, "POST", "/install/saves/conflicts", body, "Authorization", bearer(key))
	var told struct{ Id string }
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &told) != nil || len(told.Id) != 16 {
		t.Fatalf("told: %d %s", got.Code, got.Body.String())
	}
	var again struct{ Id string }
	json.Unmarshal(sendJSON(pc, "POST", "/install/saves/conflicts", body, "Authorization", bearer(key)).Body.Bytes(), &again)
	if again.Id != told.Id {
		t.Fatal("told twice is the same conflict")
	}
	if got := sendJSON(pc, "POST", "/install/saves/conflicts", strings.Replace(body, consoleSave, "00000000000000aa", 1), "Authorization", bearer(key)); got.Code != http.StatusNotFound {
		t.Fatalf("a save that is not there: %d", got.Code)
	}
	conflictsOf := func(key string) []edensync.Conflict {
		var answer struct{ Conflicts []edensync.Conflict }
		got := pc.do("GET", "/install/saves/conflicts", nil, "Authorization", bearer(key))
		if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &answer) != nil {
			t.Fatalf("the conflicts: %d %s", got.Code, got.Body.String())
		}
		return answer.Conflicts
	}
	if open := conflictsOf(key); len(open) != 1 || open[0].Keep != "" || open[0].ConsoleSave != consoleSave || open[0].TitleId != "0100000000010000" {
		t.Fatalf("open: %+v", open)
	}
	if open := conflictsOf(otherKey); len(open) != 0 {
		t.Fatalf("another computer sees it: %+v", open)
	}
	if got := sendJSON(pc, "POST", "/install/saves/conflicts/"+told.Id+"/done", ``, "Authorization", bearer(otherKey)); got.Code != http.StatusNotFound {
		t.Fatalf("another computer closes it: %d", got.Code)
	}

	sendJSON(pc, "PUT", "/install/saves/profiles", `{"emulator":"eden","profiles":[{"profile":"`+edenPlayer.String()+`","name":"Player","account":"Bob"}]}`, "Authorization", bearer(key))
	folder, _ := fakeEdenFolder(t, nil)
	web.edenLocal.find = func() (edensync.Folder, bool) { return folder, true }
	page := bob.do("GET", "/saves", nil).Body.String()
	for _, want := range []string{"Game: both saves have changed", `data-eden-conflict="/api/saves/conflicts/` + told.Id + `"`, `data-eden-keep="console"`, `data-eden-keep="emulator"`,
		"Keep this save", "Both stay in the library, whichever you choose.", "uploaded by itself", "net-save-console", "Eden on your computers", "NXPress on DESK-1",
		`data-eden-profile data-device=`, `<option value="Bob" selected>Bob</option>`, "Eden on this computer"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %q", want)
		}
	}
	if dump := os.Getenv("SLM_EDEN_PAGE"); dump != "" {
		os.WriteFile(dump, []byte(page), 0o644)
	}

	if got := sendJSON(bob, "POST", "/api/saves/conflicts/"+told.Id, `{"keep":"both"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("an unknown choice: %d", got.Code)
	}
	if got := sendJSON(admin, "POST", "/api/saves/conflicts/"+told.Id, `{"keep":"console"}`); got.Code != http.StatusNotFound {
		t.Fatalf("another user chooses: %d", got.Code)
	}
	if got := sendJSON(bob, "POST", "/api/saves/conflicts/"+told.Id, `{"keep":"console"}`); got.Code != http.StatusOK {
		t.Fatalf("chosen: %d %s", got.Code, got.Body.String())
	}
	if open := conflictsOf(key); len(open) != 1 || open[0].Keep != edensync.KeepConsole || open[0].Decided == 0 {
		t.Fatalf("the choice: %+v", open)
	}
	if strings.Contains(bob.do("GET", "/saves", nil).Body.String(), "both saves have changed") {
		t.Error("a choice made is no longer asked")
	}
	if got := sendJSON(pc, "POST", "/install/saves/conflicts/"+told.Id+"/done", ``, "Authorization", bearer(key)); got.Code != http.StatusOK {
		t.Fatalf("done: %d %s", got.Code, got.Body.String())
	}
	if open := conflictsOf(key); len(open) != 0 {
		t.Fatalf("closed: %+v", open)
	}
	if left := listSaves(t, bob, "/api/saves"); len(left) != 2 {
		t.Fatalf("both saves stay: %+v", left)
	}
	actions := map[string]string{}
	for _, entry := range web.activities().List() {
		actions[entry.Action] = entry.Detail
	}
	if actions[activity.ACTION_SAVES_CONFLICT] != "Game" || actions[activity.ACTION_SAVES_CHOICE] != "Game + console" {
		t.Fatalf("the activity: %v", actions)
	}
}

// fakeEdenFolder is a data folder of Eden with one profile, Player, and a save of a game.
func fakeEdenFolder(t *testing.T, files map[string]string) (edensync.Folder, string) {
	t.Helper()
	root := t.TempDir()
	folder := edensync.Folder{Emulator: edensync.Eden, Root: root, Nand: filepath.Join(root, "nand")}
	profiles := filepath.Join(folder.Nand, "system", "save", "8000000000000010", "su", "avators", "profiles.dat")
	os.MkdirAll(filepath.Dir(profiles), 0o755)
	os.WriteFile(profiles, edensync.ProfilesFile([]edensync.Profile{{ID: edenPlayer, Name: "Player"}}), 0o644)
	dir := folder.SaveFolder(edenPlayer, "0100000000010000")
	os.MkdirAll(dir, 0o755)
	for name, content := range files {
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	}
	return folder, dir
}

// Eden on the computer the app runs on: the user who turns it on gets its saves, with the ones of
// their console brought in, conflicts asked on the page, and nothing done when it is off; another
// user cannot take it, and without Eden there is nothing to turn on.
func TestEdenOnThisComputer(t *testing.T) {
	web, admin, bob, console, auth, _, _ := pushWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleNetSavesPage()
	folder, dir := fakeEdenFolder(t, map[string]string{"save.dat": "eden 1", ".yuzu_save_size": "size"})
	found := true
	web.edenLocal.find = func() (edensync.Folder, bool) { return folder, found }
	web.edenLocal.running = func() (bool, error) { return false, nil }

	page := bob.do("GET", "/saves", nil).Body.String()
	if !strings.Contains(page, "Eden on this computer") || !strings.Contains(page, `data-eden-local>`) {
		t.Fatal("the switch of Eden on this computer is offered, off")
	}
	if report, _ := web.edenLocalPass(); len(report.Uploaded) != 0 {
		t.Fatal("off, nothing is done")
	}
	if got := sendJSON(bob, "POST", "/api/saves/eden-local", `{"on":true}`); got.Code != http.StatusOK {
		t.Fatalf("on: %d %s", got.Code, got.Body.String())
	}
	if got := sendJSON(admin, "POST", "/api/saves/eden-local", `{"on":true}`); got.Code != http.StatusConflict {
		t.Fatalf("another user takes it: %d", got.Code)
	}
	if strings.Contains(admin.do("GET", "/saves", nil).Body.String(), "Another user keeps it in step") == false {
		t.Error("another user is told it is taken")
	}
	report, err := web.edenLocalPass()
	if err != nil || len(report.Uploaded) != 1 {
		t.Fatalf("uploaded: %+v %v", report, err)
	}
	saves := listSaves(t, bob, "/api/saves")
	if len(saves) != 1 || !saves[0].Emulator || !saves[0].Auto || saves[0].Device != edenLocalName() || saves[0].Profile != edenPlayer.String() {
		t.Fatalf("the save of Eden: %+v", saves)
	}

	// the console uploads a newer one, of the only account, with the only profile: paired by itself
	newer := saveZip(t, "save.dat")
	sendBody(console, "POST", "/install/saves/0100000000010000?account=Bob", newer, "Authorization", auth)
	report, err = web.edenLocalPass()
	if err != nil || len(report.Brought) != 1 {
		t.Fatalf("brought: %+v %v", report, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "save.dat")); string(data) != "data of save.dat" {
		t.Fatalf("in Eden: %q", data)
	}
	if size, _ := os.ReadFile(filepath.Join(dir, ".yuzu_save_size")); string(size) != "size" {
		t.Fatal("the size Eden keeps stays")
	}

	// both change: asked on the page, Eden keeps its own
	os.WriteFile(filepath.Join(dir, "save.dat"), []byte("eden 2"), 0o644)
	sendBody(console, "POST", "/install/saves/0100000000010000?account=Bob", saveZip(t, "other.dat"), "Authorization", auth)
	web.edenLocal.running = func() (bool, error) { return true, nil }
	if report, _ := web.edenLocalPass(); !report.Running {
		t.Fatal("with Eden open nothing is done")
	}
	web.edenLocal.running = func() (bool, error) { return false, nil }
	if report, _ := web.edenLocalPass(); len(report.Conflicts) != 1 {
		t.Fatalf("a conflict: %+v", report)
	}
	conflicts, _ := web.edenConflictsOf("bob", edenLocalDevice)
	if len(conflicts) != 1 || !strings.Contains(bob.do("GET", "/saves", nil).Body.String(), "both saves have changed") {
		t.Fatalf("asked: %+v", conflicts)
	}
	if got := sendJSON(bob, "POST", "/api/saves/conflicts/"+conflicts[0].Id, `{"keep":"emulator"}`); got.Code != http.StatusOK {
		t.Fatalf("chosen: %d", got.Code)
	}
	if report, _ := web.edenLocalPass(); len(report.Brought)+len(report.Uploaded) != 0 {
		t.Fatalf("Eden keeps its own: %+v", report)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "save.dat")); string(data) != "eden 2" {
		t.Fatalf("in Eden: %q", data)
	}
	if left, _ := web.edenConflictsOf("bob", edenLocalDevice); len(left) != 0 {
		t.Fatalf("closed: %+v", left)
	}

	if got := sendJSON(bob, "POST", "/api/saves/eden-local", `{"on":false}`); got.Code != http.StatusOK {
		t.Fatalf("off: %d", got.Code)
	}
	os.WriteFile(filepath.Join(dir, "save.dat"), []byte("eden 3"), 0o644)
	if report, _ := web.edenLocalPass(); len(report.Uploaded) != 0 {
		t.Fatal("off again, nothing is done")
	}
	found = false
	if got := sendJSON(bob, "POST", "/api/saves/eden-local", `{"on":true}`); got.Code != http.StatusNotFound {
		t.Fatalf("without Eden: %d", got.Code)
	}
	if strings.Contains(bob.do("GET", "/saves", nil).Body.String(), "data-eden-local") {
		t.Fatal("without Eden there is no switch")
	}
}

// The client of the package edensync against the app itself: a computer pairs as NXPress will,
// and a pass uploads the save of Eden and brings the newer one of the console.
func TestEdenHTTPLibraryAgainstTheApp(t *testing.T) {
	web, _, bob, console, auth, _, _ := pushWeb(t)
	server := httptest.NewServer(bob.handler)
	defer server.Close()
	ctx := context.Background()
	asked, err := edensync.AskPairing(ctx, server.Client(), server.URL, "NXPress on TEST-PC")
	if err != nil || asked.Code == "" {
		t.Fatalf("asked: %+v %v", asked, err)
	}
	if waiting, err := edensync.CheckPairing(ctx, server.Client(), server.URL, asked.Id); err != nil || waiting.State != edensync.PairWaiting {
		t.Fatalf("waiting: %+v %v", waiting, err)
	}
	if got := bob.do("POST", "/api/install/pairs/"+asked.Id, url.Values{"action": {"accept"}}); got.Code != http.StatusOK {
		t.Fatalf("accepted: %d", got.Code)
	}
	answer, err := edensync.CheckPairing(ctx, server.Client(), server.URL, asked.Id)
	if err != nil || answer.State != edensync.PairAccepted || answer.Key == "" || answer.User != "bob" {
		t.Fatalf("the key: %+v %v", answer, err)
	}
	if gone, _ := edensync.CheckPairing(ctx, server.Client(), server.URL, asked.Id); gone.State != edensync.PairExpired {
		t.Fatalf("given once: %+v", gone)
	}

	folder, dir := fakeEdenFolder(t, map[string]string{"save.dat": "eden 1"})
	library := &edensync.HTTPLibrary{BaseURL: server.URL, Key: answer.Key, Client: server.Client()}
	syncer := &edensync.Syncer{Folder: folder, Library: library, StatePath: filepath.Join(t.TempDir(), "state.json"), TempDir: t.TempDir(),
		IsRunning: func() (bool, error) { return false, nil }}
	report, err := syncer.Pass(ctx)
	if err != nil || len(report.Uploaded) != 1 || len(report.Errors) != 0 {
		t.Fatalf("the first pass: %+v %v", report, err)
	}
	sendBody(console, "POST", "/install/saves/0100000000010000?account=Bob", saveZip(t, "save.dat"), "Authorization", auth)
	report, err = syncer.Pass(ctx)
	if err != nil || len(report.Brought) != 1 || len(report.Errors) != 0 {
		t.Fatalf("the second pass: %+v %v", report, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "save.dat")); string(data) != "data of save.dat" {
		t.Fatalf("in Eden: %q", data)
	}
	pairs, err := library.Pairs(ctx)
	if err != nil || len(pairs.Profiles) != 1 || pairs.Profiles[0].Account != "Bob" || !pairs.Profiles[0].Mine {
		t.Fatalf("paired by itself: %+v %v", pairs, err)
	}
	if _, err := library.ReportConflict(ctx, edensync.Conflict{TitleId: "0100000000010000", Profile: edenPlayer.String(), ConsoleSave: "00000000000000aa", EmulatorSave: "00000000000000bb"}); err == nil {
		t.Fatal("a conflict of saves that are not there")
	} else if status, ok := err.(*edensync.StatusError); !ok || status.Status != http.StatusNotFound {
		t.Fatalf("the error: %v", err)
	}
	var zipped bytes.Buffer
	if err := library.Download(ctx, "0100000000010000", "0000000000000000", &zipped); err == nil {
		t.Fatal("a save that is not there")
	}
	computers := web.edenSection("bob", nil).Computers
	if len(computers) != 1 || computers[0].Name != "NXPress on TEST-PC" || computers[0].Sync.IsZero() {
		t.Fatalf("the computer: %+v", computers)
	}
	io.Copy(io.Discard, &zipped)
}
