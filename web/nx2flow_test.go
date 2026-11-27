package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/internal/ttx"
	"github.com/dtrunk90/switch-library-manager-web/internal/ttx/ttxfake"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

const nx2flowTestGame = "0400000000010000"

// nx2flowWeb is a library with a game of the Switch 2 as a NUP file of size bytes, with an icon,
// and a game with an NSP alone; bob, a user who may download, is signed in. It gives the NUP and
// a fake nx2-flow, whose card is a folder.
func nx2flowWeb(t *testing.T, size int) (*Web, *client, []byte, *ttxfake.Receiver) {
	t.Helper()
	web, admin := keysWeb(t)
	dir := t.TempDir()
	data := make([]byte, size)
	rand.New(rand.NewSource(int64(size))).Read(data)
	nup := "Made Up Quest [0400000000010000][v0].nup"
	os.WriteFile(filepath.Join(dir, nup), data, 0644)
	os.WriteFile(filepath.Join(dir, "Other [0100000000020000][v0].nsp"), []byte("an nsp"), 0644)
	// a square icon, as the titles database has
	os.MkdirAll(filepath.Join(web.dataFolder, "img"), 0755)
	icon := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for x := 0; x < 256; x++ {
		for y := 0; y < 256; y++ {
			icon.Set(x, y, color.NRGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var encoded bytes.Buffer
	png.Encode(&encoded, icon)
	os.WriteFile(filepath.Join(web.dataFolder, "img", "quest.png"), encoded.Bytes(), 0644)
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0400000000010": {BaseExist: true, Icon: "quest.png", File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: nup, BaseFolder: dir, Size: int64(size)},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: nx2flowTestGame}}},
		"0100000000020": {BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Other [0100000000020000][v0].nsp", BaseFolder: dir, Size: 6},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000020000"}}},
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})
	web.HandleNx2flow()
	bob := &client{t: t, handler: admin.handler}
	bob.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	receiver, err := ttxfake.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(receiver.Close)
	return web, bob, data, receiver
}

func nx2flowSend(c *client, address string) (int, string, int64) {
	body, _ := json.Marshal(map[string]any{"titleId": nx2flowTestGame, "address": address})
	answer := sendJSON(c, "POST", nx2flowSendPath, string(body))
	var started struct {
		Task int64 `json:"task"`
	}
	json.Unmarshal(answer.Body.Bytes(), &started)
	return answer.Code, answer.Body.String(), started.Task
}

