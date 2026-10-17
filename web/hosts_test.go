package web

import (
	"net/http/httptest"
	"testing"
)

func TestAllowedHost(t *testing.T) {
	for host, want := range map[string]bool{
		"192.168.1.2:3030":   true,
		"[::1]:3030":         true,
		"tower:3030":         true,
		"nas.local":          true,
		"nas.home.arpa:3030": true,
		"localhost":          true,
		"":                   true,
		"evil.example.org":   false,
		"rebind.attacker.io": false,
	} {
		if got := allowedHost(host); got != want {
			t.Errorf("allowedHost(%q) = %v, want %v", host, got, want)
		}
	}
	saved := allowedHosts
	defer func() { allowedHosts = saved }()
	allowedHosts = []string{"games.mydomain.com", ".lab.net"}
	if !allowedHost("games.mydomain.com:443") || !allowedHost("nas.lab.net") || allowedHost("other.mydomain.com") {
		t.Error("SLM_ALLOWED_HOSTS not applied")
	}
}

func TestCloudflareHeadersOnlyThroughCloudflare(t *testing.T) {
	// a tunnel on the local network that adds no address of its own
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.17.0.5:1234"
	r.Header.Set("Cf-Connecting-Ip", "203.0.113.7")
	if got := clientIp(r); got != "203.0.113.7" {
		t.Errorf("tunnel: %s", got)
	}
	// a proxy of the local network reached by a client that adds the header itself
	r.Header.Set("X-Forwarded-For", "198.51.100.9")
	if got := clientIp(r); got != "198.51.100.9" {
		t.Errorf("forged header believed: %s", got)
	}
	// the proxy was reached from Cloudflare
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 104.16.0.1")
	if got := clientIp(r); got != "203.0.113.7" {
		t.Errorf("through Cloudflare: %s", got)
	}
}

func TestFullLimiterKeepsBlocks(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < maxLoginFailures; i++ {
		l.fail("ip:203.0.113.1")
	}
	l.fail("ip:203.0.113.2")
	l.evictOldest()
	if _, ok := l.failures["ip:203.0.113.1"]; !ok {
		t.Error("a blocked address was let go to make room")
	}
}
