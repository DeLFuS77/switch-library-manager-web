package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
)

const mb = 1048576

// The pieces DBI asks for are put together: one asked twice, out of order or touching another
// counts once.
func TestNetSpansJoin(t *testing.T) {
	spans := []netSpan{}
	for _, piece := range []netSpan{{0, 10}, {20, 30}, {5, 12}, {20, 30}, {12, 20}, {40, 50}, {35, 40}} {
		spans = addNetSpan(spans, piece)
	}
	if len(spans) != 2 || spans[0] != (netSpan{0, 30}) || spans[1] != (netSpan{35, 50}) || netSpansSize(spans) != 45 {
		t.Fatalf("pieces: %v", spans)
	}
	if got := addNetSpan(spans, netSpan{7, 7}); len(got) != 2 {
		t.Fatalf("an empty piece changes nothing: %v", got)
	}
}

func TestNetRangeStart(t *testing.T) {
	for header, want := range map[string]int64{"": 0, "bytes=0-": 0, "bytes=1048576-2097151": 1048576, "bytes=500-": 500, "bytes=-100": 900} {
		if got, ok := netRangeStart(header, 1000); !ok || got != want {
			t.Errorf("%q: %v %v, want %v", header, got, ok, want)
		}
	}
	for _, header := range []string{"bytes=0-10,20-30", "items=0-", "bytes=x-"} {
		if _, ok := netRangeStart(header, 1000); ok {
			t.Errorf("%q is not counted", header)
		}
	}
}

// DBI asks for a file in pieces, by console and file: what it has, its speed now and on average,
// and the file ends when it has it whole. A piece asked again is written twice, not counted twice.
func TestNetSendsGroupsTheRangesOfAConsole(t *testing.T) {
	sends := &netSends{}
	game := netFile{id: "a1", name: "Game [0100000000010000][v0].nsp", game: "Game", kind: netFolderGames, size: 100 * mb, cover: "game.jpg"}
	other := netFile{id: "b2", name: "Other.nsp", game: "Other", kind: netFolderUpdates, version: 65536, size: 10 * mb}
	start := time.Date(2026, 11, 15, 18, 0, 0, 0, time.UTC)
	// 10 MB a second, in pieces of 10 MB: the third piece asked twice, the last before the others
	pieces := []int64{0, 10, 20, 20, 90, 30, 40, 50, 60, 70}
	for i, from := range pieces {
		sends.served("key1", "Living room", "ana", NET_VIA_DBI, game, from*mb, 10*mb, start.Add(time.Duration(i+1)*time.Second))
	}
	// another console asks for the same file: a run of its own
	sends.served("key2", "Lite", "ana", NET_VIA_TINFOIL, game, 0, 5*mb, start.Add(2*time.Second))
	now := start.Add(10 * time.Second)
	live, _ := sends.views(now)
	if len(live) != 2 {
		t.Fatalf("one run by console: %+v", live)
	}
	run := live[0]
	if run.Id != "key1" || run.Via != NET_VIA_DBI || len(run.Files) != 1 {
		t.Fatalf("the run of the first console: %+v", run)
	}
	file := run.Files[0]
	if file.Done != 90*mb || file.State != "running" || file.Percent() != 90 || file.Cover != "/t/game.jpg" || file.Kind != "game" {
		t.Fatalf("90 MB of 100 sent, the piece asked twice once: %+v", file)
	}
	if file.Speed < 9 || file.Speed > 11 || file.Average < 9.9 || file.Average > 10.1 || file.Left < 1 || file.Left > 2 {
		t.Fatalf("speed now, average and time left: %+v", file)
	}
	// the last piece: sent whole
	sends.served("key1", "Living room", "ana", NET_VIA_DBI, game, 80*mb, 10*mb, start.Add(11*time.Second))
	sends.served("key1", "Living room", "ana", NET_VIA_DBI, other, 0, 4*mb, start.Add(12*time.Second))
	live, _ = sends.views(start.Add(12 * time.Second))
	if f := live[0].Files[0]; f.State != tasklog.SENT_DONE || f.Done != 100*mb || f.Speed != 0 {
		t.Fatalf("the file sent whole: %+v", f)
	}
	if f := live[0].Files[1]; f.Kind != "update" || f.Label != "v65536" || f.State != "running" {
		t.Fatalf("the update asked next: %+v", f)
	}
	if live[0].Size != 110*mb || live[0].Done != 104*mb || live[0].Number() != 2 {
		t.Fatalf("the total of the run: %+v", live[0])
	}
}

