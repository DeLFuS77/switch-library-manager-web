package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// CyberFoil and Sphaira find a shop on the local network by themselves, as they find Ownfoil and
// AeroFoil, and the app answers them as one, so nobody types its address on the console.
//
// The installer sends the 16 bytes OWNFOIL_DISCOVER, with nothing after them, over UDP to the
// port 8465 of the broadcast address of its network and of 255.255.255.255, again while it
// listens (Sphaira every half a second for 1.5 seconds, CyberFoil three times in 3 seconds), and
// lists every server that answers. The servers take the question with the spaces around it cut,
// and answer with a JSON object in one datagram, back to the address and port that asked:
//
//	{"magic":"OWNFOIL","uid":"0123abcd-...","name":"...","version":"...","port":3000,"public":false}
//
//   - magic is OWNFOIL, or the answer is ignored.
//   - uid names the server among the others, the same every time. CyberFoil takes only a UUID
//     written with its dashes (8-4-4-4-12 hexadecimal digits). Sphaira saves the server with the
//     uid of the hello of the shop (OPTIONS, netownfoil.go) and finds it by it among the answers:
//     both say the same one, or the server found again would be listed twice.
//   - name and version are shown in the list (CyberFoil cuts them at 48 and 24 bytes).
//   - port is the port of the web server as the local network reaches it (in Docker it may be
//     published on another one, see netport.go). The address is never in the answer: both
//     installers take the one the answer comes from and open http://<that address>:<port>/. There
//     Sphaira says hello with OPTIONS and asks POST /api/graphql, and CyberFoil reads the index
//     of Tinfoil, which the app answers at the address of the server alone with the key of a
//     console (ownfoilShopAddress, shortShopAddress).
//   - public false: the shop needs a login, so both ask for the user and the password. No key nor
//     password is ever in the answer; the key of the console is typed on the console as before.
//   - remote, an address from outside the network, is optional and not sent.
//
// Sphaira reads at most 1023 bytes of an answer, CyberFoil 4096.
//
// Read from the sources of CyberFoil (source/util/lan_discovery.cpp and lan_discovery_scan.cpp,
// https://github.com/luketanti/CyberFoil), AeroFoil (app/discovery.py,
// https://github.com/luketanti/AeroFoil), Ownfoil 2.5.0 (app/discovery.py,
// https://github.com/a1ex4/ownfoil) and Sphaira 1.1.0 (sphaira/source/utils/ownfoil_discovery.cpp,
// https://github.com/NaGaa95/sphaira).
//
// The app answers only while installing over the network is on, with users, and only to the
// local network unless it answers outside it too, as /install/ does. It takes the port while
// installing over the network is on and lets it go when it is turned off, so an Ownfoil on the
// same computer can have it then; taken by another program, the log tells it once and the app
// goes on without it.

const (
	ownfoilDiscoveryPort  = 8465
	ownfoilDiscoveryAsk   = "OWNFOIL_DISCOVER"
	ownfoilDiscoveryMagic = "OWNFOIL"
	// how often the setting is looked at, to take the port or let it go
	ownfoilDiscoveryCheck = 5 * time.Second
	// answers a second at most
	ownfoilDiscoveryRate = 20
)

// ownfoilDiscoveryAnswer is what the app tells an installer that looks for shops: nothing about
// the library, only that it is a shop, which one and on which port.
type ownfoilDiscoveryAnswer struct {
	Magic   string `json:"magic"`
	Uid     string `json:"uid"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Port    int    `json:"port"`
	Public  bool   `json:"public"`
}

// ownfoilDiscovery is the port the app answers on, open while installing over the network is on.
type ownfoilDiscovery struct {
	// every address of the computer when nil (the tests listen on 127.0.0.1 alone)
	ip    net.IP
	port  int
	mutex sync.Mutex
	conn  *net.UDPConn
	// the port was taken by another program, which the log told once
	told bool
}

// netShopUid is the id of the app written as a UUID, as the installers that find shops on the
// network take it: said in their discovery and in the hello of the shop.
func (web *Web) netShopUid() string {
	id := strings.ToLower(web.netInstanceId())
	if len(id) != 32 {
		return id
	}
	return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}

// StartOwnfoilDiscovery answers CyberFoil and Sphaira when they look for shops on the local
// network, while installing over the network is on.
func (web *Web) StartOwnfoilDiscovery() {
	finder := &ownfoilDiscovery{port: ownfoilDiscoveryPort}
	go func() {
		for {
			web.followOwnfoilDiscovery(finder)
			time.Sleep(ownfoilDiscoveryCheck)
		}
	}()
}

// followOwnfoilDiscovery takes the port when installing over the network is on, and lets it go
// when it is off.
func (web *Web) followOwnfoilDiscovery(finder *ownfoilDiscovery) {
	on := netInstallEnabled(settings.ReadSettings(web.dataFolder))
	finder.mutex.Lock()
	defer finder.mutex.Unlock()
	switch {
	case on && finder.conn == nil:
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: finder.ip, Port: finder.port})
		if err != nil {
			if !finder.told {
				finder.told = true
				web.sugarLogger.Infof("Network install: CyberFoil and Sphaira will not find the library by themselves (UDP %d: %v); they still connect with its address", finder.port, err)
			}
			return
		}
		finder.conn = conn
		go web.answerOwnfoilDiscovery(conn)
	case !on && finder.conn != nil:
		finder.conn.Close()
		finder.conn = nil
	}
}

// stop lets the port go.
func (finder *ownfoilDiscovery) stop() {
	finder.mutex.Lock()
	defer finder.mutex.Unlock()
	if finder.conn != nil {
		finder.conn.Close()
		finder.conn = nil
	}
}

// answerOwnfoilDiscovery answers the questions that come to the port until it is closed.
func (web *Web) answerOwnfoilDiscovery(conn *net.UDPConn) {
	buffer := make([]byte, 256)
	answered := 0
	window := time.Now()
	for {
		n, from, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Windows tells here that an installer answered before stopped listening
			time.Sleep(50 * time.Millisecond)
			continue
		}
		answer, ok := web.ownfoilDiscoveryAnswerTo(buffer[:n], from.IP)
		if !ok {
			continue
		}
		if time.Since(window) > time.Second {
			window, answered = time.Now(), 0
		}
		if answered++; answered > ownfoilDiscoveryRate {
			continue
		}
		conn.WriteToUDP(answer, from)
	}
}

// ownfoilDiscoveryAnswerTo is the answer to a datagram that came from an address, when it is the
// question of an installer and the app answers it now.
func (web *Web) ownfoilDiscoveryAnswerTo(question []byte, from net.IP) ([]byte, bool) {
	if string(bytes.TrimSpace(question)) != ownfoilDiscoveryAsk {
		return nil, false
	}
	appSettings := settings.ReadSettings(web.dataFolder)
	if !netInstallEnabled(appSettings) || !web.auth.Enabled() {
		return nil, false
	}
	if !appSettings.NetInstall.Remote && !isLocalAddress(from.String()) {
		return nil, false
	}
	// the port the library is reached on from the local network, as NXDock is told it (netport.go)
	hello := web.netHello()
	answer, err := json.Marshal(ownfoilDiscoveryAnswer{Magic: ownfoilDiscoveryMagic, Uid: web.netShopUid(), Name: hello.Name,
		Version: hello.Version, Port: hello.Port})
	return answer, err == nil
}
