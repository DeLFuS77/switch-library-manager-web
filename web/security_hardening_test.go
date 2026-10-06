package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNextNeverLeavesTheSite(t *testing.T) {
	for next, want := range map[string]string{
		"/updates.html?page=2": "/updates.html?page=2",
		"/\t/evil.com":         "/index.html",
		"/%09/evil.com":        "/%09/evil.com",
		"//evil.com":           "/index.html",
		"/\\evil.com":          "/index.html",
		"https://evil.com/":    "/index.html",
		"/login.html":          "/index.html",
		"":                     "/index.html",
		"/\r\nSet-Cookie:x=1":  "/index.html",
	} {
		if got := safeNext(next); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", next, got, want)
		}
	}
}

func TestWithoutLoginOnlyTheLocalNetworkIsAnswered(t *testing.T) {
	web := usersWeb(t)
	handler := web.auth.middleware(web.router)
	request := func(remote string, headers ...string) int {
		r := httptest.NewRequest("GET", "/settings.html", nil)
		r.RemoteAddr = remote + ":1234"
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if code := request("192.168.1.20"); code != http.StatusOK {
		t.Fatalf("local network: %d", code)
	}
	if code := request("100.101.1.2"); code != http.StatusOK {
		t.Fatalf("VPN such as Tailscale: %d", code)
	}
	if code := request("203.0.113.9"); code != http.StatusForbidden {
		t.Fatalf("internet: %d", code)
	}
	// through a reverse proxy of the local network, the client is on the internet
	if code := request("172.17.0.1", "X-Forwarded-For", "192.168.1.5, 203.0.113.9"); code != http.StatusForbidden {
		t.Fatalf("through a proxy: %d", code)
	}
	if code := request("172.17.0.1", "X-Real-Ip", "203.0.113.9"); code != http.StatusForbidden {
		t.Fatalf("through a proxy (X-Real-Ip): %d", code)
	}
	if code := request("172.17.0.1", "Forwarded", `for="[2001:db8::1]:4711"`); code != http.StatusForbidden {
		t.Fatalf("through a proxy (Forwarded): %d", code)
	}
	if code := request("172.17.0.1", "X-Forwarded-For", "192.168.1.5"); code != http.StatusOK {
		t.Fatalf("through a proxy, from the local network: %d", code)
	}
	web.auth.remoteWithoutLogin = true
	if code := request("203.0.113.9"); code != http.StatusOK {
		t.Fatalf("allowed with %s: %d", REMOTE_WITHOUT_LOGIN_ENV, code)
	}
}

func TestClientAddressBehindAProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.17.0.1:5000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9")
	if got := clientIp(r); got != "203.0.113.9" {
		t.Fatalf("the address the proxy added: %q", got)
	}
	// from the internet, forwarded addresses are not believed
	r.RemoteAddr = "198.51.100.7:5000"
	if got := clientIp(r); got != "198.51.100.7" {
		t.Fatalf("forged header: %q", got)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	web := usersWeb(t)
	c := &client{t: t, handler: web.auth.middleware(web.router)}
	c.do("POST", "/users/create", url.Values{"name": {"alice"}, "password": {"password1"}})
	if code := c.do("GET", "/settings.html", nil).Code; code != http.StatusOK {
		t.Fatalf("logged in: %d", code)
	}
	stolen := append([]*http.Cookie(nil), c.cookies...)
	c.do("POST", "/logout", url.Values{})
	c.cookies = stolen
	if code := c.do("GET", "/settings.html", nil, "Accept", "text/html").Code; code == http.StatusOK {
		t.Fatal("a copy of the cookie still works after Log out")
	}
	// the ended sessions are remembered after a restart
	if !loadRevokedSessions(web.dataFolder).has(strings.Split(stolen[0].Value, ".")[2]) {
		t.Fatal("ended session not saved")
	}
}

func TestOnlyOneFirstAdministrator(t *testing.T) {
	web := usersWeb(t)
	if err := web.auth.users.add("alice", "password1", ROLE_ADMIN, true); err != nil {
		t.Fatal(err)
	}
	if err := web.auth.users.add("mallory", "password2", ROLE_ADMIN, true); err == nil {
		t.Fatal("a second first user was created")
	}
}

func TestRequestBodiesAreLimited(t *testing.T) {
	read := 0
	handler := withBodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffer := make([]byte, 1<<16)
		for {
			n, err := r.Body.Read(buffer)
			read += n
			if err != nil {
				return
			}
		}
	}))
	r := httptest.NewRequest("POST", "/login.html", strings.NewReader(strings.Repeat("a", 1<<20)))
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if read > maxLoginBody {
		t.Fatalf("read %d bytes of a login", read)
	}
	r = httptest.NewRequest("POST", "/login.html", strings.NewReader("x"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("multipart login: %d", w.Code)
	}
}
