package web

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// The port the library is reached on from the local network, as the installers that find it by
// themselves are told it (NXDock, CyberFoil, Sphaira). In Docker the app listens on the port of
// its settings while the computer may publish it on another (-p 8080:3000), and the consoles would
// be sent to the port inside. The requests that reach the app from the local network tell the
// port they were sent to, in their Host header: the app remembers the one of the latest, keeps it
// in the data folder for the next start, and says the port it listens on until one is seen.
//
// Only a request that tells it truly is believed:
//   - it comes straight from another device of the local network: not from this computer itself
//     (in a container that is the health check, on the port inside) and without anything a
//     reverse proxy adds (forwarded addresses, host, protocol or port), as the port of a proxy
//     may be of another computer or of https, not the one the consoles reach;
//   - it names the computer by an address of the local network or a name of it (a name without a
//     dot, .local, .lan...): a public name reaches it through the router, whose port may be
//     another;
//   - it is of a browser signed in (or of the local network without users) or of a console with
//     its key: not of a key of another role, as tools such as dashboards reach the container by
//     its name and the port inside, nor of someone who is not let in.
//
// One port for both families of addresses: Docker publishes the same port on IPv4 and IPv6, and
// the discoveries answer over IPv4.

const netLanPortFile = "lan-port"

// netLanPortSeen is the port remembered, read from the data folder the first time it is needed.
type netLanPortSeen struct {
	once  sync.Once
	port  atomic.Int64
	mutex sync.Mutex
}

// lanPortOf is the port a request reached the app on, when the request tells it truly (see above).
func lanPortOf(r *http.Request, principal *Principal) (int, bool) {
	if r.TLS != nil || principal == nil {
		return 0, false
	}
	if principal.Source == "key" {
		if principal.Role != ROLE_CONSOLE {
			return 0, false
		}
	} else if r.Header.Get("Authorization") != "" || (principal.Name == "" && principal.Role == ROLE_GUEST) {
		return 0, false
	}
	remote := net.ParseIP(remoteHost(r))
	if remote == nil || remote.IsLoopback() || !isLocalAddress(remote.String()) {
		return 0, false
	}
	if len(forwardedAddresses(r)) > 0 {
		return 0, false
	}
	for _, header := range []string{"X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Forwarded-Server"} {
		if r.Header.Get(header) != "" {
			return 0, false
		}
	}
	host, port := r.Host, "80"
	if name, given, err := net.SplitHostPort(r.Host); err == nil {
		host, port = name, given
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "" {
		return 0, false
	}
	if net.ParseIP(host) != nil {
		if !isLocalAddress(host) {
			return 0, false
		}
	} else if strings.Contains(host, ".") && !hasLocalSuffix(host) {
		return 0, false
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return 0, false
	}
	return number, true
}

// hasLocalSuffix tells a name of the local network or of a VPN.
func hasLocalSuffix(host string) bool {
	for _, suffix := range localSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// withLanPort remembers the port of the requests that tell it, once they are let in.
func (web *Web) withLanPort(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if port, ok := lanPortOf(r, principalFrom(r)); ok {
			web.rememberLanPort(port)
		}
		next.ServeHTTP(w, r)
	})
}

func (web *Web) loadLanPort() {
	web.lanPort.once.Do(func() {
		data, err := os.ReadFile(filepath.Join(web.dataFolder, netLanPortFile))
		if err != nil {
			return
		}
		if port, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && port >= 1 && port <= 65535 {
			web.lanPort.port.Store(int64(port))
		}
	})
}

// rememberLanPort keeps a port seen, writing it to the data folder only when it changes.
func (web *Web) rememberLanPort(port int) {
	web.loadLanPort()
	if web.lanPort.port.Load() == int64(port) {
		return
	}
	web.lanPort.mutex.Lock()
	defer web.lanPort.mutex.Unlock()
	if web.lanPort.port.Swap(int64(port)) != int64(port) {
		os.WriteFile(filepath.Join(web.dataFolder, netLanPortFile), []byte(strconv.Itoa(port)+"\n"), 0644)
	}
}

// netLanPort is the port the installers are told: the one remembered, else the one the app
// listens on, else the one of the settings.
func (web *Web) netLanPort(settingsPort int) int {
	web.loadLanPort()
	if port := int(web.lanPort.port.Load()); port > 0 {
		return port
	}
	if listening := int(listeningPort.Load()); listening > 0 {
		return listening
	}
	return settingsPort
}
