package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
)

// Sending a game to a console from the web, and what is installed on the console.
//
//   - A user who may download chooses, on the page of a game, one of their consoles and the files
//     to send: POST /api/install/push. The files wait in memory for that console, 24 hours at most.
//   - NXDock waits for them with GET /install/push/wait (a long poll), asks the user of the
//     console whether to install, and tells the answer with POST /install/push/answer, which is
//     noted in the activity.
//   - When the user of the console agrees, NXDock sends what is installed on it with POST
//     /install/inventory: the app keeps it in the data folder, one file per console, and shows it
//     on the page of each game and with the consoles.
//
// The console signs in with its key, as for the rest of /install/, behind the same gate: turned
// on, with users, and only on the local network unless allowed.

const (
	netInstallPushWait   = NET_INSTALL_PREFIX + "push/wait"
	netInstallPushAnswer = NET_INSTALL_PREFIX + "push/answer"
	netInstallInventory  = NET_INSTALL_PREFIX + "inventory"
	// the paths of the web
	netInstallPushSend       = "/api/install/push"
	netInstallConsoles       = "/api/install/consoles"
	netInstallGame           = "/api/install/game/"
	netInventoryForgetPrefix = "/api/install/inventory/"

	// installs waiting for a console at most, and how long they wait
	netPushMax  = 50
	netPushLife = 24 * time.Hour
	// how long a console waits for an install by default, and at most, in seconds
	netPushWaitDefault = 25
	netPushWaitMax     = 55
	// the titles kept of what is installed on a console at most
	netInventoryMax = 5000
	// the folder of the data folder with what is installed on each console
	netInventoryFolder = "consoles"
)

var (
	netTitleIdPattern = regexp.MustCompile(`^[0-9A-F]{16}$`)
	netKeyIdPattern   = regexp.MustCompile(`^[0-9a-f]{16}$`)
	netPushIdPattern  = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

var (
	errNetPushConsole  = errors.New("the console was not found")
	errNetPushNoFiles  = errors.New("choose at least one file to send")
	errNetPushGone     = errors.New("a file to send is no longer in the library")
	errNetPushFull     = errors.New("the console already has 50 installs waiting: open NXDock on it first")
	errNetPushNotAllow = errors.New("only a user who may download sends to a console")
)

// netPushOffMessage tells a user who sends to a console that the network install is off.
const netPushOffMessage = "Install over the network is off: an administrator has to turn it on before the console can install."

// netPushItem is a file sent to a console, as the console gets it.
type netPushItem struct {
	Id      string `json:"id"`
	FileId  string `json:"fileId"`
	Url     string `json:"url"`
	Game    string `json:"game"`
	TitleId string `json:"titleId"`
	Kind    string `json:"kind"`
	Version int    `json:"version"`
	Size    int64  `json:"size"`
	Format  string `json:"format"`
	Name    string `json:"name"`
	From    string `json:"from"`
}

// netPush is a file sent to a console that waits for it, or that it got and has not answered.
type netPush struct {
	id, keyId, keyName, owner, fileId, game string
	made                                    time.Time
	// as it was given to the console
	item netPushItem
}

// netPushQueue holds the files sent to each console, by the id of its key.
type netPushQueue struct {
	mutex sync.Mutex
	// waiting, oldest first
	pending map[string][]*netPush
	// given to the console, until it answers, by the id of the push
	delivered map[string]*netPush
	// closed when a file is sent to a console, so the request that waits for it answers
	signal map[string]chan struct{}
}

// forgetOld drops what waited too long. With the mutex held.
func (q *netPushQueue) forgetOld(now time.Time) {
	for key, list := range q.pending {
		kept := list[:0]
		for _, push := range list {
			if now.Sub(push.made) < netPushLife {
				kept = append(kept, push)
			}
		}
		if len(kept) == 0 {
			delete(q.pending, key)
		} else {
			q.pending[key] = kept
		}
	}
	for id, push := range q.delivered {
		if now.Sub(push.made) >= netPushLife {
			delete(q.delivered, id)
		}
	}
}

// add sends files to a console: all of them or, with too many waiting, none.
func (q *netPushQueue) add(pushes []*netPush) error {
	if len(pushes) == 0 {
		return nil
	}
	key := pushes[0].keyId
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if q.pending == nil {
		q.pending, q.delivered, q.signal = map[string][]*netPush{}, map[string]*netPush{}, map[string]chan struct{}{}
	}
	q.forgetOld(time.Now())
	if len(q.pending[key])+len(pushes) > netPushMax {
		return errNetPushFull
	}
	q.pending[key] = append(q.pending[key], pushes...)
	if signal, ok := q.signal[key]; ok {
		close(signal)
		delete(q.signal, key)
	}
	return nil
}

// take gives what waits for a console, which is then given; with nothing waiting, what tells
// when something comes.
func (q *netPushQueue) take(key string) ([]*netPush, <-chan struct{}) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if q.pending == nil {
		q.pending, q.delivered, q.signal = map[string][]*netPush{}, map[string]*netPush{}, map[string]chan struct{}{}
	}
	q.forgetOld(time.Now())
	if list := q.pending[key]; len(list) > 0 {
		delete(q.pending, key)
		return list, nil
	}
	signal, ok := q.signal[key]
	if !ok {
		signal = make(chan struct{})
		q.signal[key] = signal
	}
	return nil, signal
}

