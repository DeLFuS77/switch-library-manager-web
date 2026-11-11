package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/notify"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
)

// The install queue of NXDock seen from the web (22A), and the message when it ends (21A).
//
//   - While its queue works, NXDock sends what it does every few seconds with POST
//     /install/queue: each job with its stage, speed and time left. The answer carries what the
//     web asked of it since (pause, go on, cancel a job). NXDock always asks on its screen before
//     cancelling what the web asked to cancel, as with Send to console.
//   - When the queue ends, POST /install/queue/done tells what was installed and what failed, and
//     the app sends it through the channels of the notifications, in its language, unless that is
//     turned off in Settings (or on the console).
//   - The web reads the queues of the user's consoles with GET /api/install/queue, and asks with
//     POST /api/install/queue/{id} {"action": "pause" | "resume" | "cancel", "job": n}.
//
// What the consoles send is kept in memory only: a queue not heard of for a while is shown as
// gone.

const (
	netInstallQueue     = NET_INSTALL_PREFIX + "queue"
	netInstallQueueDone = NET_INSTALL_PREFIX + "queue/done"
	netQueueRead        = "/api/install/queue"
	// a queue not heard of for longer is no longer shown as working
	netQueueStale = 20 * time.Second
	// what the web may ask of a console at once, and the jobs and lines kept
	netQueueCommandsMax = 20
	netQueueJobsMax     = 200
	netQueueLinesMax    = 20
)

