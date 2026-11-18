package web

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// startOwnfoilDiscovery opens the port of the discovery on a free port, as the app does with
// installing over the network on, and gives the address to ask.
func startOwnfoilDiscovery(t *testing.T, web *Web) (*ownfoilDiscovery, *net.UDPAddr) {
	t.Helper()
	finder := &ownfoilDiscovery{ip: net.IPv4(127, 0, 0, 1)}
	web.followOwnfoilDiscovery(finder)
	t.Cleanup(finder.stop)
	if finder.conn == nil {
		t.Fatal("the port of the discovery is not open with installing over the network on")
	}
	return finder, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: finder.conn.LocalAddr().(*net.UDPAddr).Port}
}

// askShops sends a datagram as an installer does, and gives the answer, if one comes.
func askShops(t *testing.T, to *net.UDPAddr, question string) ([]byte, bool) {
	t.Helper()
	conn, err := net.DialUDP("udp4", nil, to)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(question)); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	buffer := make([]byte, 4097)
	n, err := conn.Read(buffer)
	if err != nil {
		return nil, false
	}
	return buffer[:n], true
}

// The answer is the one CyberFoil and Sphaira take: magic, a UUID, a name, a version, the port of
// the web server and public, in one small datagram, with the same uid as the hello of the shop and
// no key nor password.
func TestOwnfoilDiscoveryAnswers(t *testing.T) {
	web, admin, key := netWeb(t)
	_, address := startOwnfoilDiscovery(t, web)

	for _, question := range []string{"OWNFOIL_DISCOVER", "OWNFOIL_DISCOVER\n", " OWNFOIL_DISCOVER\r\n"} {
		data, ok := askShops(t, address, question)
		if !ok {
			t.Fatalf("no answer to %q", question)
		}
		// Sphaira reads 1023 bytes at most
		if len(data) > 1023 {
			t.Fatalf("too long for Sphaira: %d bytes", len(data))
		}
		fields := map[string]any{}
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatalf("not an object: %s", data)
		}
		if len(fields) != 6 {
			t.Fatalf("only magic, uid, name, version, port and public: %s", data)
		}
		var answer ownfoilDiscoveryAnswer
		json.Unmarshal(data, &answer)
		if answer.Magic != "OWNFOIL" || !uuidPattern.MatchString(answer.Uid) || answer.Name == "" || answer.Version != settings.SLM_WEB_VERSION {
			t.Fatalf("the answer: %s", data)
		}
		if port, ok := fields["port"].(float64); !ok || int(port) != settings.ReadSettings(web.dataFolder).Port || port < 1 || port > 65535 {
			t.Fatalf("the port of the web server: %s", data)
		}
		if public, ok := fields["public"].(bool); !ok || public {
			t.Fatalf("a shop that needs a login: %s", data)
		}
		if strings.Contains(string(data), key) || strings.Contains(string(data), "slm_") || strings.Contains(string(data), "secret") {
			t.Fatalf("a key in the answer: %s", data)
		}
	}

	// the same uid as the hello of the shop, so Sphaira knows the shop it found as the one it saved
	data, _ := askShops(t, address, "OWNFOIL_DISCOVER")
	var answer ownfoilDiscoveryAnswer
	json.Unmarshal(data, &answer)
	console := &client{t: t, handler: admin.handler}
	got := console.do("OPTIONS", "/", nil, "Authorization", basic("console", key))
	var hello netOwnfoilHello
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &hello) != nil || hello.Uid != answer.Uid {
		t.Fatalf("the hello of the shop (%s) and the discovery (%s)", got.Body.String(), answer.Uid)
	}
	if again, _ := askShops(t, address, "OWNFOIL_DISCOVER"); !strings.Contains(string(again), answer.Uid) {
		t.Fatalf("the uid changed: %s", again)
	}
}

// Anything but the question of the installers gets no answer, and the app goes on answering.
func TestOwnfoilDiscoveryIgnoresOtherDatagrams(t *testing.T) {
	web, _, _ := netWeb(t)
	_, address := startOwnfoilDiscovery(t, web)
	for _, question := range []string{"OWNFOIL_DISCOVERX", "ownfoil_discover", "OWNFOIL", "NXDOCK-DISCOVER 1", "OWNFOIL_DISCOVER OWNFOIL_DISCOVER",
		`{"magic":"OWNFOIL"}`, "\x00\xff\x10", strings.Repeat("A", 300), "OWNFOIL_DISCOVER\x00"} {
		if data, ok := askShops(t, address, question); ok {
			t.Fatalf("an answer to %q: %s", question, data)
		}
	}
	if _, ok := askShops(t, address, "OWNFOIL_DISCOVER"); !ok {
		t.Fatal("no answer after the datagrams it ignored")
	}
}