// given keeps a push given to the console until it answers.
func (q *netPushQueue) given(push *netPush) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if len(q.delivered) > 20*netPushMax {
		// a console that never answers does not fill the memory
		oldest := ""
		for id, other := range q.delivered {
			if oldest == "" || other.made.Before(q.delivered[oldest].made) {
				oldest = id
			}
		}
		delete(q.delivered, oldest)
	}
	q.delivered[push.id] = push
}

// answered takes the push a console answers, only one given to that console.
func (q *netPushQueue) answered(key string, id string) (*netPush, bool) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	push, ok := q.delivered[id]
	if !ok || push.keyId != key {
		return nil, false
	}
	delete(q.delivered, id)
	return push, true
}

// waiting counts what waits for a console.
func (q *netPushQueue) waiting(key string) int {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	q.forgetOld(time.Now())
	return len(q.pending[key])
}

// netConsoleKey is the key of the console of a request: a key of the role Console.
func netConsoleKey(w http.ResponseWriter, r *http.Request) (*Principal, bool) {
	principal := principalFrom(r)
	if principal.Source != "key" || principal.Role != ROLE_CONSOLE || !netKeyIdPattern.MatchString(principal.KeyID) {
		http.Error(w, "only the key of a console", http.StatusForbidden)
		return nil, false
	}
	return principal, true
}

// netUserConsoles are the consoles of a user: their keys of the role Console.
func (web *Web) netUserConsoles(user string) []KeyRow {
	consoles := []KeyRow{}
	if user == "" {
		return consoles
	}
	for _, key := range web.auth.users.KeyRows(user) {
		if key.Role == ROLE_CONSOLE && strings.EqualFold(key.User, user) {
			consoles = append(consoles, key)
		}
	}
	return consoles
}

// netUserConsole is a console of a user by the id of its key.
func (web *Web) netUserConsole(user string, id string) (KeyRow, bool) {
	for _, key := range web.netUserConsoles(user) {
		if key.ID == id {
			return key, true
		}
	}
	return KeyRow{}, false
}

// netPushItemOf is a file of the library as the console gets it.
func netPushItemOf(file netFile) netPushItem {
	return netPushItem{FileId: file.id, Url: netInstallFiles + file.id + "/" + url.PathEscape(file.name), Game: file.game,
		TitleId: file.titleId, Kind: netCatalogKinds[file.kind], Version: file.version, Size: file.size,
		Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(file.name)), "."), Name: netPushName(file)}
}

// netPushName is the name of a file sent: the one of the DLC, or the one of the file.
func netPushName(file netFile) string {
	if file.kind == netFolderDlc && file.dlcName != "" {
		return file.dlcName
	}
	return file.name
}

