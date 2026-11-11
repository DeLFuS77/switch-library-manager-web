package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
)

// A console finds the app by itself and is paired with a code, so nobody types an address or a
// key: NXDock asks the local network who is there (a UDP message, or the port of the app on the
// addresses of the network), shows a code, and a user who may download accepts it in the app.
// The app then makes a key of the role Console for that user and gives it to the console once.
// The console keeps it, and the next times it signs in by itself.

const (
	netInstallHello = NET_INSTALL_PREFIX + "hello"
	netInstallPair  = NET_INSTALL_PREFIX + "pair"
	// the port the app listens on for the question of an installer on the local network
	netDiscoveryPort = 45730
	netDiscoveryAsk  = "NXDOCK-DISCOVER 1"
	netAppName       = "switch-library-manager-web"
	// the code shown on the console: no letters easy to mistake for others
	netPairAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	netPairCodeLen  = 8
	// how long a console waits to be accepted, and then to take its key
	netPairLife   = 10 * time.Minute
	netPairPickup = 2 * time.Minute
	// pairings waiting at once: a flood of them is refused
	netPairMax = 8
	// the name of a console as it asks to be paired
	maxNetConsoleName = 40
)

var netPairIdPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// netHelloAnswer is what the app tells an installer that looks for it: nothing about the
// library, only that it is this app, which one, and whether it installs over the network now.
type netHelloAnswer struct {
	App     string `json:"app"`
	Id      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Port    int    `json:"port"`
	Enabled bool   `json:"enabled"`
}

// netPairing is a console that asked to be paired.
type netPairing struct {
	id, code, console, ip string
	made                  time.Time
	accepted, rejected    bool
	decided               time.Time
	// the key made when accepted, given once to the console and then forgotten
	key, keyName, user string
}

type netPairings struct {
	mutex sync.Mutex
	list  map[string]*netPairing
}

// netPairRow is a pairing waiting, as the app shows it to the users who may accept it.
type netPairRow struct {
	Id        string `json:"id"`
	Code      string `json:"code"`
	Console   string `json:"console"`
	Ip        string `json:"ip"`
	Expires   int64  `json:"expires"`
	Enabled   bool   `json:"enabled"`
	CanEnable bool   `json:"canEnable"`
}

var errNetPairGone = errors.New("the console is no longer waiting")

// netInstanceId names this app among others on the network, kept in the data folder so it stays
// the same after an update or a new address: a console paired once finds it again by it.
func (web *Web) netInstanceId() string {
	web.netIdOnce.Do(func() {
		path := filepath.Join(web.dataFolder, "instance-id")
		if data, err := os.ReadFile(path); err == nil {
			if id := strings.TrimSpace(string(data)); len(id) == 32 {
				web.netId = id
				return
			}
		}
		random := make([]byte, 16)
		rand.Read(random)
		web.netId = hex.EncodeToString(random)
		os.WriteFile(path, []byte(web.netId+"\n"), 0644)
	})
	return web.netId
}

func (web *Web) netHello() netHelloAnswer {
	appSettings := settings.ReadSettings(web.dataFolder)
	return netHelloAnswer{App: netAppName, Id: web.netInstanceId(), Name: "Switch Library Manager", Version: settings.SLM_WEB_VERSION,
		Port: appSettings.Port, Enabled: netInstallEnabled(appSettings)}
}

// netPairGate answers a console that the app does not take now: without users (never without a
// password), or from outside the local network unless that is allowed. It works with the
// network install turned off, so a console can ask and an administrator turn it on as they accept.
func (web *Web) netPairGate(w http.ResponseWriter, r *http.Request) bool {
	appSettings := settings.ReadSettings(web.dataFolder)
	if !web.auth.Enabled() {
		http.Error(w, "installing over the network needs a user with a password", http.StatusForbidden)
		return false
	}
	if !appSettings.NetInstall.Remote && !isLocalRequest(r) {
		http.Error(w, "installing over the network answers only on the local network", http.StatusForbidden)
		return false
	}
	return true
}

// isConsolePublicRequest tells the requests a console sends before it has a key: hello and the
// pairing. Written plainly, as the other public paths.
func isConsolePublicRequest(r *http.Request) bool {
	p := r.URL.Path
	if r.URL.EscapedPath() != p || filepath.ToSlash(p) != p {
		return false
	}
	switch {
	case p == netInstallHello && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		return true
	case p == netInstallPair && r.Method == http.MethodPost:
		return true
	case strings.HasPrefix(p, netInstallPair+"/") && r.Method == http.MethodGet:
		return netPairIdPattern.MatchString(strings.TrimPrefix(p, netInstallPair+"/"))
	}
	return false
}

func newNetPairCode() string {
	code := make([]byte, 0, netPairCodeLen)
	random := make([]byte, 32)
	for len(code) < netPairCodeLen {
		rand.Read(random)
		for _, b := range random {
			// 256 is 8 times 32: every letter as likely as the others
			if len(code) < netPairCodeLen {
				code = append(code, netPairAlphabet[int(b)%len(netPairAlphabet)])
			}
		}
	}
	return string(code)
}

