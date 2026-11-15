package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

func pushCompressedBody(keyId string, ids ...string) string {
	data, _ := json.Marshal(map[string]any{"keyId": keyId, "fileIds": ids, "compressed": true})
	return string(data)
}

// netCopyFile is the file of the library sent with that id.
func netCopyFile(t *testing.T, web *Web, id string) netFile {
	t.Helper()
	_, byId := web.netFilesKept(settings.ReadSettings(web.dataFolder).NetInstall)
	file, ok := byId[id]
	if !ok {
		t.Fatalf("no file %s", id)
	}
	return file
}

// netSendGameFiles are the files of the game as the dialog reads them, by id.
func netSendGameFiles(t *testing.T, bob *client) map[string]netGameFile {
	t.Helper()
	var answer struct {
		Files []netGameFile `json:"files"`
	}
	got := bob.do("GET", "/api/install/game/0100000000010000", nil)
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &answer) != nil {
		t.Fatalf("the files of the game: %d %s", got.Code, got.Body.String())
	}
	files := map[string]netGameFile{}
	for _, file := range answer.Files {
		files[file.Id] = file
	}
	return files
}

func takePushes(t *testing.T, console *client, auth string) []netPushItem {
	t.Helper()
	var answer pushWaitAnswer
	got := console.do("GET", "/install/push/wait?timeout=1", nil, "Authorization", auth)
	if got.Code == http.StatusOK {
		json.Unmarshal(got.Body.Bytes(), &answer)
	}
	return answer.Items
}

// pauseFileTasks makes the next task on files start paused, so what it does before it works can
// be looked at.
func pauseFileTasks(web *Web) {
	web.compressor.mutex.Lock()
	web.compressor.paused, web.compressor.resume = true, make(chan struct{})
	web.compressor.mutex.Unlock()
}

// A file in the experimental format of NXPress goes, with Compressed, as it is on the disk: its
// size, its extension and ?raw=1, at once. An NSZ without that content goes as Normal, and the
// dialog is told so.
func TestNetSendCompressedNativeAndAsIs(t *testing.T) {
	web, _, bob, console, auth, keyId, files := pushWeb(t)
	update := netCopyFile(t, web, files["update"])
	stat, _ := os.Stat(update.path)
	// the update taken for an NCZ of the extra compression, which the installers get rebuilt
	web.netStreams.mutex.Lock()
	web.netStreams.known[update.path] = netStreamEntry{size: stat.Size(), modTime: stat.ModTime(), stream: &switchfs.InstallStream{}}
	web.netStreams.mutex.Unlock()
	web.netFilesForget()

	dialog := netSendGameFiles(t, bob)
	if got := dialog[files["update"]]; got.CompressedSize != stat.Size() || got.CompressFirst || got.AsIs {
		t.Fatalf("the update in the experimental format: %+v", got)
	}
	if got := dialog[files["game"]]; !got.CompressFirst || got.CompressedSize != 0 {
		t.Fatalf("the NSP is compressed first, its size not known: %+v", got)
	}
	sent := sendJSON(bob, "POST", "/api/install/push", pushCompressedBody(keyId, files["update"]))
	if sent.Code != http.StatusOK || !strings.Contains(sent.Body.String(), `"preparing":0`) {
		t.Fatalf("sent: %d %s", sent.Code, sent.Body.String())
	}
	items := takePushes(t, console, auth)
	if len(items) != 1 || !strings.HasSuffix(items[0].Url, "?raw=1") || items[0].Size != stat.Size() || items[0].Format != "nsz" {
		t.Fatalf("as it is on the disk: %+v", items)
	}
	raw := console.do("GET", items[0].Url, nil, "Authorization", auth)
	original, _ := os.ReadFile(update.path)
	if raw.Code != http.StatusOK || !bytes.Equal(raw.Body.Bytes(), original) {
		t.Fatalf("the file as it is: %d %d bytes", raw.Code, raw.Body.Len())
	}

	// the same update taken for a plain NSZ
	web.netStreams.mutex.Lock()
	delete(web.netStreams.known, update.path)
	web.netStreams.mutex.Unlock()
	web.netFilesForget()
	nsz := netCopyFile(t, web, files["update"])
	if nsz.stream != nil {
		t.Fatal("a plain NSZ")
	}
	if got := netSendGameFiles(t, bob)[files["update"]]; !got.AsIs || got.CompressedSize != got.Size || got.CompressFirst {
		t.Fatalf("an NSZ goes as it is: %+v", got)
	}
	sendJSON(bob, "POST", "/api/install/push", pushCompressedBody(keyId, files["update"]))
	items = takePushes(t, console, auth)
	if len(items) != 1 || items[0].Url != netPushItemOf(nsz).Url || items[0].Format != "nsz" {
		t.Fatalf("an NSZ goes as Normal: %+v", items)
	}
}