// handleNetPushSend sends files of the library to a console of the user.
func (web *Web) handleNetPushSend(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := netPairer(r)
	if !ok {
		writeGlobalError(w, http.StatusForbidden, lang, errNetPushNotAllow.Error())
		return
	}
	appSettings := settings.ReadSettings(web.dataFolder)
	if !netInstallEnabled(appSettings) {
		writeGlobalError(w, http.StatusConflict, lang, netPushOffMessage)
		return
	}
	var body struct {
		KeyId   string   `json:"keyId"`
		FileIds []string `json:"fileIds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeGlobalError(w, http.StatusBadRequest, lang, errNetPushNoFiles.Error())
		return
	}
	console, ok := web.netUserConsole(principal.Name, body.KeyId)
	if !ok {
		writeGlobalError(w, http.StatusNotFound, lang, errNetPushConsole.Error())
		return
	}
	if len(body.FileIds) == 0 || len(body.FileIds) > netPushMax {
		writeGlobalError(w, http.StatusBadRequest, lang, errNetPushNoFiles.Error())
		return
	}
	_, byId := web.netFilesKept(appSettings.NetInstall)
	now := time.Now()
	pushes := []*netPush{}
	seen := map[string]bool{}
	games := []string{}
	for _, id := range body.FileIds {
		file, found := byId[id]
		if !found {
			writeGlobalError(w, http.StatusNotFound, lang, errNetPushGone.Error())
			return
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		random := make([]byte, 16)
		rand.Read(random)
		pushes = append(pushes, &netPush{id: hex.EncodeToString(random), keyId: console.ID, keyName: console.Name, owner: principal.Name,
			fileId: id, game: file.game, made: now})
		if !containsString(games, file.game) {
			games = append(games, file.game)
		}
	}
	if err := web.netPushes.add(pushes); err != nil {
		writeGlobalError(w, http.StatusTooManyRequests, lang, err.Error())
		return
	}
	web.sugarLogger.Infof("Network install: %s sent %d files to the console %s", principal.Name, len(pushes), console.Name)
	if !isDemoMode() {
		web.activities().Add(activity.Entry{Time: now, User: principal.Name, Action: activity.ACTION_NET_PUSH,
			Detail: quotedKeyName(console.Name) + ": " + strings.Join(games, ", "), IP: clientIp(r)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": len(pushes), "console": console.Name})
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// netPushTimeout is how long a console waits, from ?timeout= in seconds.
func netPushTimeout(r *http.Request) time.Duration {
	seconds, err := strconv.Atoi(r.URL.Query().Get("timeout"))
	if err != nil || seconds <= 0 {
		seconds = netPushWaitDefault
	}
	return time.Duration(min(seconds, netPushWaitMax)) * time.Second
}

// handleNetPushWait answers a console that waits for what is sent to it: at once with what
// waits, or once something comes, or with nothing (204) at the end of the time it waits.
func (web *Web) handleNetPushWait(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netInstallGate(w, r)
	if !ok {
		return
	}
	key, ok := netConsoleKey(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	timer := time.NewTimer(netPushTimeout(r))
	defer timer.Stop()
	for {
		pushes, signal := web.netPushes.take(key.KeyID)
		if len(pushes) > 0 {
			_, byId := web.netFilesKept(options)
			items := []netPushItem{}
			for _, push := range pushes {
				file, found := byId[push.fileId]
				if !found {
					// gone from the library since it was sent
					continue
				}
				push.item = netPushItemOf(file)
				push.item.Id, push.item.From = push.id, push.owner
				web.netPushes.given(push)
				items = append(items, push.item)
			}
			if len(items) > 0 {
				writeJSON(w, http.StatusOK, map[string]any{"items": items})
				return
			}
			continue
		}
		select {
		case <-signal:
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		case <-web.closingPages():
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
}

// handleNetPushAnswer notes the answer of the user of a console to a file sent to it.
func (web *Web) handleNetPushAnswer(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netConsoleKey(w, r)
	if !ok {
		return
	}
	var body struct {
		Id       string `json:"id"`
		Accepted bool   `json:"accepted"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || !netPushIdPattern.MatchString(body.Id) {
		http.Error(w, "the id of an install sent to the console", http.StatusBadRequest)
		return
	}
	push, found := web.netPushes.answered(key.KeyID, body.Id)
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"state": "unknown"})
		return
	}
	action, word := activity.ACTION_NET_PUSH_NO, "declined"
	if body.Accepted {
		action, word = activity.ACTION_NET_PUSH_YES, "accepted"
	}
	web.sugarLogger.Infof("Network install: the console %s (%s) %s %s", key.KeyName, key.Name, word, push.item.Name)
	if !isDemoMode() {
		web.activities().Add(activity.Entry{Time: time.Now(), User: key.Name, Action: action,
			Detail: quotedKeyName(key.KeyName) + ": " + push.item.Name, IP: clientIp(r)})
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "noted"})
}