// A file the console stops asking for is cut after a minute, and the run, with nothing left to
// send, ends a minute after its last request: its files, their averages and how they ended go to
// the history of the tasks. A file asked for again before it goes on.
func TestNetSendsCutAndEnd(t *testing.T) {
	sends := &netSends{}
	game := netFile{id: "a1", game: "Game", kind: netFolderGames, size: 20 * mb}
	dlc := netFile{id: "c3", game: "Game", kind: netFolderDlc, dlcName: "Extra", size: 40 * mb}
	start := time.Date(2026, 11, 15, 18, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		sends.served("key1", "Living room", "ana", NET_VIA_DBI, game, int64(i)*5*mb, 5*mb, start.Add(time.Duration(i+1)*time.Second))
	}
	sends.served("key1", "Living room", "ana", NET_VIA_DBI, dlc, 0, 10*mb, start.Add(5*time.Second))
	sends.served("key1", "Living room", "ana", NET_VIA_DBI, dlc, 10*mb, 10*mb, start.Add(9*time.Second))

	// 30 seconds later: nothing cut yet
	if ended, going := sends.sweep(start.Add(39 * time.Second)); len(ended) != 0 || !going {
		t.Fatalf("too soon to cut: %v %v", ended, going)
	}
	// asked again: it goes on
	sends.served("key1", "Living room", "ana", NET_VIA_DBI, dlc, 20*mb, 10*mb, start.Add(40*time.Second))
	if ended, _ := sends.sweep(start.Add(90 * time.Second)); len(ended) != 0 {
		t.Fatalf("it went on 50 seconds ago: %v", ended)
	}
	// cut a minute after its last piece, and the run, with nothing left, ends with it
	live, _ := sends.views(start.Add(100 * time.Second))
	if live[0].Files[1].State != "running" || live[0].Files[1].Speed != 0 {
		t.Fatalf("still going, at no speed: %+v", live[0].Files[1])
	}
	ended, going := sends.sweep(start.Add(101 * time.Second))
	if len(ended) != 1 || going {
		t.Fatalf("the run ended: %v %v", ended, going)
	}
	if view := ended[0].view(ended[0].ended); view.Cut != 1 || view.Ok != 1 {
		t.Fatalf("the DLC is cut: %+v", view)
	}
	task, ok := netSendTask(ended[0])
	if !ok || task.Kind != tasklog.TASK_NET_INSTALL || task.Status != tasklog.TASK_WARNING || task.Console != "Living room" || task.Via != NET_VIA_DBI {
		t.Fatalf("the task kept: %+v", task)
	}
	if !task.Finished.Equal(start.Add(40*time.Second)) || task.SentCount(tasklog.SENT_DONE) != 1 || task.SentCount(tasklog.SENT_CUT) != 1 {
		t.Fatalf("ends with its last request, one sent and one cut: %+v", task)
	}
	sent, cut := task.Sent[0], task.Sent[1]
	// 20 MB in the 3 seconds from its first piece to its last; 30 MB of 40 in 35 seconds
	if sent.Done != 20*mb || sent.Seconds != 3 || sent.Average() < 6.6 || sent.Average() > 6.7 || sent.Took() != "3s" {
		t.Fatalf("the game: %+v %v", sent, sent.Average())
	}
	if cut.Done != 30*mb || cut.Seconds != 35 || cut.Kind != "dlc" || cut.Label != "Extra" || cut.State != tasklog.SENT_CUT {
		t.Fatalf("the DLC: %+v", cut)
	}
	if average := task.SentAverage(); average < 1.31 || average > 1.33 {
		t.Fatalf("50 MB in 38 seconds: %v", average)
	}
	if width := tasklog.SpeedWidth(cut.Average(), task.SentFastest()); width < 12 || width > 14 {
		t.Fatalf("the bar of the DLC against the game: %v", width)
	}
	// Settings shows it for half an hour
	if _, recent := sends.views(start.Add(101 * time.Second)); len(recent) != 1 || recent[0].Running {
		t.Fatalf("the run that ended: %+v", recent)
	}
	sends.sweep(start.Add(40*time.Second + 31*time.Minute))
	if _, recent := sends.views(start.Add(32 * time.Minute)); len(recent) != 0 {
		t.Fatalf("gone after half an hour: %+v", recent)
	}
}