// With installing over the network off nothing answers, and the port is let go; turned on again,
// the app answers again.
func TestOwnfoilDiscoveryOnlyWhileOn(t *testing.T) {
	web, _, _ := netWeb(t)
	finder, address := startOwnfoilDiscovery(t, web)
	ask := []byte("OWNFOIL_DISCOVER")

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	if _, ok := web.ownfoilDiscoveryAnswerTo(ask, net.ParseIP("192.168.1.30")); ok {
		t.Fatal("an answer with installing over the network off")
	}
	// before the setting is looked at again, the port still open answers nothing
	if data, ok := askShops(t, address, "OWNFOIL_DISCOVER"); ok {
		t.Fatalf("an answer with installing over the network off: %s", data)
	}
	web.followOwnfoilDiscovery(finder)
	if finder.conn != nil {
		t.Fatal("the port is kept with installing over the network off")
	}
	if probe, err := net.ListenUDP("udp4", address); err != nil {
		t.Fatalf("the port is not let go: %v", err)
	} else {
		probe.Close()
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = true })
	web.followOwnfoilDiscovery(finder)
	if finder.conn == nil {
		t.Fatal("the port is not opened again")
	}
	again := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: finder.conn.LocalAddr().(*net.UDPAddr).Port}
	if _, ok := askShops(t, again, "OWNFOIL_DISCOVER"); !ok {
		t.Fatal("no answer once turned on again")
	}
}

// Only the local network is answered, unless installing over the network answers outside it too,
// and never without users.
func TestOwnfoilDiscoveryOnlyOnTheLocalNetwork(t *testing.T) {
	web, _, _ := netWeb(t)
	ask := []byte("OWNFOIL_DISCOVER")
	for _, ip := range []string{"192.168.178.40", "10.0.0.7", "172.17.0.1", "169.254.10.20", "127.0.0.1", "100.100.1.2"} {
		if _, ok := web.ownfoilDiscoveryAnswerTo(ask, net.ParseIP(ip)); !ok {
			t.Fatalf("no answer to %s, on the local network", ip)
		}
	}
	for _, ip := range []string{"203.0.113.9", "8.8.8.8", "1.1.1.1"} {
		if _, ok := web.ownfoilDiscoveryAnswerTo(ask, net.ParseIP(ip)); ok {
			t.Fatalf("an answer to %s, outside the local network", ip)
		}
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Remote = true })
	if _, ok := web.ownfoilDiscoveryAnswerTo(ask, net.ParseIP("203.0.113.9")); !ok {
		t.Fatal("no answer outside the local network with installing over the network answering there")
	}

	// without users installing over the network answers nothing, nor does its discovery
	alone := usersWeb(t)
	settings.UpdateSettings(alone.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = true })
	if _, ok := alone.ownfoilDiscoveryAnswerTo(ask, net.ParseIP("192.168.178.40")); ok {
		t.Fatal("an answer without users")
	}
}

// A port taken by another program (an Ownfoil on the same computer) is told once in the log, and
// the app goes on without it.
func TestOwnfoilDiscoveryPortTaken(t *testing.T) {
	web, _, _ := netWeb(t)
	core, logs := observer.New(zap.InfoLevel)
	web.sugarLogger = zap.New(core).Sugar()
	other, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	finder := &ownfoilDiscovery{ip: net.IPv4(127, 0, 0, 1), port: other.LocalAddr().(*net.UDPAddr).Port}
	t.Cleanup(finder.stop)
	for i := 0; i < 3; i++ {
		web.followOwnfoilDiscovery(finder)
	}
	if finder.conn != nil {
		t.Fatal("the port of another program was taken")
	}
	told := logs.FilterMessageSnippet("CyberFoil and Sphaira will not find the library by themselves").Len()
	if told != 1 {
		t.Fatalf("told %d times in the log, not once", told)
	}
	// let go by the other program, the app takes it
	other.Close()
	web.followOwnfoilDiscovery(finder)
	if finder.conn == nil {
		t.Fatal("the port let go by the other program is not taken")
	}
}

// Settings › Install over the network says it under the address for Tinfoil, CyberFoil and
// Sphaira, with its help.
func TestOwnfoilDiscoveryInSettings(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleSettings()
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/settings.html", nil))
	body := page.Body.String()
	line := strings.Index(body, `<span class="help-name">CyberFoil and Sphaira find it by themselves on your network</span>`)
	if line < 0 || line < strings.Index(body, `data-net-line="tinfoil"`) || !strings.Contains(body, `class="net-discover-dot"`) ||
		!strings.Contains(body[line:], `<div class="form-text">They ask on the local network and this library answers with its address`) {
		t.Fatal("the line of the discovery, under the address for Tinfoil, CyberFoil and Sphaira, with its help")
	}
}