// netInventoryItem is a title installed on a console.
type netInventoryItem struct {
	TitleId string `json:"titleId"`
	Type    string `json:"type"` // game, update or dlc
	Version int    `json:"version"`
	Storage string `json:"storage,omitempty"` // sd or nand
}

// netInventory is what is installed on a console, as it told it last.
type netInventory struct {
	KeyId   string             `json:"keyId"`
	Console string             `json:"console"`
	Owner   string             `json:"owner"`
	Updated time.Time          `json:"updated"`
	Items   []netInventoryItem `json:"items"`
}

var netInventoryTypes = map[string]bool{"game": true, "update": true, "dlc": true}

func (web *Web) netInventoryPath(keyId string) string {
	return filepath.Join(web.dataFolder, netInventoryFolder, keyId+".json")
}

// readNetInventory is what is installed on a console, if it told it.
func (web *Web) readNetInventory(keyId string) (*netInventory, bool) {
	if !netKeyIdPattern.MatchString(keyId) {
		return nil, false
	}
	data, err := os.ReadFile(web.netInventoryPath(keyId))
	if err != nil {
		return nil, false
	}
	inventory := &netInventory{}
	if json.Unmarshal(data, inventory) != nil || inventory.KeyId != keyId {
		return nil, false
	}
	return inventory, true
}

