package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestDeviceAddressesOfTheLocalNetwork(t *testing.T) {
	ips := func(addresses ...string) []net.IP {
		result := []net.IP{}
		for _, address := range addresses {
			result = append(result, net.ParseIP(address))
		}
		return result
	}
	interfaces := []netInterface{
		{Name: "lo", Up: true, Loopback: true, IPs: ips("127.0.0.1", "::1")},
		{Name: "VPN", Up: true, IPs: ips("100.64.1.2")},
		{Name: "Wi-Fi", Up: true, IPs: ips("fe80::1", "2001:db8::5", "2001:db8::6", "192.168.1.20", "169.254.3.4")},
		{Name: "Ethernet", Up: false, IPs: ips("192.168.1.30")},
		{Name: "docker0", Up: true, IPs: ips("172.17.0.1")},
		{Name: "vEthernet (WSL)", Up: true, IPs: ips("172.20.0.1")},
		{Name: "Ethernet 2", Up: true, IPs: ips("10.0.0.7", "192.168.1.20")},
	}
	got := deviceAddresses(interfaces, 8080)
	want := []DeviceAddress{
		// IPv4 first, the home networks before the others, then one IPv6 of each network
		{Url: "http://192.168.1.20:8080", Name: "Wi-Fi"},
		{Url: "http://10.0.0.7:8080", Name: "Ethernet 2"},
		{Url: "http://100.64.1.2:8080", Name: "VPN"},
		{Url: "http://[2001:db8::5]:8080", Name: "Wi-Fi"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the addresses:\n%+v\nwant\n%+v", got, want)
	}

	// a computer with many networks does not fill the box
	many := []netInterface{}
	for i := 1; i <= 10; i++ {
		many = append(many, netInterface{Name: "net", Up: true, IPs: ips("192.168.0." + string(rune('0'+i%10)))})
	}
	if got := deviceAddresses(many, 3000); len(got) != maxDeviceAddresses {
		t.Fatalf("%d addresses", len(got))
	}
	if got := deviceAddresses(interfaces[:1], 3000); len(got) != 0 {
		t.Fatalf("only this computer: %+v", got)
	}
}

func TestLocalNameOfTheComputer(t *testing.T) {
	for hostname, want := range map[string]string{
		"DESKTOP-PC":   "http://desktop-pc.local:3000",
		"nas.local":    "http://nas.local:3000",
		"nas.local.":   "http://nas.local:3000",
		"localhost":    "",
		"":             "",
		"my_pc":        "",
		"nas.home.lan": "",
	} {
		if got := localName(hostname, 3000); got != want {
			t.Errorf("%q: %q, want %q", hostname, got, want)
		}
	}
}

func TestOtherDevicesDependOnTheSystem(t *testing.T) {
	interfaces := []netInterface{{Name: "Wi-Fi", Up: true, IPs: []net.IP{net.ParseIP("192.168.1.20")}}}

	windows := otherDevicesFor("windows", false, interfaces, "DESKTOP-PC", 3000, "en")
	if !windows.FirewallHint || windows.InContainer || len(windows.Addresses) != 2 || windows.Addresses[1] != (DeviceAddress{Url: "http://desktop-pc.local:3000", Name: "Name of the computer"}) {
		t.Fatalf("the program of Windows: %+v", windows)
	}
	if linux := otherDevicesFor("linux", false, interfaces, "nas", 8080, "en"); linux.FirewallHint || linux.Addresses[0].Url != "http://192.168.1.20:8080" {
		t.Fatalf("the program of Linux: %+v", linux)
	}
	// in a container the addresses are the ones of the container: none is shown
	if docker := otherDevicesFor("windows", true, interfaces, "abc123", 3000, "en"); !docker.InContainer || docker.FirewallHint || len(docker.Addresses) != 0 {
		t.Fatalf("in a container: %+v", docker)
	}
	// without a network, not the name of the computer alone either
	if alone := otherDevicesFor("darwin", false, nil, "mac", 0, "en"); len(alone.Addresses) != 0 || alone.Port != 3000 {
		t.Fatalf("without a network: %+v", alone)
	}
	if !firewallHint("windows", false) || firewallHint("windows", true) || firewallHint("darwin", false) {
		t.Fatal("the hint of the firewall")
	}
}

// The box is on the page of Diagnostics, for administrators, and the addresses are not in the
// report, which is pasted in public.
func TestDiagnosticsShowOtherDevices(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleDiagnostics()
	listeningPort.Store(41234)
	t.Cleanup(func() { listeningPort.Store(0) })

	data := web.diagnostics(httptest.NewRequest(http.MethodGet, "/diagnostics.html", nil), "en")
	if data.OtherDevices.Port != 41234 {
		t.Fatalf("the port the app listens on: %+v", data.OtherDevices)
	}
	for _, address := range data.OtherDevices.Addresses {
		if host, _, _ := net.SplitHostPort(strings.TrimPrefix(address.Url, "http://")); strings.Contains(data.Report, host) {
			t.Fatalf("the report has the address %s:\n%s", address.Url, data.Report)
		}
	}

	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/diagnostics.html", nil))
	page := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(page, "data-other-devices") || !strings.Contains(page, "Open from other devices") || !strings.Contains(page, "bi-phone") {
		t.Fatalf("the box of other devices: %d", recorder.Code)
	}
	if _, isAdminPage := adminOnlyPages["/diagnostics.html"]; !isAdminPage {
		t.Fatal("Diagnostics is for administrators")
	}
}

// The box as each system shows it: the addresses with their code and the hint of the firewall on
// Windows; in a container only the hint of the address of the NAS, and the one of the page.
func TestOtherDevicesBoxOfEachSystem(t *testing.T) {
	set, err := parseTemplates(os.DirFS(".."), "resources/layout.html", "resources/partials/*.html", "resources/pages/diagnostics.html")
	if err != nil {
		t.Fatal(err)
	}
	render := func(devices OtherDevices) string {
		t.Helper()
		var out strings.Builder
		data := DiagnosticsPageData{GlobalPageData: GlobalPageData{Auth: AuthInfo{IsAdmin: true, Role: ROLE_ADMIN}, IsKeysFileAvailable: true, Page: "diagnostics"}, OtherDevices: devices}
		if err := set["en"].ExecuteTemplate(&out, "layout", data); err != nil {
			t.Fatal(err)
		}
		page := out.String()
		start := strings.Index(page, "data-other-devices")
		if start < 0 {
			t.Fatal("no box of other devices")
		}
		return page[start : start+strings.Index(page[start:], "</section>")]
	}
	interfaces := []netInterface{{Name: "Wi-Fi", Up: true, IPs: []net.IP{net.ParseIP("192.168.1.20")}}}

	box := render(otherDevicesFor("windows", false, interfaces, "pc", 3000, "en"))
	for _, want := range []string{`data-device-address="http://192.168.1.20:3000"`, `data-copy="http://192.168.1.20:3000"`, "data-device-qr", `aria-pressed="true"`, "http://pc.local:3000", "Windows Firewall"} {
		if !strings.Contains(box, want) {
			t.Errorf("the box on Windows has no %s", want)
		}
	}
	if box := render(otherDevicesFor("linux", false, interfaces, "nas", 3000, "en")); strings.Contains(box, "Windows Firewall") {
		t.Error("the hint of the firewall of Windows on Linux")
	}
	docker := render(otherDevicesFor("linux", true, interfaces, "abc", 8080, "en"))
	if strings.Contains(docker, "192.168.1.20") || !strings.Contains(docker, "in the container it listens on 8080") || !strings.Contains(docker, "data-device-page hidden") {
		t.Errorf("the box in a container: %s", docker)
	}
	if none := render(otherDevicesFor("windows", false, nil, "pc", 3000, "en")); !strings.Contains(none, "No network was found") {
		t.Errorf("the box without a network: %s", none)
	}
}
