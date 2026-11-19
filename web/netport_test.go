package web

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Only a request that tells truly the port the local network reaches the app on is believed.
func TestLanPortOfARequest(t *testing.T) {
	browser := &Principal{Name: "bob", Role: ROLE_VIEWER}
	console := &Principal{Name: "bob", Role: ROLE_CONSOLE, Source: "key", KeyName: "Switch", KeyID: "1"}
	tool := &Principal{Name: "alice", Role: ROLE_ADMIN, Source: "key", KeyName: "Dashboard", KeyID: "2"}
	withoutUsers := &Principal{Role: ROLE_ADMIN}
	nobody := &Principal{Role: ROLE_GUEST}
	for _, tt := range []struct {
		name      string
		principal *Principal
		remote    string
		host      string
		headers   []string
		tls       bool
		want      int
	}{
		{"a browser by the address of the computer", browser, "192.168.1.20", "192.168.1.10:8080", nil, false, 8080},
		{"a browser by the name of the NAS", browser, "192.168.1.20", "nas:3030", nil, false, 3030},
		{"a name of the local network", browser, "10.0.0.5", "tower.local:3030", nil, false, 3030},
		{"IPv6", browser, "fe80::2", "[fe80::1]:3030", nil, false, 3030},
		{"no port: 80", browser, "192.168.1.20", "nas", nil, false, 80},
		{"the computer itself through Docker", browser, "172.17.0.1", "localhost:3030", nil, false, 3030},
		{"the local network without users", withoutUsers, "192.168.1.20", "192.168.1.10:8080", nil, false, 8080},
		{"a console with its key", console, "192.168.1.30", "192.168.1.10:3030", []string{"Authorization", basic("console", "slm_key")}, false, 3030},
		{"a key of another role", tool, "172.18.0.5", "switch-library-manager-web:3000", []string{"Authorization", "Bearer slm_key"}, false, 0},
		{"a password sent with the request", browser, "192.168.1.20", "nas:3000", []string{"Authorization", basic("bob", "bravo-secret-22")}, false, 0},
		{"nobody signed in", nobody, "192.168.1.20", "nas:3000", nil, false, 0},
		{"the health check of the container", withoutUsers, "127.0.0.1", "localhost:3000", nil, false, 0},
		{"from outside the local network", browser, "203.0.113.9", "192.168.1.10:8080", nil, false, 0},
		{"through a reverse proxy", browser, "192.168.1.2", "nas:443", []string{"X-Forwarded-For", "192.168.1.20"}, false, 0},
		{"a proxy that tells the protocol", browser, "192.168.1.2", "nas:3000", []string{"X-Forwarded-Proto", "https"}, false, 0},
		{"a proxy that tells the host", browser, "192.168.1.2", "nas:3000", []string{"X-Forwarded-Host", "slm.example.com"}, false, 0},
		{"Forwarded", browser, "192.168.1.2", "nas:3000", []string{"Forwarded", "for=192.168.1.20"}, false, 0},
		{"a public name, through the router", browser, "192.168.1.1", "slm.example.com:8443", nil, false, 0},
		{"a public address", browser, "192.168.1.1", "203.0.113.5:8443", nil, false, 0},
		{"TLS", browser, "192.168.1.20", "nas:3000", nil, true, 0},
		{"port 0", browser, "192.168.1.20", "nas:0", nil, false, 0},
		{"a port too big", browser, "192.168.1.20", "nas:99999", nil, false, 0},
		{"not a port", browser, "192.168.1.20", "nas:abc", nil, false, 0},
		{"no host", browser, "192.168.1.20", "", nil, false, 0},
	} {
		request := httptest.NewRequest("GET", "/index.html", nil)
		request.RemoteAddr = net.JoinHostPort(tt.remote, "5000")
		request.Host = tt.host
		for i := 0; i+1 < len(tt.headers); i += 2 {
			request.Header.Set(tt.headers[i], tt.headers[i+1])
		}
		if tt.tls {
			request.TLS = &tls.ConnectionState{}
		}
		port, ok := lanPortOf(request, tt.principal)
		if (tt.want > 0) != ok || port != tt.want {
			t.Errorf("%s: %d %v, not %d", tt.name, port, ok, tt.want)
		}
	}
}

// The port the browser or the console reached the app on is the one NXDock, CyberFoil and Sphaira
// are told, kept for the next start; until one is seen, the port the app listens on.
func TestLanPortIsTold(t *testing.T) {
	web, _, key := netWeb(t)
	handler := web.auth.middleware(web.withLanPort(web.router))
	ask := []byte("OWNFOIL_DISCOVER")
	told := func() (int, int) {
		t.Helper()
		answer, ok := web.ownfoilDiscoveryAnswerTo(ask, net.ParseIP("192.168.1.30"))
		var discovery ownfoilDiscoveryAnswer
		if !ok || json.Unmarshal(answer, &discovery) != nil {
			t.Fatal("no answer to the discovery")
		}
		return discovery.Port, web.netHello().Port
	}
	if discovery, hello := told(); discovery != 3000 || hello != 3000 {
		t.Fatalf("before any request, the port of the settings: %d %d", discovery, hello)
	}
	request := func(path string, remote string, host string, headers ...string) {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = remote + ":5000"
		r.Host = host
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}

	// bob opens the app in a browser at the port Docker publishes
	bob := &client{t: t, handler: handler}
	bob.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	if len(bob.cookies) != 1 {
		t.Fatal("bob signs in")
	}
	request("/index.html", "192.168.1.20", "192.168.1.10:8080", "Cookie", bob.cookies[0].Name+"="+bob.cookies[0].Value)
	if discovery, hello := told(); discovery != 8080 || hello != 8080 {
		t.Fatalf("the port of the browser: %d %d", discovery, hello)
	}
	if kept, _ := os.ReadFile(filepath.Join(web.dataFolder, netLanPortFile)); strings.TrimSpace(string(kept)) != "8080" {
		t.Fatalf("not kept for the next start: %q", kept)
	}

	// the health check, a dashboard with a key and a stranger do not change it
	request("/healthz", "127.0.0.1", "localhost:3000")
	request("/index.html", "172.18.0.5", "switch-library-manager-web:3000", "Authorization", basic("console", "slm_wrong"))
	request("/index.html", "192.168.1.66", "192.168.1.10:9999")
	if discovery, _ := told(); discovery != 8080 {
		t.Fatalf("changed by a request that does not tell it: %d", discovery)
	}

	// a console with its key, at another port
	request("/install/tinfoil", "192.168.1.30", "192.168.1.10:3030", "Authorization", basic("console", key))
	if discovery, hello := told(); discovery != 3030 || hello != 3030 {
		t.Fatalf("the port of the console: %d %d", discovery, hello)
	}

	// the next start says it before any request
	again := &Web{dataFolder: web.dataFolder}
	if port := again.netLanPort(3000); port != 3030 {
		t.Fatalf("after a restart: %d", port)
	}
	fresh := &Web{dataFolder: t.TempDir()}
	if port := fresh.netLanPort(3000); port != 3000 {
		t.Fatalf("with nothing seen yet: %d", port)
	}
	listeningPort.Store(41234)
	t.Cleanup(func() { listeningPort.Store(0) })
	if port := fresh.netLanPort(3000); port != 41234 {
		t.Fatalf("the port the app listens on: %d", port)
	}

}