// netShownCode is the code as the console and the app show it: two groups of four.
func netShownCode(code string) string {
	if len(code) != netPairCodeLen {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// cleanNetConsoleName is the name a console gives, fit to name its key.
func cleanNetConsoleName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if runes := []rune(name); len(runes) > maxNetConsoleName {
		name = string(runes[:maxNetConsoleName])
	}
	if cleaned, ok := cleanAPIKeyName(name); ok {
		return cleaned
	}
	return "NXDock"
}

// forgetOld drops the pairings that ran out of time. With the mutex held.
func (p *netPairings) forgetOld(now time.Time) {
	for id, pairing := range p.list {
		decided := pairing.accepted || pairing.rejected
		if (!decided && now.Sub(pairing.made) > netPairLife) || (decided && now.Sub(pairing.decided) > netPairPickup) {
			delete(p.list, id)
		}
	}
}

func (web *Web) handleNetHello(w http.ResponseWriter, r *http.Request) {
	if !web.netPairGate(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, web.netHello())
}

// handleNetPairAsk is a console that asks to be paired: it gets the id to wait with and the code
// it shows. A second request of the same address takes the place of the first.
func (web *Web) handleNetPairAsk(w http.ResponseWriter, r *http.Request) {
	if !web.netPairGate(w, r) {
		return
	}
	var body struct {
		Console string `json:"console"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	ip := clientIp(r)
	now := time.Now()
	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	pairing := &netPairing{id: hex.EncodeToString(idBytes), code: newNetPairCode(), console: cleanNetConsoleName(body.Console), ip: ip, made: now}

	pairs := &web.netPairs
	pairs.mutex.Lock()
	if pairs.list == nil {
		pairs.list = map[string]*netPairing{}
	}
	pairs.forgetOld(now)
	for id, other := range pairs.list {
		if other.ip == ip && !other.accepted && !other.rejected {
			delete(pairs.list, id)
		}
	}
	if len(pairs.list) >= netPairMax {
		pairs.mutex.Unlock()
		http.Error(w, "too many consoles waiting, try again in a few minutes", http.StatusTooManyRequests)
		return
	}
	pairs.list[pairing.id] = pairing
	pairs.mutex.Unlock()

	web.sugarLogger.Infof("Network install: a console (%s) asks to be paired", pairing.console)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"id": pairing.id, "code": netShownCode(pairing.code), "expires": int(netPairLife.Seconds())})
}

// handleNetPairWait answers a console that waits: still waiting, refused, or accepted with its
// key, given once and only to the address that asked.
func (web *Web) handleNetPairWait(w http.ResponseWriter, r *http.Request) {
	if !web.netPairGate(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, netInstallPair+"/")
	w.Header().Set("Cache-Control", "no-store")
	pairs := &web.netPairs
	pairs.mutex.Lock()
	defer pairs.mutex.Unlock()
	pairs.forgetOld(time.Now())
	pairing, ok := pairs.list[id]
	if !ok || pairing.ip != clientIp(r) {
		writeJSON(w, http.StatusGone, map[string]string{"state": "expired"})
		return
	}
	switch {
	case pairing.rejected:
		delete(pairs.list, id)
		writeJSON(w, http.StatusForbidden, map[string]string{"state": "rejected"})
	case pairing.accepted:
		delete(pairs.list, id)
		hello := web.netHello()
		writeJSON(w, http.StatusOK, map[string]any{"state": "accepted", "key": pairing.key, "name": pairing.keyName, "user": pairing.user, "enabled": hello.Enabled, "app": hello})
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"state": "waiting"})
	}
}

// netPairer is who may accept a console: a user signed in who may download, not a key.
func netPairer(r *http.Request) (*Principal, bool) {
	principal := principalFrom(r)
	if principal.Source == "key" || principal.Name == "" || principal.Source == "env" {
		return principal, false
	}
	return principal, roleRank(principal.Role) >= roleRank(ROLE_VIEWER)
}

// handleNetPairList gives the consoles waiting, to the users who may accept them.
func (web *Web) handleNetPairList(w http.ResponseWriter, r *http.Request) {
	principal, ok := netPairer(r)
	rows := []netPairRow{}
	if !ok {
		writeJSON(w, http.StatusOK, rows)
		return
	}
	enabled := netInstallEnabled(settings.ReadSettings(web.dataFolder))
	now := time.Now()
	pairs := &web.netPairs
	pairs.mutex.Lock()
	pairs.forgetOld(now)
	for _, pairing := range pairs.list {
		if pairing.accepted || pairing.rejected {
			continue
		}
		rows = append(rows, netPairRow{Id: pairing.id, Code: netShownCode(pairing.code), Console: pairing.console, Ip: pairing.ip,
			Expires: int64(netPairLife.Seconds() - now.Sub(pairing.made).Seconds()), Enabled: enabled, CanEnable: principal.IsAdmin()})
	}
	pairs.mutex.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].Expires > rows[j].Expires })
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, rows)
}

// handleNetPairDecide accepts or refuses a console. Accepted, it gets a key of the role Console
// of the user who accepted it; an administrator may turn the network install on at once.
func (web *Web) handleNetPairDecide(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := netPairer(r)
	if !ok {
		writeGlobalError(w, http.StatusForbidden, lang, "only a user who may download accepts a console")
		return
	}
	id := mux.Vars(r)["id"]
	accept := r.FormValue("action") == "accept"
	enable := r.FormValue("enable") == "true"

	pairs := &web.netPairs
	pairs.mutex.Lock()
	pairs.forgetOld(time.Now())
	pairing, found := pairs.list[id]
	if !found || pairing.accepted || pairing.rejected {
		pairs.mutex.Unlock()
		writeGlobalError(w, http.StatusGone, lang, errNetPairGone.Error())
		return
	}
	if !accept {
		pairing.rejected, pairing.decided = true, time.Now()
		pairs.mutex.Unlock()
		web.sugarLogger.Infof("Network install: %s refused the console %s", principal.Name, pairing.console)
		writeJSON(w, http.StatusOK, map[string]string{"state": "rejected"})
		return
	}
	console := pairing.console
	pairs.mutex.Unlock()

	if enable && principal.IsAdmin() && !netInstallEnabled(settings.ReadSettings(web.dataFolder)) {
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = true })
		web.netFilesForget()
	}
	// the key is named after the console; a name taken gets a number
	var secret string
	var key APIKey
	var err error
	for n := 1; n <= 20; n++ {
		name := console
		if n > 1 {
			suffix := " " + string(rune('0'+n%10))
			if n >= 10 {
				suffix = " 1" + string(rune('0'+n%10))
			}
			name = string([]rune(console)[:min(len([]rune(console)), maxAPIKeyName-len(suffix))]) + suffix
		}
		secret, key, err = web.auth.users.AddAPIKey(principal.Name, name, ROLE_CONSOLE)
		if !errors.Is(err, ErrAPIKeyName) {
			break
		}
	}
	if err != nil {
		writeGlobalError(w, apiKeyStatus(err), lang, err.Error())
		return
	}

	pairs.mutex.Lock()
	pairing, found = pairs.list[id]
	if found {
		pairing.accepted, pairing.decided = true, time.Now()
		pairing.key, pairing.keyName, pairing.user = secret, key.Name, principal.Name
	}
	pairs.mutex.Unlock()
	if !found {
		// it ran out of time while the key was made: the key would be of nobody
		web.auth.users.DeleteAPIKey(principal.Name, key.ID)
		writeGlobalError(w, http.StatusGone, lang, errNetPairGone.Error())
		return
	}
	web.sugarLogger.Infof("Network install: %s accepted the console %s", principal.Name, key.Name)
	web.activities().Add(activity.Entry{Time: time.Now(), User: principal.Name, Action: activity.ACTION_KEY_NEW,
		Detail: quotedKeyName(key.Name) + ": " + apiKeyRoleDetail(key.Role), IP: clientIp(r)})
	writeJSON(w, http.StatusOK, map[string]string{"state": "accepted", "name": key.Name})
}

// StartNetDiscovery answers the installers that ask the local network where the app is. Only
// with users (never without a password) and only to addresses of the local network unless the
// network install answers outside it.
func (web *Web) StartNetDiscovery() {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: netDiscoveryPort})
	if err != nil {
		web.sugarLogger.Infof("Network install: consoles will not find the app by themselves (UDP %d: %v); they still find it on its port", netDiscoveryPort, err)
		return
	}
	go func() {
		defer conn.Close()
		buffer := make([]byte, 256)
		answered := 0
		window := time.Now()
		for {
			n, from, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			if strings.TrimSpace(string(buffer[:n])) != netDiscoveryAsk || !web.auth.Enabled() {
				continue
			}
			if !settings.ReadSettings(web.dataFolder).NetInstall.Remote && !isLocalAddress(from.IP.String()) {
				continue
			}
			// a few answers a second at most
			if time.Since(window) > time.Second {
				window, answered = time.Now(), 0
			}
			if answered++; answered > 20 {
				continue
			}
			answer, _ := json.Marshal(web.netHello())
			conn.WriteToUDP(answer, from)
		}
	}()
}

// handleNetPairing adds the paths of finding and pairing.
func (web *Web) handleNetPairing() {
	web.router.HandleFunc(netInstallHello, web.handleNetHello).Methods(http.MethodGet, http.MethodHead)
	web.router.HandleFunc(netInstallPair, web.handleNetPairAsk).Methods(http.MethodPost)
	web.router.HandleFunc(netInstallPair+"/{id:[0-9a-f]{32}}", web.handleNetPairWait).Methods(http.MethodGet)
	web.router.HandleFunc("/api/install/pairs", web.handleNetPairList).Methods(http.MethodGet)
	web.router.HandleFunc("/api/install/pairs/{id:[0-9a-f]{32}}", web.handleNetPairDecide).Methods(http.MethodPost)
}