// NXDock tells its queue: the run shows its jobs, their stage and what waits, and ends when the
// console says it ended. A job cancelled on the console is grey, not a failure.
func TestNetSendsQueueOfNxdock(t *testing.T) {
	sends := &netSends{}
	start := time.Date(2026, 11, 15, 18, 0, 0, 0, time.UTC)
	game := netFile{id: "a1", game: "Game", kind: netFolderGames, size: 20 * mb}
	// it downloaded before it told its queue: the queue replaces what was counted
	sends.served("key1", "Lite", "ana", NET_VIA_TINFOIL, game, 0, 5*mb, start)
	cover := func(name string) string { return "/t/" + strings.ToLower(name) + ".jpg" }
	queue := func(at time.Duration, jobs ...netQueueJob) {
		sends.queue("key1", "Lite", "ana", netQueueLive{Jobs: jobs}, cover, start.Add(at))
	}
	queue(time.Second, netQueueJob{Id: 1, Game: "Game", State: "running", Stage: "network", Percent: 50, Speed: 30, Left: 20, Size: 20 * mb},
		netQueueJob{Id: 2, Game: "Other", State: "waiting", Size: 60 * mb})
	live, _ := sends.views(start.Add(time.Second))
	run := live[0]
	if run.Via != NET_VIA_NXDOCK || len(run.Files) != 2 || run.Files[0].Stage != "network" || run.Files[0].Tone() != "cyan" || run.Files[0].Done != 10*mb {
		t.Fatalf("the queue of NXDock: %+v", run)
	}
	// its time left, and what waits at its speed
	if run.Files[0].Left != 20 || run.Left != 22 || run.Files[1].Cover != "/t/other.jpg" || run.Current().Job != 1 {
		t.Fatalf("time left: %+v", run)
	}
	queue(11*time.Second, netQueueJob{Id: 1, Game: "Game", State: "done", Size: 20 * mb},
		netQueueJob{Id: 2, Game: "Other", State: "running", Stage: "decompress", Percent: 10, Size: 60 * mb})
	queue(31*time.Second, netQueueJob{Id: 1, Game: "Game", State: "done", Size: 20 * mb},
		netQueueJob{Id: 2, Game: "Other", State: "cancelled", Size: 60 * mb})
	// the bytes it is still served are not counted again
	sends.served("key1", "Lite", "ana", NET_VIA_TINFOIL, game, 5*mb, 5*mb, start.Add(32*time.Second))
	run_ := sends.end("key1", start.Add(33*time.Second))
	if run_ == nil {
		t.Fatal("the console says its queue ended")
	}
	task, ok := netSendTask(run_)
	if !ok || task.Status != tasklog.TASK_SUCCESS || task.Via != NET_VIA_NXDOCK || task.SentCount(tasklog.SENT_DONE) != 1 || task.SentCount(tasklog.SENT_CANCELLED) != 1 {
		t.Fatalf("one installed and one cancelled: %+v", task)
	}
	if task.Sent[0].Seconds != 10 || task.Sent[0].Average() != 2 {
		t.Fatalf("20 MB in the 10 seconds between its start and its end: %+v", task.Sent[0])
	}
	if task.Ending() != "" {
		t.Fatalf("no failure: %v", task.Ending())
	}

	// all cancelled: grey; all failed: a failure
	sends.queue("key2", "Lite", "ana", netQueueLive{Jobs: []netQueueJob{{Id: 1, Game: "A", State: "running"}}}, nil, start)
	sends.queue("key2", "Lite", "ana", netQueueLive{Jobs: []netQueueJob{{Id: 1, Game: "A", State: "cancelled"}}}, nil, start.Add(time.Second))
	cancelled, _ := netSendTask(sends.end("key2", start.Add(time.Second)))
	if cancelled.Ending() != tasklog.ENDING_CANCELLED {
		t.Fatalf("cancelled on the console: %+v", cancelled)
	}
	sends.queue("key3", "Lite", "ana", netQueueLive{Jobs: []netQueueJob{{Id: 1, Game: "A", State: "running"}}}, nil, start)
	sends.queue("key3", "Lite", "ana", netQueueLive{Jobs: []netQueueJob{{Id: 1, Game: "A", State: "failed", Error: "No room"}}}, nil, start.Add(time.Second))
	failed, _ := netSendTask(sends.end("key3", start.Add(time.Second)))
	if failed.Status != tasklog.TASK_FAILED || failed.Ending() != "" || failed.Sent[0].Error != "No room" {
		t.Fatalf("failed on the console: %+v", failed)
	}

	// a queue not heard of for a minute: what it did is cut
	sends.queue("key4", "Lite", "ana", netQueueLive{Jobs: []netQueueJob{{Id: 1, Game: "A", State: "running", Percent: 30, Size: 10 * mb}}}, nil, start)
	if live, _ := sends.views(start.Add(25 * time.Second)); !live[0].Gone {
		t.Fatalf("not answering: %+v", live)
	}
	ended, _ := sends.sweep(start.Add(61 * time.Second))
	if len(ended) != 1 || ended[0].files[0].state != tasklog.SENT_CUT {
		t.Fatalf("cut when not heard of: %+v", ended)
	}
}

