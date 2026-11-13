package web

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
)

// Diagnostics > Open from other devices: the addresses of the local network the app answers
// on, each with a QR code to open it with the camera of a phone. In a container the addresses
// the app sees are the ones of the container, so only a hint is shown there. The addresses are
// not in the report to copy: it is pasted in public to ask for help.

// DeviceAddress is an address to open the app from another device.
type DeviceAddress struct {
	Url string
	// the network it is on, or what the address is
	Name string
}

// OtherDevices is the box of Diagnostics to open the app from other devices.
type OtherDevices struct {
	Addresses []DeviceAddress
	// the port the app listens on
	Port int
	// in a container: the addresses of the network are not known
	InContainer bool
	// a program of Windows: its firewall may block the app
	FirewallHint bool
}

// the port the app listens on, once it does
var listeningPort atomic.Int64

// netInterface is a network interface with its addresses.
type netInterface struct {
	Name     string
	Up       bool
	Loopback bool
	IPs      []net.IP
}

// the interfaces of virtual machines and containers, which other devices cannot reach
var virtualInterface = regexp.MustCompile(`(?i)^(docker|br-|veth|virbr|lxcbr|cni|flannel|vethernet \((wsl|default switch))`)

// maximum addresses shown: a computer with many networks would fill the box
const maxDeviceAddresses = 6

// deviceAddresses are the addresses of the local network to open the app from other devices:
// every IPv4 address of the interfaces that are up first, then one IPv6 address of each, not
// the loopback nor link-local ones.
func deviceAddresses(interfaces []netInterface, port int) []DeviceAddress {
	type found struct {
		ip   net.IP
		name string
	}
	ipv4, ipv6 := []found{}, []found{}
	seen := map[string]bool{}
	for _, iface := range interfaces {
		if !iface.Up || iface.Loopback || virtualInterface.MatchString(iface.Name) {
			continue
		}
		hasIPv6 := false
		for _, ip := range iface.IPs {
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || seen[ip.String()] {
				continue
			}
			seen[ip.String()] = true
			if ip.To4() != nil {
				ipv4 = append(ipv4, found{ip.To4(), iface.Name})
			} else if !hasIPv6 {
				hasIPv6 = true
				ipv6 = append(ipv6, found{ip, iface.Name})
			}
		}
	}
	// the addresses of home networks before the others (a VPN, a network of a provider)
	sort.SliceStable(ipv4, func(i, j int) bool { return ipv4[i].ip.IsPrivate() && !ipv4[j].ip.IsPrivate() })
	sort.SliceStable(ipv6, func(i, j int) bool { return ipv6[i].ip.IsPrivate() && !ipv6[j].ip.IsPrivate() })
	addresses := []DeviceAddress{}
	for _, address := range append(ipv4, ipv6...) {
		if len(addresses) == maxDeviceAddresses {
			break
		}
		// the app has no HTTPS of its own: a proxy with HTTPS has an address of its own
		addresses = append(addresses, DeviceAddress{Url: "http://" + net.JoinHostPort(address.ip.String(), fmt.Sprint(port)), Name: address.name})
	}
	return addresses
}

// a name of a computer that can be written in an address
var hostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$`)

// localName is the address of a computer by its name on the local network (name.local), which
// phones and computers find without knowing its IP; none when the name cannot be used.
func localName(hostname string, port int) string {
	hostname = strings.TrimSuffix(strings.TrimSuffix(hostname, "."), ".local")
	if !hostName.MatchString(hostname) || strings.EqualFold(hostname, "localhost") {
		return ""
	}
	return fmt.Sprintf("http://%s.local:%d", strings.ToLower(hostname), port)
}

// firewallHint tells whether to say the firewall may block the app: the programs of Windows,
// not a container.
func firewallHint(goos string, inContainer bool) bool {
	return goos == "windows" && !inContainer
}

// systemInterfaces are the network interfaces of this computer.
func systemInterfaces() []netInterface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := []netInterface{}
	for _, iface := range ifaces {
		found := netInterface{Name: iface.Name, Up: iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagRunning != 0, Loopback: iface.Flags&net.FlagLoopback != 0}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				found.IPs = append(found.IPs, ipNet.IP)
			}
		}
		result = append(result, found)
	}
	return result
}

// otherDevices is the box of Diagnostics to open the app from other devices: the port is the
// one of the settings until the app listens.
func otherDevices(port int, lang string) OtherDevices {
	if listening := int(listeningPort.Load()); listening > 0 {
		port = listening
	}
	inContainer := runningInContainer()
	var interfaces []netInterface
	hostname := ""
	if !inContainer {
		interfaces = systemInterfaces()
		hostname, _ = os.Hostname()
	}
	return otherDevicesFor(runtime.GOOS, inContainer, interfaces, hostname, port, lang)
}

// otherDevicesFor is the box to open the app from other devices on a system.
func otherDevicesFor(goos string, inContainer bool, interfaces []netInterface, hostname string, port int, lang string) OtherDevices {
	if port <= 0 {
		port = 3000
	}
	result := OtherDevices{Port: port, InContainer: inContainer, FirewallHint: firewallHint(goos, inContainer)}
	if inContainer {
		return result
	}
	result.Addresses = deviceAddresses(interfaces, port)
	if len(result.Addresses) > 0 {
		if address := localName(hostname, port); address != "" {
			result.Addresses = append(result.Addresses, DeviceAddress{Url: address, Name: i18n.Translate(lang, "Name of the computer")})
		}
	}
	return result
}