// An NSP is compressed first, as a task, into the data folder: what was sent goes to the console
// only once the copy is checked, from /install/c/ with range requests behind the gate; the copy is
// used again while it is newer than the NSP, and deleted once every console declined it.
func TestNetSendCompressedNspPreparedFirst(t *testing.T) {
	web, _, bob, console, auth, keyId, files := pushWeb(t)
	if err := testnsp.WriteKeys(web.dataFolder); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { settings.InitSwitchKeys(t.TempDir()) })
	game := netCopyFile(t, web, files["game"])
	if _, err := testnsp.WriteNsp(game.path); err != nil {
		t.Fatal(err)
	}
	// older than the copy made of it
	old := time.Now().Add(-time.Hour)
	os.Chtimes(game.path, old, old)
	web.netFilesForget()
	before, _ := os.ReadFile(game.path)

	pauseFileTasks(web)
	sent := sendJSON(bob, "POST", "/api/install/push", pushCompressedBody(keyId, files["game"]))
	if sent.Code != http.StatusOK || !strings.Contains(sent.Body.String(), `"preparing":1`) {
		t.Fatalf("sent: %d %s", sent.Code, sent.Body.String())
	}
	copy := web.netSendCopy(files["game"])
	if web.netPushes.waiting(keyId) != 0 || web.netCopyWaits.count(keyId) != 1 {
		t.Fatalf("nothing goes before the copy: %d waiting, %d for the copy", web.netPushes.waiting(keyId), web.netCopyWaits.count(keyId))
	}
	if _, err := os.Stat(copy); err == nil {
		t.Fatal("the copy is not made yet")
	}
	if !web.pauseCompression(false) {
		t.Fatal("the task of the copy runs, paused")
	}
	task := waitForTask(t, web, tasklog.TASK_NET_PREPARE)
	if task.Error != nil {
		t.Fatalf("the copy: %+v", task.Error)
	}
	stat, err := os.Stat(copy)
	if err != nil || filepath.Dir(copy) != filepath.Join(web.dataFolder, netSendFolder) {
		t.Fatalf("the copy in the data folder: %v", err)
	}
	if after, _ := os.ReadFile(game.path); !bytes.Equal(before, after) {
		t.Fatal("the NSP of the library changed")
	}
	if web.netPushes.waiting(keyId) != 1 || web.netCopyWaits.count(keyId) != 0 {
		t.Fatal("what was sent goes once the copy is made")
	}
	if got := netSendGameFiles(t, bob)[files["game"]]; got.CompressFirst || got.CompressedSize != stat.Size() {
		t.Fatalf("the dialog knows the size of the copy: %+v", got)
	}
	items := takePushes(t, console, auth)
	if len(items) != 1 || !strings.HasPrefix(items[0].Url, netInstallCompressed+files["game"]+"/") || items[0].Format != "nsz" || items[0].Size != stat.Size() {
		t.Fatalf("the copy goes: %+v", items)
	}
	whole, _ := os.ReadFile(copy)
	part := console.do("GET", items[0].Url, nil, "Authorization", auth, "Range", "bytes=4-19")
	if part.Code != http.StatusPartialContent || !bytes.Equal(part.Body.Bytes(), whole[4:20]) {
		t.Fatalf("a range of the copy: %d %d bytes", part.Code, part.Body.Len())
	}
	if got := (&client{t: t, handler: console.handler}).do("GET", items[0].Url, nil); got.Code == http.StatusOK || got.Code == http.StatusPartialContent {
		t.Fatalf("the copy without a key: %d", got.Code)
	}

	// sent again: the copy is used again, at once
	again := sendJSON(bob, "POST", "/api/install/push", pushCompressedBody(keyId, files["game"]))
	if !strings.Contains(again.Body.String(), `"preparing":0`) {
		t.Fatalf("the copy used again: %s", again.Body.String())
	}
	second := takePushes(t, console, auth)
	if len(second) != 1 || second[0].Url != items[0].Url {
		t.Fatalf("the same copy: %+v", second)
	}
	decline := func(id string) {
		data, _ := json.Marshal(map[string]any{"id": id, "accepted": false})
		sendJSON(console, "POST", "/install/push/answer", string(data), "Authorization", auth)
	}
	decline(items[0].Id)
	if _, err := os.Stat(copy); err != nil {
		t.Fatal("the copy stays while another push uses it")
	}
	decline(second[0].Id)
	if _, err := os.Stat(copy); err == nil {
		t.Fatal("the copy goes once every console declined it")
	}

	// with the network install off, the route answers nothing
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	if got := console.do("GET", items[0].Url, nil, "Authorization", auth); got.Code != http.StatusNotFound {
		t.Fatalf("behind the gate: %d", got.Code)
	}
}