// What the app serves is counted as it is written, from where the range asked starts.
func TestNetSendWriterCountsWhatIsServed(t *testing.T) {
	web := newTestWeb(t)
	content := bytes.Repeat([]byte("x"), 3*mb)
	file := netFile{id: "a1", game: "Game", kind: netFolderGames, size: int64(len(content))}
	serve := func(rangeHeader string) {
		request := httptest.NewRequest(http.MethodGet, netInstallDbi+"Games/Game.nsp", nil)
		if rangeHeader != "" {
			request.Header.Set("Range", rangeHeader)
		}
		recorder := httptest.NewRecorder()
		http.ServeContent(web.netSendCounted(recorder, request, file), request, "", time.Time{}, bytes.NewReader(content))
	}
	serve("bytes=1048576-2097151")
	serve("bytes=2097152-")
	serve("bytes=1048576-2097151")
	live, _ := web.netSends.views(time.Now())
	if len(live) != 1 || live[0].Via != NET_VIA_DBI || live[0].Files[0].Done != 2*mb || live[0].Files[0].State != "running" {
		t.Fatalf("2 MB of 3 served: %+v", live)
	}
	serve("")
	live, _ = web.netSends.views(time.Now())
	if live[0].Files[0].State != tasklog.SENT_DONE {
		t.Fatalf("served whole: %+v", live[0].Files[0])
	}
	if web.work(false).Tasks == 0 {
		t.Fatal("an update waits for a console that is installing")
	}
	// a request for several pieces and a HEAD are not counted
	head := httptest.NewRequest(http.MethodHead, netInstallDbi+"Games/Game.nsp", nil)
	if writer := web.netSendCounted(httptest.NewRecorder(), head, file); writer != nil {
		if _, counted := writer.(*netSendWriter); counted {
			t.Fatal("a HEAD is not counted")
		}
	}
}

// The Tasks page shows a run going on as a task, and the run that ended in the history with what
// each file got; the list of the queue of tasks on files does not change.
func TestNetSendsOnTheTasksPage(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleTasks()
	start := time.Now().Add(-time.Minute)
	game := netFile{id: "a1", game: "Forest Adventure", kind: netFolderGames, size: 100 * mb}
	web.netSends.served("key1", "Living room", "", NET_VIA_DBI, game, 0, 40*mb, start)
	web.netSends.served("key1", "Living room", "", NET_VIA_DBI, game, 40*mb, 20*mb, start.Add(30*time.Second))
	web.netSends.queue("key2", "Lite", "", netQueueLive{Jobs: []netQueueJob{{Id: 7, Game: "Star Puzzle", State: "running", Stage: "decompress", Percent: 40, Size: 10 * mb},
		{Id: 8, Game: "Happy Farm", Label: "Season", State: "waiting", Size: mb}}}, nil, time.Now())

	ended := &netSendRun{id: "key3", console: "Bedroom", via: NET_VIA_DBI, started: start, last: start.Add(20 * time.Second), ended: start.Add(20 * time.Second),
		files: []*netSendFile{{id: "x", name: "Racing", kind: "update", label: "v131072", size: 20 * mb, done: 20 * mb, state: tasklog.SENT_DONE, started: start, last: start.Add(10 * time.Second), ended: start.Add(10 * time.Second)},
			{id: "y", name: "Racing", kind: "game", size: 50 * mb, done: 5 * mb, state: tasklog.SENT_CUT, started: start.Add(10 * time.Second), last: start.Add(20 * time.Second), ended: start.Add(20 * time.Second)}}}
	web.netSendEnded([]*netSendRun{ended})

	part := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/tasks.html?part=list", nil)
	request.Header.Set("Accept-Language", "es")
	web.router.ServeHTTP(part, request)
	body := part.Body.String()
	for _, want := range []string{"Instalar por red", "Living room", "data-task-id=\"net-key1\"", "Forest Adventure", "60%", "Star Puzzle", "Descomprimiendo",
		"Happy Farm · Season", "Bedroom", "Completada con avisos", "1 enviados enteros, 1 cortados", "Actualización v131072", "2,0 MB/s de media", "Cortado", "Enviado"} {
		if !strings.Contains(body, want) {
			t.Errorf("the list lacks %q", want)
		}
	}
	// pause and cancel only for the console of who looks: login is off in the test
	if strings.Contains(body, "data-net-ask") {
		t.Error("pause and cancel only on a console of who looks")
	}
	if t.Failed() {
		t.Log(body)
	}
	if len(web.queue().list()) != 0 || web.taskLog().RunningCount() != 0 {
		t.Fatal("it is no task of the queue")
	}
}