// netInventoryForget deletes what is known to be installed on a console.
func (web *Web) netInventoryForget(keyId string) error {
	if !netKeyIdPattern.MatchString(keyId) {
		return nil
	}
	web.netInventories.Lock()
	defer web.netInventories.Unlock()
	if err := os.Remove(web.netInventoryPath(keyId)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// cleanNetInventory keeps the titles told well, once each.
func cleanNetInventory(items []netInventoryItem) []netInventoryItem {
	cleaned := []netInventoryItem{}
	seen := map[string]bool{}
	for _, item := range items {
		item.TitleId = strings.ToUpper(strings.TrimSpace(item.TitleId))
		item.Type = strings.ToLower(strings.TrimSpace(item.Type))
		item.Storage = strings.ToLower(strings.TrimSpace(item.Storage))
		if !netTitleIdPattern.MatchString(item.TitleId) || !netInventoryTypes[item.Type] || item.Version < 0 {
			continue
		}
		if item.Storage != "sd" && item.Storage != "nand" {
			item.Storage = ""
		}
		id := item.TitleId + "|" + item.Type + "|" + item.Storage
		if seen[id] {
			continue
		}
		seen[id] = true
		cleaned = append(cleaned, item)
	}
	return cleaned
}

// handleNetInventory keeps what is installed on the console, in place of what it told before;
// {"off": true} forgets it.
func (web *Web) handleNetInventory(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netConsoleKey(w, r)
	if !ok {
		return
	}
	var body struct {
		Off   bool               `json:"off"`
		Items []netInventoryItem `json:"items"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
		http.Error(w, "the titles installed on the console, as JSON", http.StatusBadRequest)
		return
	}
	if body.Off {
		if err := web.netInventoryForget(key.KeyID); err != nil {
			http.Error(w, "could not forget what is installed", http.StatusInternalServerError)
			return
		}
		web.sugarLogger.Infof("Network install: the console %s (%s) no longer tells what is installed on it", key.KeyName, key.Name)
		writeJSON(w, http.StatusOK, map[string]any{"off": true})
		return
	}
	if len(body.Items) > netInventoryMax {
		http.Error(w, "at most 5000 titles", http.StatusRequestEntityTooLarge)
		return
	}
	inventory := netInventory{KeyId: key.KeyID, Console: key.KeyName, Owner: key.Name, Updated: time.Now().UTC(), Items: cleanNetInventory(body.Items)}
	data, err := json.Marshal(inventory)
	if err == nil {
		web.netInventories.Lock()
		if err = os.MkdirAll(filepath.Join(web.dataFolder, netInventoryFolder), 0755); err == nil {
			err = durable.WriteFile(web.netInventoryPath(key.KeyID), data)
		}
		web.netInventories.Unlock()
	}
	if err != nil {
		web.sugarLogger.Warnf("Network install: what is installed on the console %s could not be kept: %v", key.KeyName, err)
		http.Error(w, "could not keep what is installed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stored": len(inventory.Items)})
}

// netInventorySummary is what is known to be installed on a console, for the web.
type netInventorySummary struct {
	Titles  int       `json:"titles"`
	Items   int       `json:"items"`
	Updated time.Time `json:"updated"`
}

// netConsoleRow is a console of the user, for the web.
type netConsoleRow struct {
	Id        string               `json:"id"`
	Name      string               `json:"name"`
	Waiting   int                  `json:"waiting"`
	Inventory *netInventorySummary `json:"inventory,omitempty"`
	// for the page of a game: what of it is installed on the console
	Installed *netGameOnConsole `json:"installed,omitempty"`
}

// netGameOnConsole is a game installed on a console: its newest version, where, and its DLC.
type netGameOnConsole struct {
	Version int    `json:"version"`
	Storage string `json:"storage,omitempty"`
	Dlc     int    `json:"dlc"`
}

// netGamePrefix is the key that groups a title with its game, as the library has it.
func netGamePrefix(titleId string) string {
	prefix, err := db.TitleIDPrefix(strings.ToLower(titleId))
	if err != nil {
		return ""
	}
	return prefix
}

func summarizeNetInventory(inventory *netInventory) *netInventorySummary {
	games := map[string]bool{}
	for _, item := range inventory.Items {
		if prefix := netGamePrefix(item.TitleId); prefix != "" {
			games[prefix] = true
		}
	}
	return &netInventorySummary{Titles: len(games), Items: len(inventory.Items), Updated: inventory.Updated}
}

// netGameInInventory is what of a game (by its prefix) is installed on a console, if anything.
func netGameInInventory(inventory *netInventory, prefix string) *netGameOnConsole {
	var found *netGameOnConsole
	for _, item := range inventory.Items {
		if netGamePrefix(item.TitleId) != prefix {
			continue
		}
		if found == nil {
			found = &netGameOnConsole{}
		}
		switch item.Type {
		case "dlc":
			found.Dlc++
		case "game":
			if item.Storage != "" {
				found.Storage = item.Storage
			}
			found.Version = max(found.Version, item.Version)
		default:
			if found.Storage == "" {
				found.Storage = item.Storage
			}
			found.Version = max(found.Version, item.Version)
		}
	}
	return found
}

// netConsoleRows are the consoles of the user, with what is known to be installed on them; with a
// prefix, what of that game.
func (web *Web) netConsoleRows(user string, prefix string) []netConsoleRow {
	rows := []netConsoleRow{}
	for _, key := range web.netUserConsoles(user) {
		row := netConsoleRow{Id: key.ID, Name: key.Name, Waiting: web.netPushes.waiting(key.ID)}
		if inventory, ok := web.readNetInventory(key.ID); ok {
			row.Inventory = summarizeNetInventory(inventory)
			if prefix != "" {
				row.Installed = netGameInInventory(inventory, prefix)
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name) })
	return rows
}

// handleNetConsoles gives the consoles of the user, with what is installed on them.
func (web *Web) handleNetConsoles(w http.ResponseWriter, r *http.Request) {
	enabled := netInstallEnabled(settings.ReadSettings(web.dataFolder))
	rows := []netConsoleRow{}
	if principal, ok := netPairer(r); ok {
		rows = web.netConsoleRows(principal.Name, "")
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "consoles": rows})
}

// netGameFile is a file of a game that may be sent to a console.
type netGameFile struct {
	Id      string `json:"id"`
	Kind    string `json:"kind"`
	Version int    `json:"version"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	// ticked at first: the game and its newest update, as what the console has is not known
	Checked bool `json:"checked"`
}

// handleNetGame gives, for the page of a game, the consoles of the user with what of the game is
// installed on them, and the files of the game that may be sent.
func (web *Web) handleNetGame(w http.ResponseWriter, r *http.Request) {
	appSettings := settings.ReadSettings(web.dataFolder)
	answer := map[string]any{"enabled": netInstallEnabled(appSettings), "consoles": []netConsoleRow{}, "files": []netGameFile{}}
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := netPairer(r)
	prefix := netGamePrefix(mux.Vars(r)["titleId"])
	if !ok || prefix == "" {
		writeJSON(w, http.StatusOK, answer)
		return
	}
	consoles := web.netConsoleRows(principal.Name, prefix)
	answer["consoles"] = consoles
	if len(consoles) == 0 {
		writeJSON(w, http.StatusOK, answer)
		return
	}
	kept, _ := web.netFilesKept(appSettings.NetInstall)
	files := []netGameFile{}
	newest := -1
	for _, file := range kept {
		if file.gameKey == prefix && file.kind == netFolderUpdates {
			newest = max(newest, file.version)
		}
	}
	for _, file := range kept {
		if file.gameKey != prefix {
			continue
		}
		kind := netCatalogKinds[file.kind]
		files = append(files, netGameFile{Id: file.id, Kind: kind, Version: file.version, Name: netPushName(file), Size: file.size,
			Checked: kind == "game" || (kind == "update" && file.version == newest)})
	}
	order := map[string]int{"game": 0, "update": 1, "dlc": 2}
	sort.SliceStable(files, func(i, j int) bool {
		if order[files[i].Kind] != order[files[j].Kind] {
			return order[files[i].Kind] < order[files[j].Kind]
		}
		if files[i].Version != files[j].Version {
			return files[i].Version > files[j].Version
		}
		return files[i].Name < files[j].Name
	})
	answer["files"] = files
	writeJSON(w, http.StatusOK, answer)
}

// handleNetInventoryForget forgets what is installed on a console of the user.
func (web *Web) handleNetInventoryForget(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := netPairer(r)
	if !ok {
		writeGlobalError(w, http.StatusForbidden, lang, errNetPushNotAllow.Error())
		return
	}
	console, ok := web.netUserConsole(principal.Name, mux.Vars(r)["id"])
	if !ok {
		writeGlobalError(w, http.StatusNotFound, lang, errNetPushConsole.Error())
		return
	}
	if err := web.netInventoryForget(console.ID); err != nil {
		writeGlobalError(w, http.StatusInternalServerError, lang, "Error!")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "forgotten"})
}

// handleNetPush adds the paths of sending to a console and of what is installed on it.
func (web *Web) handleNetPush() {
	web.router.HandleFunc(netInstallPushWait, web.handleNetPushWait).Methods(http.MethodGet)
	web.router.HandleFunc(netInstallPushAnswer, web.handleNetPushAnswer).Methods(http.MethodPost)
	web.router.HandleFunc(netInstallInventory, web.handleNetInventory).Methods(http.MethodPost)
	web.router.HandleFunc(netInstallPushSend, web.handleNetPushSend).Methods(http.MethodPost)
	web.router.HandleFunc(netInstallConsoles, web.handleNetConsoles).Methods(http.MethodGet)
	web.router.HandleFunc(netInstallGame+"{titleId:[0-9A-Fa-f]{16}}", web.handleNetGame).Methods(http.MethodGet)
	web.router.HandleFunc(netInventoryForgetPrefix+"{id:[0-9a-f]{16}}", web.handleNetInventoryForget).Methods(http.MethodDelete)
}