// nx2flowEnded waits for the task to end and gives it.
func nx2flowEnded(t *testing.T, web *Web, id int64) tasklog.Task {
	t.Helper()
	end := time.Now().Add(20 * time.Second)
	for time.Now().Before(end) {
		for _, task := range web.taskLog().Snapshot() {
			if task.Id == id && task.Status != tasklog.TASK_RUNNING {
				// the send is forgotten once its task ended
				for time.Now().Before(end) && len(web.nx2flowRunning("bob")) > 0 {
					time.Sleep(5 * time.Millisecond)
				}
				return task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the task %d does not end", id)
	return tasklog.Task{}
}

// The NUP arrives whole, byte for byte, in the folder of the games, checked with the sum read back
// from the card; its cover arrives as a front of 500 by 813 named by the title ID; the address is
// remembered for the user and the send is noted in the activity.
func TestNx2flowSendsTheGameAndItsCover(t *testing.T) {
	web, bob, data, receiver := nx2flowWeb(t, 3<<20+17)
	code, body, id := nx2flowSend(bob, receiver.Address())
	if code != http.StatusAccepted || id == 0 {
		t.Fatalf("send: %d %s", code, body)
	}
	task := nx2flowEnded(t, web, id)
	if task.Status != tasklog.TASK_SUCCESS || task.Kind != TASK_NX2FLOW || task.Subject != "Made Up Quest" {
		t.Fatalf("the task: %+v %+v", task, task.Error)
	}
	card := receiver.Root
	written, err := os.ReadFile(filepath.Join(card, "nx2-flow", "games", "Made Up Quest [0400000000010000][v0].nup"))
	if err != nil || !bytes.Equal(written, data) {
		t.Fatalf("the game on the card: %d bytes, %v", len(written), err)
	}
	if receiver.Count("HASH") != 1 {
		t.Fatalf("checked on the card: %d", receiver.Count("HASH"))
	}
	cover, err := os.ReadFile(filepath.Join(card, "nx2-flow", "covers", nx2flowTestGame+".jpg"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(cover))
	if err != nil || config.Width != 500 || config.Height != 813 {
		t.Fatalf("the cover: %+v %v", config, err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(card, "nx2-flow", "*", "*"+ttx.PartSuffix))
	if len(leftovers) > 0 {
		t.Fatalf("part files stay: %v", leftovers)
	}
	game := bob.do("GET", nx2flowGamePath+nx2flowTestGame, nil)
	var answer struct {
		Files   []nx2flowFile `json:"files"`
		Address string        `json:"address"`
		Port    int           `json:"port"`
	}
	if json.Unmarshal(game.Body.Bytes(), &answer) != nil || answer.Address != receiver.Address() || answer.Port != 28302 || len(answer.Files) != 1 ||
		answer.Files[0].Size != int64(len(data)) {
		t.Fatalf("the game: %d %s", game.Code, game.Body.String())
	}
	noted := false
	for _, entry := range web.activities().List() {
		if entry.Action == activity.ACTION_NX2FLOW && entry.User == "bob" && entry.Detail == receiver.Address()+": Made Up Quest" {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("noted: %+v", web.activities().List())
	}
	// remembered across a restart of the app
	again := &Web{dataFolder: web.dataFolder}
	if again.nx2flowRemembered("bob") != receiver.Address() || again.nx2flowRemembered("alice") != "" {
		t.Fatal("the address is not kept")
	}
}

// The folder of the games set in the settings of nx2-flow is the one the game goes to.
func TestNx2flowGamesFolderOfItsSettings(t *testing.T) {
	web, bob, data, receiver := nx2flowWeb(t, 1000)
	os.MkdirAll(filepath.Join(receiver.Root, "nx2-flow"), 0755)
	os.WriteFile(filepath.Join(receiver.Root, "nx2-flow", "menu.cfg"), []byte("style=Showroom\r\ngames=/roms/nup/\r\n"), 0644)
	_, body, id := nx2flowSend(bob, receiver.Address())
	if task := nx2flowEnded(t, web, id); task.Status != tasklog.TASK_SUCCESS {
		t.Fatalf("%s: %+v", body, task.Error)
	}
	if written, err := os.ReadFile(filepath.Join(receiver.Root, "roms", "nup", "Made Up Quest [0400000000010000][v0].nup")); err != nil || !bytes.Equal(written, data) {
		t.Fatalf("the game in the folder of the settings: %v", err)
	}
	for settings, folder := range map[string]string{"": nx2flowGamesFolder, "games=sdmc:/a/b\n": "/a/b", "games=../x": nx2flowGamesFolder, " games = /g ": "/g", "gamesx=/y": nx2flowGamesFolder} {
		if got := nx2flowGamesFolderOf([]byte(settings)); got != folder {
			t.Errorf("%q: %q", settings, got)
		}
	}
}

// Cancelled halfway: the task says so, and the card keeps neither the game nor its part file.
func TestNx2flowCancelLeavesNoFile(t *testing.T) {
	web, bob, _, receiver := nx2flowWeb(t, 32<<20)
	reached, release := make(chan struct{}), make(chan struct{})
	once := false
	receiver.Set(func(r *ttxfake.Receiver) {
		r.OnData = func(path string, written int64) {
			if strings.HasSuffix(path, ".nup") && written >= 1<<20 && !once {
				once = true
				close(reached)
				select {
				case <-release:
				case <-time.After(10 * time.Second):
				}
			}
		}
	})
	code, body, id := nx2flowSend(bob, receiver.Address())
	if code != http.StatusAccepted {
		t.Fatalf("send: %d %s", code, body)
	}
	<-reached
	if other := sendJSON(bob, "POST", nx2flowCancelPath+"999", ""); other.Code != http.StatusNotFound {
		t.Fatalf("a send that is not going on: %d", other.Code)
	}
	if cancelled := sendJSON(bob, "POST", fmt.Sprintf("%s%d", nx2flowCancelPath, id), ""); cancelled.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", cancelled.Code, cancelled.Body.String())
	}
	close(release)
	task := nx2flowEnded(t, web, id)
	if task.Status != tasklog.TASK_FAILED || task.Error == nil || task.Error.Text != tasklog.NOTE_COMPRESS_CANCELED || task.Ending() != "cancelled" {
		t.Fatalf("the task: %+v %+v", task, task.Error)
	}
	if !receiver.Wait(5 * time.Second) {
		t.Fatal("the connection stays")
	}
	names, _ := filepath.Glob(filepath.Join(receiver.Root, "nx2-flow", "games", "*"))
	if len(names) > 0 {
		t.Fatalf("on the card: %v", names)
	}
	if receiver.Count("HASH") != 0 {
		t.Fatal("checked a file cut")
	}
}

// Another user does not cancel the send of bob; an administrator does.
func TestNx2flowCancelOnlyOwnSends(t *testing.T) {
	web := &Web{}
	cancelled := false
	web.nx2flow.running = map[int64]*nx2flowRun{7: {task: 7, user: "bob", cancel: func() { cancelled = true }}}
	if web.cancelNx2flowSend(7, "carol", false) || cancelled {
		t.Fatal("carol cancelled the send of bob")
	}
	if !web.cancelNx2flowSend(7, "alice", true) || !cancelled {
		t.Fatal("an administrator cancels it")
	}
}

// A file that does not arrive as it was sent fails the task, with what to do.
func TestNx2flowWrongSumFails(t *testing.T) {
	web, bob, _, receiver := nx2flowWeb(t, 5000)
	receiver.Set(func(r *ttxfake.Receiver) { r.WrongPutSum = true })
	_, _, id := nx2flowSend(bob, receiver.Address())
	if task := nx2flowEnded(t, web, id); task.Status != tasklog.TASK_FAILED || task.Error == nil || task.Error.Text != NOTE_NX2FLOW_CHECK {
		t.Fatalf("the task: %+v %+v", task, task.Error)
	}
}

// An address where nothing answers, one outside the local network, one that is not an address, a
// card without room: each refused at once with what to do, and no task.
func TestNx2flowRefusedAddresses(t *testing.T) {
	web, bob, _, receiver := nx2flowWeb(t, 1000)
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := listener.Addr().String()
	listener.Close()
	before := len(web.taskLog().Snapshot())
	for address, want := range map[string]struct {
		code    int
		message string
	}{
		closed:           {http.StatusBadGateway, string(errNx2flowReach)},
		"8.8.8.8":        {http.StatusBadRequest, string(errNx2flowNotLocal)},
		"":               {http.StatusBadRequest, string(errNx2flowAddress)},
		"192.168.1.5:x":  {http.StatusBadRequest, string(errNx2flowAddress)},
		"10.0.0.1:70000": {http.StatusBadRequest, string(errNx2flowAddress)},
	} {
		code, body, _ := nx2flowSend(bob, address)
		if code != want.code || !strings.Contains(body, want.message) {
			t.Errorf("%q: %d %s", address, code, body)
		}
	}
	receiver.Set(func(r *ttxfake.Receiver) { r.Free = 100 })
	if code, body, _ := nx2flowSend(bob, receiver.Address()); code != http.StatusInsufficientStorage || !strings.Contains(body, string(errNx2flowNoRoom)) {
		t.Errorf("no room: %d %s", code, body)
	}
	if after := len(web.taskLog().Snapshot()); after != before {
		t.Fatalf("tasks were started: %d, %d", before, after)
	}
	if web.nx2flowRemembered("bob") != "" {
		t.Fatal("an address refused is remembered")
	}
	if len(web.nx2flow.running) != 0 {
		t.Fatalf("sends held: %+v", web.nx2flow.running)
	}
}

// Only a game with a NUP file, and never in the demo.
func TestNx2flowOnlyNupAndNotInTheDemo(t *testing.T) {
	_, bob, _, receiver := nx2flowWeb(t, 1000)
	body, _ := json.Marshal(map[string]any{"titleId": "0100000000020000", "address": receiver.Address()})
	if answer := sendJSON(bob, "POST", nx2flowSendPath, string(body)); answer.Code != http.StatusNotFound || !strings.Contains(answer.Body.String(), string(errNx2flowNoFile)) {
		t.Fatalf("an NSP: %d %s", answer.Code, answer.Body.String())
	}
	t.Setenv("SLM_DEMO", "true")
	if code, body, _ := nx2flowSend(bob, receiver.Address()); code != http.StatusForbidden || !strings.Contains(body, DEMO_DISABLED) {
		t.Fatalf("demo: %d %s", code, body)
	}
	if receiver.Count("PING") != 0 {
		t.Fatal("the console was reached")
	}
}

// One send at a time to a console.
func TestNx2flowOneSendPerConsole(t *testing.T) {
	web, bob, _, receiver := nx2flowWeb(t, 8<<20)
	release := make(chan struct{})
	receiver.Set(func(r *ttxfake.Receiver) {
		r.OnData = func(path string, written int64) {
			select {
			case <-release:
			case <-time.After(10 * time.Second):
			}
		}
	})
	_, _, id := nx2flowSend(bob, receiver.Address())
	if code, body, _ := nx2flowSend(bob, receiver.Address()); code != http.StatusConflict || !strings.Contains(body, string(errNx2flowBusy)) {
		t.Errorf("a second send: %d %s", code, body)
	}
	close(release)
	if task := nx2flowEnded(t, web, id); task.Status != tasklog.TASK_SUCCESS {
		t.Fatalf("the first one: %+v", task.Error)
	}
}

func TestNx2flowAddress(t *testing.T) {
	for typed, want := range map[string][2]string{
		"192.168.1.50":        {"192.168.1.50:28302", "192.168.1.50"},
		" 192.168.1.50:9000 ": {"192.168.1.50:9000", "192.168.1.50:9000"},
		"10.1.2.3:28302":      {"10.1.2.3:28302", "10.1.2.3"},
		"[fe80::1]:28302":     {"[fe80::1]:28302", "[fe80::1]"},
		"fd00::5":             {"[fd00::5]:28302", "[fd00::5]"},
		"http://172.16.0.9/":  {"172.16.0.9:28302", "172.16.0.9"},
		"localhost":           {"", ""},
	} {
		dial, remembered, err := nx2flowAddress(context.Background(), typed)
		if typed == "localhost" {
			// a name of the local network, as the system resolves it
			if err != nil || !strings.HasSuffix(dial, ":28302") || remembered != "localhost" {
				t.Errorf("%q: %q %q %v", typed, dial, remembered, err)
			}
			continue
		}
		if err != nil || dial != want[0] || remembered != want[1] {
			t.Errorf("%q: %q %q %v", typed, dial, remembered, err)
		}
	}
	for _, typed := range []string{"1.1.1.1", "8.8.8.8:28302", "100.64.0.1"} {
		if _, _, err := nx2flowAddress(context.Background(), typed); !errors.Is(err, errNx2flowNotLocal) {
			t.Errorf("%q: %v", typed, err)
		}
	}
	for _, typed := range []string{"", "a b", "1.2.3.4:0", "user@10.0.0.1", "10.0.0.1/x"} {
		if _, _, err := nx2flowAddress(context.Background(), typed); !errors.Is(err, errNx2flowAddress) {
			t.Errorf("%q: %v", typed, err)
		}
	}
}

// A portrait cover fills the front; a square icon goes whole across it. Both are 500 by 813.
func TestNx2flowCoverJpeg(t *testing.T) {
	for _, size := range []image.Point{{256, 256}, {264, 374}, {1280, 720}} {
		picture := image.NewNRGBA(image.Rect(0, 0, size.X, size.Y))
		for x := 0; x < size.X; x++ {
			picture.Set(x, size.Y/2, color.NRGBA{255, 0, 0, 255})
		}
		var encoded bytes.Buffer
		png.Encode(&encoded, picture)
		cover, err := nx2flowCoverJpeg(encoded.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := jpeg.Decode(bytes.NewReader(cover))
		if err != nil || decoded.Bounds().Dx() != 500 || decoded.Bounds().Dy() != 813 {
			t.Fatalf("%v: %v %v", size, decoded.Bounds(), err)
		}
	}
	if _, err := nx2flowCoverJpeg([]byte("not an image")); err == nil {
		t.Fatal("not an image")
	}
}

func TestNx2flowCardName(t *testing.T) {
	if got := nx2flowCardName("A: Game? [0400000000010000][v0].NUP"); strings.ContainsAny(got, `:?/\`) || !strings.HasSuffix(got, ".nup") {
		t.Fatalf("%q", got)
	}
}

func TestNx2flowTextsAreTranslated(t *testing.T) {
	for _, text := range []string{NX2FLOW_ACTION, activity.ACTION_NX2FLOW, string(errNx2flowNotAllowed), string(errNx2flowNoFile), string(errNx2flowGone),
		string(errNx2flowAddress), string(errNx2flowNotLocal), string(errNx2flowReach), string(errNx2flowBusy), string(errNx2flowTooMany),
		string(errNx2flowNoRoom), string(errNx2flowNotFound), NOTE_NX2FLOW_FAILED, NOTE_NX2FLOW_CHECK, NOTE_NX2FLOW_NO_COVER, NOTE_NX2FLOW_COVER,
		nx2flowSending, nx2flowChecking, nx2flowCover} {
		if _, ok := i18n.Translations["es"][text]; !ok {
			t.Errorf("no Spanish for %q", text)
		}
	}
	if taskKindNames[TASK_NX2FLOW] != NX2FLOW_ACTION {
		t.Error("the kind of the task has no name")
	}
}

// A game nx2-flow keeps in parts on the card (FAT32, or split= in its menu.cfg) is checked part
// by part, not taken for a failure because the whole file is not there.
func TestNx2flowChecksTheParts(t *testing.T) {
	web, bob, data, receiver := nx2flowWeb(t, 3<<20+17)
	receiver.Set(func(r *ttxfake.Receiver) { r.SplitAt = 1 << 20 })
	code, body, id := nx2flowSend(bob, receiver.Address())
	if code != http.StatusAccepted || id == 0 {
		t.Fatalf("send: %d %s", code, body)
	}
	task := nx2flowEnded(t, web, id)
	if task.Status != tasklog.TASK_SUCCESS {
		t.Fatalf("the task: %+v %+v", task, task.Error)
	}
	whole := filepath.Join(receiver.Root, "nx2-flow", "games", "Made Up Quest [0400000000010000][v0].nup")
	var joined []byte
	for number := 1; number <= 4; number++ {
		part, err := os.ReadFile(fmt.Sprintf("%s.%03d", whole, number))
		if err != nil {
			t.Fatal(err)
		}
		joined = append(joined, part...)
	}
	if !bytes.Equal(joined, data) {
		t.Fatal("the parts are not the game")
	}
	// the whole file, then each of the four parts
	if receiver.Count("HASH") != 5 {
		t.Fatalf("checked on the card: %d", receiver.Count("HASH"))
	}
}