// A copy cancelled drops what waited for it, and the task tells it as the others do.
func TestNetSendCompressedCancelled(t *testing.T) {
	web, _, bob, console, auth, keyId, files := pushWeb(t)
	pauseFileTasks(web)
	sendJSON(bob, "POST", "/api/install/push", pushCompressedBody(keyId, files["game"], files["dlc"]))
	if web.netCopyWaits.count(keyId) != 2 {
		t.Fatalf("two wait for their copies: %d", web.netCopyWaits.count(keyId))
	}
	if !web.cancelCompression() {
		t.Fatal("the task of the copies is cancelled")
	}
	task := waitForTask(t, web, tasklog.TASK_NET_PREPARE)
	if task.Error == nil || task.Error.Text != tasklog.NOTE_COMPRESS_CANCELED {
		t.Fatalf("cancelled: %+v", task.Error)
	}
	if web.netCopyWaits.count(keyId) != 0 || web.netPushes.waiting(keyId) != 0 || len(takePushes(t, console, auth)) != 0 {
		t.Fatal("nothing goes to the console")
	}
	entries, _ := os.ReadDir(web.netSendFolder())
	if len(entries) != 0 {
		t.Fatalf("no copy left: %v", entries)
	}
}

// A file that is no NSP the app can compress (these are not real games) fails the task, which says
// to send it as Normal, and nothing goes.
func TestNetSendCompressedFailed(t *testing.T) {
	web, _, bob, _, _, keyId, files := pushWeb(t)
	sendJSON(bob, "POST", "/api/install/push", pushCompressedBody(keyId, files["game"]))
	task := waitForTask(t, web, tasklog.TASK_NET_PREPARE)
	if task.Error == nil || task.Error.Text != NOTE_NET_PREPARE_FAILED {
		t.Fatalf("failed: %+v", task.Error)
	}
	if web.netCopyWaits.count(keyId) != 0 || web.netPushes.waiting(keyId) != 0 {
		t.Fatal("nothing waits")
	}
}

// The copies nothing has used for 24 hours are deleted, the ones used are kept; at the start, also
// what a task left half written, and the tasks of the queue that were to make copies.
func TestNetSendSweep(t *testing.T) {
	web, _, _, _, _, _, _ := pushWeb(t)
	folder := web.netSendFolder()
	os.MkdirAll(folder, 0755)
	write := func(name string, age time.Duration) string {
		path := filepath.Join(folder, name)
		os.WriteFile(path, []byte("copy"), 0644)
		at := time.Now().Add(-age)
		os.Chtimes(path, at, at)
		return path
	}
	stale := write("0123456789abcdef.nsz", 25*time.Hour)
	fresh := write("1123456789abcdef.nsz", time.Hour)
	used := write("2123456789abcdef.nsz", 30*time.Hour)
	half := write(".3123456789abcdef.nsz.tmp", 30*time.Hour)
	web.netPushes.add([]*netPush{{id: "p", keyId: "k", fileId: "2123456789abcdef", made: time.Now(), copy: used}})
	queued := web.queue().add(QueuedTask{Kind: tasklog.TASK_NET_PREPARE, Paths: []string{"x.nsp"}})

	web.netCopySweep(false)
	for path, kept := range map[string]bool{stale: false, fresh: true, used: true, half: true} {
		if _, err := os.Stat(path); (err == nil) != kept {
			t.Errorf("%s kept: %v", filepath.Base(path), err == nil)
		}
	}
	if web.queue().position(queued) == 0 {
		t.Fatal("the queue stays while the app runs")
	}
	web.netCopySweep(true)
	if _, err := os.Stat(half); err == nil {
		t.Fatal("what was left half written goes at the start")
	}
	if web.queue().position(queued) != 0 {
		t.Fatal("no task makes copies after a restart: nothing waits for them")
	}
}

// The new texts are translated, and the copy is a task of files like the others.
func TestNetSendTextsAndKind(t *testing.T) {
	if taskKindNames[tasklog.TASK_NET_PREPARE] != NET_PREPARE_ACTION || !pausableKinds[tasklog.TASK_NET_PREPARE] || !queueKinds[tasklog.TASK_NET_PREPARE] {
		t.Error("the copy to send is not a task of files like the others")
	}
	if tasklog.FormatChange(tasklog.TASK_NET_PREPARE, "Game.nsp") != "NSP → NSZ" {
		t.Error("the copy turns an NSP into an NSZ")
	}
	for _, text := range []string{NET_PREPARE_ACTION, NOTE_NET_PREPARE_FAILED, NOTE_NET_PREPARE_UNWANTED, NOTE_NET_SEND_SPACE} {
		if _, ok := i18n.Translations["es"][text]; !ok {
			t.Errorf("no Spanish for %q", text)
		}
	}
	if netPrepareNote(errCompressSpace) != NOTE_NET_SEND_SPACE || netPrepareNote(errNetSendUnwanted) != NOTE_NET_PREPARE_UNWANTED || netPrepareNote(os.ErrInvalid) != NOTE_NET_PREPARE_FAILED {
		t.Error("the notes of a copy that failed")
	}
}