// netQueueJob is a job of the queue of a console, as NXDock tells it.
type netQueueJob struct {
	Id    int    `json:"id"`
	Game  string `json:"game"`
	Label string `json:"label,omitempty"`
	// waiting, running, done, failed, cancelled
	State string `json:"state"`
	// what the running job does: copy (the saves first), network, decompress, write, check,
	// register
	Stage   string  `json:"stage,omitempty"`
	Percent float64 `json:"percent,omitempty"`
	Speed   float64 `json:"speed,omitempty"` // MB/s
	Left    int     `json:"left,omitempty"`  // seconds, 0 when not known
	Size    int64   `json:"size,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// netQueueLive is what a console says its queue does.
type netQueueLive struct {
	Paused  bool          `json:"paused"`
	Jobs    []netQueueJob `json:"jobs"`
	Updated time.Time     `json:"updated"`
	// filled for the web: the console, and whether it was heard of lately
	Id      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Gone    bool   `json:"gone,omitempty"`
	Waiting int    `json:"asked,omitempty"` // what the web asked that the console has not taken yet
}

// netQueueCommand is what the web asks of a console.
type netQueueCommand struct {
	Id     string `json:"id"`
	Action string `json:"action"`
	Job    int    `json:"job,omitempty"`
	Game   string `json:"game,omitempty"`
	By     string `json:"by,omitempty"`
}

// netQueues keeps the queues of the consoles and what the web asked of them, by the id of the key
// of each console.
type netQueues struct {
	mutex    sync.Mutex
	live     map[string]netQueueLive
	commands map[string][]netQueueCommand
}

func (q *netQueues) put(keyId string, live netQueueLive) []netQueueCommand {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if q.live == nil {
		q.live = map[string]netQueueLive{}
		q.commands = map[string][]netQueueCommand{}
	}
	q.live[keyId] = live
	taken := q.commands[keyId]
	delete(q.commands, keyId)
	if taken == nil {
		taken = []netQueueCommand{}
	}
	return taken
}

func (q *netQueues) get(keyId string) (netQueueLive, int, bool) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	live, ok := q.live[keyId]
	return live, len(q.commands[keyId]), ok
}

func (q *netQueues) ask(keyId string, command netQueueCommand) bool {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if q.commands == nil {
		q.live = map[string]netQueueLive{}
		q.commands = map[string][]netQueueCommand{}
	}
	if len(q.commands[keyId]) >= netQueueCommandsMax {
		return false
	}
	q.commands[keyId] = append(q.commands[keyId], command)
	return true
}

var netQueueStates = map[string]bool{"waiting": true, "running": true, "done": true, "failed": true, "cancelled": true}
var netQueueStages = map[string]bool{"": true, "copy": true, "network": true, "decompress": true, "write": true, "check": true, "register": true}

// cleanNetQueue keeps what makes sense of what a console sent.
func cleanNetQueue(live netQueueLive) netQueueLive {
	jobs := []netQueueJob{}
	for _, job := range live.Jobs {
		if !netQueueStates[job.State] || !netQueueStages[job.Stage] || len(jobs) >= netQueueJobsMax {
			continue
		}
		job.Game = netInstallMessage(job.Game)
		job.Label = netInstallMessage(job.Label)
		job.Error = netInstallMessage(job.Error)
		job.Percent = min(max(job.Percent, 0), 100)
		job.Speed = max(job.Speed, 0)
		job.Left = max(job.Left, 0)
		jobs = append(jobs, job)
	}
	return netQueueLive{Paused: live.Paused, Jobs: jobs}
}

// handleNetQueue keeps what the queue of a console does, and gives what the web asked of it.
func (web *Web) handleNetQueue(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netConsoleKey(w, r)
	if !ok {
		return
	}
	var body netQueueLive
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		http.Error(w, "the queue of the console, as JSON", http.StatusBadRequest)
		return
	}
	live := cleanNetQueue(body)
	live.Updated = time.Now()
	commands := web.netQueues.put(key.KeyID, live)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"commands": commands})
}

// netQueueDone is what a console says when its queue ends.
type netQueueDone struct {
	Items []struct {
		Game  string `json:"game"`
		Label string `json:"label"`
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
	} `json:"items"`
}

// handleNetQueueDone sends the message of a queue that ended, through the channels of the
// notifications (21A).
func (web *Web) handleNetQueueDone(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netConsoleKey(w, r)
	if !ok {
		return
	}
	var body netQueueDone
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		http.Error(w, "what the queue installed, as JSON", http.StatusBadRequest)
		return
	}
	sent := false
	appSettings := settings.ReadSettings(web.dataFolder)
	options := appSettings.Notifications
	if len(body.Items) > 0 && notify.Configured(options) && !options.ConsoleQueueQuiet && !isDemoMode() {
		lang := web.notificationLanguage(appSettings.Language)
		title, text, items := netQueueDoneText(lang, key.KeyName, body)
		if err := notify.Send(options, title, text, items); err != nil {
			web.sugarLogger.Warnf("Failed to send the notification of the queue of a console: %v", err)
		} else {
			sent = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": sent})
}

// netQueueDoneText is the message of a queue that ended: how many went well and how many failed,
// then each, the failed first.
func netQueueDoneText(lang string, console string, body netQueueDone) (string, string, []any) {
	ok, failed := 0, 0
	good, bad := []string{}, []string{}
	items := []any{}
	for _, item := range body.Items {
		name := netInstallMessage(item.Game)
		if label := netInstallMessage(item.Label); label != "" {
			name += " · " + label
		}
		if item.Ok {
			ok++
			good = append(good, "✓ "+name)
		} else {
			failed++
			line := "✗ " + name
			if reason := netInstallMessage(item.Error); reason != "" {
				line += ": " + reason
			}
			bad = append(bad, line)
		}
		items = append(items, map[string]any{"game": name, "ok": item.Ok})
	}
	title := i18n.Translatef(lang, "NXDock finished installing on %v", netInstallMessage(console))
	lines := append([]string{i18n.Translatef(lang, "%v installed, %v failed", ok, failed)}, append(bad, good...)...)
	if len(lines) > netQueueLinesMax+1 {
		more := len(lines) - netQueueLinesMax - 1
		lines = append(lines[:netQueueLinesMax+1], i18n.Translatef(lang, "and %v more", more))
	}
	return title, strings.Join(lines, "\n"), items
}

// handleNetQueueRead gives the queues of the user's consoles, for the web.
func (web *Web) handleNetQueueRead(w http.ResponseWriter, r *http.Request) {
	queues := []netQueueLive{}
	if principal, ok := netPairer(r); ok {
		for _, key := range web.netUserConsoles(principal.Name) {
			live, asked, ok := web.netQueues.get(key.ID)
			if !ok {
				continue
			}
			live.Id, live.Name, live.Waiting = key.ID, key.Name, asked
			live.Gone = time.Since(live.Updated) > netQueueStale
			queues = append(queues, live)
		}
	}
	sort.SliceStable(queues, func(i, j int) bool { return strings.ToLower(queues[i].Name) < strings.ToLower(queues[j].Name) })
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"queues": queues})
}

// handleNetQueueAsk asks something of the queue of a console of the user: it takes it the next
// time it says what it does, and asks on its screen before cancelling.
func (web *Web) handleNetQueueAsk(w http.ResponseWriter, r *http.Request) {
	principal, ok := netPairer(r)
	if !ok {
		http.Error(w, "only a user who may download asks a console", http.StatusForbidden)
		return
	}
	key, ok := web.netUserConsole(principal.Name, mux.Vars(r)["id"])
	if !ok {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Action string `json:"action"`
		Job    int    `json:"job"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil ||
		(body.Action != "pause" && body.Action != "resume" && body.Action != "cancel") || (body.Action == "cancel" && body.Job <= 0) {
		http.Error(w, `{"action": "pause" | "resume" | "cancel", "job": n}`, http.StatusBadRequest)
		return
	}
	command := netQueueCommand{Action: body.Action, Job: body.Job, By: principal.Name}
	if body.Action == "cancel" {
		if live, _, ok := web.netQueues.get(key.ID); ok {
			for _, job := range live.Jobs {
				if job.Id == body.Job {
					command.Game = job.Game
				}
			}
		}
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		http.Error(w, "no random id", http.StatusInternalServerError)
		return
	}
	command.Id = hex.EncodeToString(id)
	if !web.netQueues.ask(key.ID, command) {
		http.Error(w, fmt.Sprintf("the console has %v things asked already: open NXDock on it first", netQueueCommandsMax), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": command.Id})
}

func (web *Web) handleNetQueuePaths() {
	web.router.HandleFunc(netInstallQueue, web.handleNetQueue).Methods(http.MethodPost)
	web.router.HandleFunc(netInstallQueueDone, web.handleNetQueueDone).Methods(http.MethodPost)
	web.router.HandleFunc(netQueueRead, web.handleNetQueueRead).Methods(http.MethodGet)
	web.router.HandleFunc(netQueueRead+"/{id:[0-9a-f]{16}}", web.handleNetQueueAsk).Methods(http.MethodPost)
}
