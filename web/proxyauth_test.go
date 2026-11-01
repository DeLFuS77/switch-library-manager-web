package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// proxyAddress is the proxy with a sign-in portal of the tests, set in SLM_TRUSTED_PROXIES.
const proxyAddress = "10.0.0.2"

// portalWeb is a web with the user pages, alice an administrator and bob a player, the proxy of
// proxyAddress trusted, and the portal set as options says.
func portalWeb(t *testing.T, options settings.PortalOptions) *Web {
	t.Helper()
	saved := trustedProxyNets
	t.Cleanup(func() { trustedProxyNets = saved })
	trustedProxyNets = parseTrustedProxies(proxyAddress)
	web := usersWeb(t)
	web.auth.users.Add("alice", "alpha-secret-11", ROLE_ADMIN)
	web.auth.users.Add("bob", "bravo-secret-22", ROLE_VIEWER)
	web.auth.onLogin = web.recordLogin
	web.auth.onKey = web.recordKeyAttempt
	web.auth.onPortal = web.recordPortal
	web.auth.portalSettings = func() settings.PortalOptions { return settings.ReadSettings(web.dataFolder).Portal }
	setPortal(web, options)
	return web
}

func setPortal(web *Web, options settings.PortalOptions) {
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Portal = options })
}

// activityCount counts the entries of the activity log with an action and a detail.
func activityCount(web *Web, action string, detail string) int {
	n := 0
	for _, entry := range web.activities().list() {
		if entry.Action == action && entry.Detail == detail {
			n++
		}
	}
	return n
}

// headerPortal is the header of Authelia and Authentik, turned on.
var headerPortal = settings.PortalOptions{Header: true}

// A proxy listed in SLM_TRUSTED_PROXIES names the user, who opens the app with their role and
// without the password, with no session of the app; told once a day.
func TestPortalHeaderFromATrustedProxy(t *testing.T) {
	web := portalWeb(t, headerPortal)
	proxy := &client{t: t, handler: web.auth.middleware(web.router), ip: proxyAddress}
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "alice").Code; code != http.StatusOK {
		t.Fatalf("an administrator named by the proxy: %v", code)
	}
	if len(proxy.cookies) != 0 {
		t.Fatal("the portal signs the person in with every request: no session of the app")
	}
	if code := proxy.do("GET", "/index.html", nil, "Remote-User", "BOB").Code; code != http.StatusOK {
		t.Fatalf("a player, in any case: %v", code)
	}
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "bob").Code; code != http.StatusForbidden {
		t.Fatalf("a player keeps the role of a player: %v", code)
	}
	if location := proxy.do("GET", "/login.html", nil, "Remote-User", "alice").Header().Get("Location"); location != "/index.html" {
		t.Fatalf("the login page is not needed: %q", location)
	}
	proxy.do("GET", "/index.html", nil, "Remote-User", "alice")
	if n := activityCount(web, ACTION_PORTAL_PROXY, ""); n != 2 {
		t.Fatalf("each user signed in through the proxy is told once a day: %d", n)
	}
	if user, _ := web.auth.users.Get("alice"); user.LastLogin.IsZero() {
		t.Fatal("the sign-in is shown in Users")
	}
}

// The header from any other address never signs anybody in: not from a computer of the local
// network, nor from the internet, nor from Cloudflare (anybody can send anything through it),
// nor with a forwarded address that says it is the proxy. It is told once an hour by address.
func TestPortalHeaderFromAnotherAddressIsIgnored(t *testing.T) {
	web := portalWeb(t, headerPortal)
	handler := web.auth.middleware(web.router)
	trustedProxyNets = parseTrustedProxies(proxyAddress + ", cloudflare")
	for _, address := range []string{"192.168.1.10", "203.0.113.5", "162.158.1.1", "127.0.0.1"} {
		c := &client{t: t, handler: handler, ip: address}
		for range 3 {
			page := c.do("GET", "/settings.html", nil, "Remote-User", "alice", "Accept", "text/html", "X-Forwarded-For", proxyAddress)
			if page.Code != http.StatusSeeOther || !strings.HasPrefix(page.Header().Get("Location"), "/login.html") {
				t.Fatalf("from %s the header is ignored: %v %q", address, page.Code, page.Header().Get("Location"))
			}
			if code := c.do("POST", "/sync", nil, "Remote-User", "alice", "Remote-Groups", "admins").Code; code != http.StatusUnauthorized {
				t.Fatalf("from %s an API client is not signed in: %v", address, code)
			}
		}
	}
	if n := activityCount(web, ACTION_LOGIN_FAIL, portalUntrustedDetail); n != 4 {
		t.Fatalf("a header from an address not trusted is told once an hour by address: %d", n)
	}
	for _, entry := range web.activities().list() {
		if entry.Action == ACTION_LOGIN_FAIL && entry.IP == proxyAddress {
			t.Fatal("the address told is the one of the connection, not the one it forwards")
		}
	}
	if web.auth.portalView().LastProblem.Result != "untrusted" {
		t.Fatal("Settings shows the last header not believed")
	}

	// sent twice, the proxy may have added its own after the one of the client
	proxy := &client{t: t, handler: handler, ip: proxyAddress}
	twice := httptest.NewRequest("GET", "/settings.html", nil)
	twice.RemoteAddr = proxyAddress + ":1234"
	twice.Header.Add("Remote-User", "bob")
	twice.Header.Add("Remote-User", "alice")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, twice)
	if recorder.Code == http.StatusOK {
		t.Fatal("a header sent twice names nobody")
	}
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "alice\x01").Code; code == http.StatusOK {
		t.Fatal("a header with control characters names nobody")
	}

	// without SLM_TRUSTED_PROXIES no proxy is believed for the header, not even of the local network
	trustedProxyNets = parseTrustedProxies("")
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "alice").Code; code == http.StatusOK {
		t.Fatal("a header with no proxy set")
	}
	trustedProxyNets = parseTrustedProxies("cloudflare")
	if headerProxiesSet() {
		t.Fatal("Cloudflare is never a proxy believed for the header")
	}
}

// Off by default: the header from a trusted proxy is ignored, and nothing is told.
func TestPortalIsOffByDefault(t *testing.T) {
	web := portalWeb(t, settings.PortalOptions{})
	proxy := &client{t: t, handler: web.auth.middleware(web.router), ip: proxyAddress}
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "alice").Code; code != http.StatusUnauthorized {
		t.Fatalf("the header means nothing until it is turned on: %v", code)
	}
	if len(web.activities().list()) != 0 {
		t.Fatal("nothing is told")
	}
}

// A person the portal let in without a user of the app gets the login page, which says who the
// portal named; no user is made unless the options say so.
func TestPortalPersonWithoutUser(t *testing.T) {
	web := portalWeb(t, headerPortal)
	proxy := &client{t: t, handler: web.auth.middleware(web.router), ip: proxyAddress}
	page := proxy.do("GET", "/settings.html", nil, "Remote-User", "carol", "Accept", "text/html")
	if page.Code != http.StatusSeeOther || !strings.HasPrefix(page.Header().Get("Location"), "/login.html") {
		t.Fatalf("a person without user logs in: %v %q", page.Code, page.Header().Get("Location"))
	}
	login := proxy.do("GET", "/login.html", nil, "Remote-User", "carol")
	if login.Code != http.StatusOK {
		t.Fatalf("the login page says who the portal named: %v", login.Code)
	}
	if web.auth.users.Count() != 2 {
		t.Fatal("no user is made by default")
	}
	if n := activityCount(web, ACTION_LOGIN_FAIL, portalUnknownDetail); n != 1 {
		t.Fatalf("told once an hour: %d", n)
	}
	// the password of the app still works for them
	if code := proxy.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}}, "Remote-User", "carol").Code; code != http.StatusSeeOther || len(proxy.cookies) != 1 {
		t.Fatalf("the login page signs in with a password: %v", code)
	}
	if code := proxy.do("GET", "/index.html", nil, "Remote-User", "carol").Code; code != http.StatusOK {
		t.Fatalf("then with the session: %v", code)
	}
}

// With the option, a person without a user gets one, never an administrator nor an editor,
// without a password; once.
func TestPortalMakesUsersWhenAsked(t *testing.T) {
	web := portalWeb(t, settings.PortalOptions{Header: true, NewUsers: ROLE_VIEWER})
	proxy := &client{t: t, handler: web.auth.middleware(web.router), ip: proxyAddress}
	for range 3 {
		if code := proxy.do("GET", "/index.html", nil, "Remote-User", "carol", "Remote-Email", "carol@example.com").Code; code != http.StatusOK {
			t.Fatalf("a new user opens the app: %v", code)
		}
	}
	carol, ok := web.auth.users.Get("carol")
	if !ok || carol.Role != ROLE_VIEWER || carol.Email != "carol@example.com" || carol.HasPassword() || web.auth.users.Count() != 3 {
		t.Fatalf("one player made, with the email and no password: %+v (%d users)", carol, web.auth.users.Count())
	}
	if _, ok := web.auth.users.Verify("carol", ""); ok {
		t.Fatal("a user without a password cannot sign in with an empty one")
	}
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "carol").Code; code != http.StatusForbidden {
		t.Fatalf("a user made by the portal is no administrator: %v", code)
	}
	if n := activityCount(web, ACTION_USER_NEW, "carol: viewer"); n != 1 {
		t.Fatalf("the user made is told: %d", n)
	}

	// a role that would manage something is never given
	for _, role := range []string{ROLE_ADMIN, ROLE_EDITOR, "root"} {
		setPortal(web, settings.PortalOptions{Header: true, NewUsers: role})
		if code := proxy.do("GET", "/index.html", nil, "Remote-User", "dave-"+role).Code; code != http.StatusUnauthorized {
			t.Fatalf("no user is made as %s: %v", role, code)
		}
	}

	// a name that cannot be the one of a user: by the email, and matched by it the next time
	setPortal(web, settings.PortalOptions{Header: true, NewUsers: ROLE_GUEST})
	for range 2 {
		proxy.do("GET", "/index.html", nil, "Remote-User", "Émile Zola", "Remote-Email", "emile@example.com")
	}
	if user, ok := web.auth.users.FindByEmail("EMILE@example.com"); !ok || user.Name != "emile" || user.Role != ROLE_GUEST || web.auth.users.Count() != 4 {
		t.Fatalf("named after the email: %+v (%d users)", user, web.auth.users.Count())
	}
	// without an email to find them again, none is made
	if code := proxy.do("GET", "/index.html", nil, "Remote-User", "Émile Zola").Code; code != http.StatusUnauthorized || web.auth.users.Count() != 4 {
		t.Fatalf("a user is made only when the person is found again: %v", code)
	}
}

// The members of the group of the administrators are administrators, while the portal says so;
// the role stored stays as it is.
func TestPortalAdministratorsGroup(t *testing.T) {
	web := portalWeb(t, settings.PortalOptions{Header: true, AdminGroup: "slm-admins"})
	handler := web.auth.middleware(web.router)
	proxy := &client{t: t, handler: handler, ip: proxyAddress}
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "bob", "Remote-Groups", "family,slm-admins").Code; code != http.StatusOK {
		t.Fatalf("a member of the group (Authelia) is an administrator: %v", code)
	}
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "bob", "Remote-Groups", "family|SLM-Admins").Code; code != http.StatusOK {
		t.Fatalf("a member of the group (Authentik) is an administrator: %v", code)
	}
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "bob", "Remote-Groups", "family,slm-admins-not").Code; code != http.StatusForbidden {
		t.Fatalf("another group is not: %v", code)
	}
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "bob").Code; code != http.StatusForbidden {
		t.Fatalf("without the group, the role of the user: %v", code)
	}
	if user, _ := web.auth.users.Get("bob"); user.Role != ROLE_VIEWER {
		t.Fatal("the group does not change the role stored")
	}
	if n := activityCount(web, ACTION_PORTAL_PROXY, ROLE_ADMIN); n != 1 {
		t.Fatalf("the sign-in as administrator by the group is told: %d", n)
	}
	other := &client{t: t, handler: handler, ip: "192.168.1.10"}
	if code := other.do("GET", "/settings.html", nil, "Remote-User", "bob", "Remote-Groups", "slm-admins").Code; code != http.StatusUnauthorized {
		t.Fatalf("the group from another address: %v", code)
	}
	// a user made by the portal is a player, an administrator only by the group
	setPortal(web, settings.PortalOptions{Header: true, AdminGroup: "slm-admins", NewUsers: ROLE_VIEWER})
	if code := proxy.do("GET", "/settings.html", nil, "Remote-User", "erin", "Remote-Groups", "slm-admins").Code; code != http.StatusOK {
		t.Fatalf("a new member of the group: %v", code)
	}
	if user, _ := web.auth.users.Get("erin"); user.Role != ROLE_VIEWER {
		t.Fatalf("made as a player: %q", user.Role)
	}
}

// A person is matched by name or email, by name only, or by the email set for each user only.
func TestPortalMatchesByNameOrEmail(t *testing.T) {
	web := portalWeb(t, headerPortal)
	admin := &client{t: t, handler: web.auth.middleware(web.router), ip: proxyAddress}
	admin.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	if location := admin.do("POST", "/users/email", url.Values{"name": {"alice"}, "email": {"Alice@Example.com"}}).Header().Get("Location"); location != "/users.html?done=email" {
		t.Fatalf("an administrator sets the email of a user: %q", location)
	}
	if location := admin.do("POST", "/users/email", url.Values{"name": {"bob"}, "email": {"alice@example.com"}}).Header().Get("Location"); !strings.Contains(location, "error=") {
		t.Fatalf("no two users have one email: %q", location)
	}
	if location := admin.do("POST", "/users/email", url.Values{"name": {"bob"}, "email": {"not an email"}}).Header().Get("Location"); !strings.Contains(location, "error=") {
		t.Fatalf("an email is checked: %q", location)
	}

	proxy := &client{t: t, handler: admin.handler, ip: proxyAddress}
	signedIn := func(headers ...string) bool {
		return proxy.do("GET", "/settings.html", nil, headers...).Code == http.StatusOK
	}
	if !signedIn("Remote-User", "ali", "Remote-Email", "alice@example.com") || !signedIn("Remote-User", "alice") {
		t.Fatal("by name or by email")
	}
	setPortal(web, settings.PortalOptions{Header: true, MatchBy: MATCH_NAME})
	if signedIn("Remote-User", "ali", "Remote-Email", "alice@example.com") || !signedIn("Remote-User", "alice") {
		t.Fatal("by name only")
	}
	setPortal(web, settings.PortalOptions{Header: true, MatchBy: MATCH_EMAIL})
	if !signedIn("Remote-User", "ali", "Remote-Email", "alice@example.com") || signedIn("Remote-User", "alice") {
		t.Fatal("by email only: a name is not enough")
	}
	// other names of the headers
	setPortal(web, settings.PortalOptions{Header: true, UserHeader: "X-Authentik-Username", EmailHeader: "X-Authentik-Email"})
	if !signedIn("X-Authentik-Username", "alice") || signedIn("Remote-User", "alice") {
		t.Fatal("the header set is the one read")
	}
	// a non-member cannot set emails
	viewer := &client{t: t, handler: admin.handler}
	viewer.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	if code := viewer.do("POST", "/users/email", url.Values{"name": {"bob"}, "email": {"bob@example.com"}}).Code; code != http.StatusForbidden {
		t.Fatalf("only administrators set emails: %v", code)
	}
}

// While there is no user the portal does nothing: it never makes the first user nor skips the
// setup code, whatever its options.
func TestPortalNeverSkipsTheSetupCode(t *testing.T) {
	saved := trustedProxyNets
	t.Cleanup(func() { trustedProxyNets = saved })
	trustedProxyNets = parseTrustedProxies(proxyAddress)
	web := usersWeb(t)
	web.auth.portalSettings = func() settings.PortalOptions {
		return settings.PortalOptions{Header: true, NewUsers: ROLE_VIEWER, AdminGroup: "slm-admins"}
	}
	proxy := &client{t: t, handler: web.auth.middleware(web.router), ip: proxyAddress}
	proxy.do("GET", "/index.html", nil, "Remote-User", "mallory", "Remote-Groups", "slm-admins")
	if web.auth.users.Count() != 0 || web.auth.Enabled() {
		t.Fatal("no user is made while login is off")
	}
	created := proxy.do("POST", "/users/create", url.Values{"name": {"mallory"}, "password": {"mallory-secret-1"}}, "Remote-User", "mallory", "Remote-Groups", "slm-admins")
	if !strings.Contains(created.Header().Get("Location"), "error=") || web.auth.Enabled() {
		t.Fatalf("the first administrator still needs the setup code: %q", created.Header().Get("Location"))
	}
	// from the internet through the proxy, login off answers as before: not at all
	if code := proxy.do("GET", "/index.html", nil, "Remote-User", "mallory", "X-Forwarded-For", "203.0.113.9").Code; code != http.StatusForbidden {
		t.Fatalf("the header does not open the app to the internet: %v", code)
	}
}

// The passwords, basic authentication and the API keys keep working with the portal on.
func TestPortalKeepsPasswordsAndAPIKeys(t *testing.T) {
	web := portalWeb(t, settings.PortalOptions{Header: true, AdminGroup: "slm-admins"})
	handler := web.auth.middleware(web.router)
	admin := &client{t: t, handler: handler}
	admin.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	if len(admin.cookies) != 1 || admin.do("GET", "/settings.html", nil).Code != http.StatusOK {
		t.Fatal("the login page")
	}
	key := createKey(t, admin, "Home Assistant", ROLE_VIEWER)
	api := &client{t: t, handler: handler, ip: proxyAddress}
	if code := api.do("GET", "/index.html", nil, "Authorization", bearer(key.Key)).Code; code != http.StatusOK {
		t.Fatalf("an API key through the proxy: %v", code)
	}
	if code := api.do("POST", "/sync", nil, "Authorization", bearer(key.Key)).Code; code != http.StatusForbidden {
		t.Fatalf("a read-only key stays read-only: %v", code)
	}
	if code := api.do("GET", "/index.html", nil, "Authorization", basic("bob", "bravo-secret-22")).Code; code != http.StatusOK {
		t.Fatalf("basic authentication: %v", code)
	}
	if code := api.do("GET", "/index.html", nil, "Authorization", basic("bob", "wrong")).Code; code != http.StatusUnauthorized {
		t.Fatalf("a wrong password: %v", code)
	}
	// a key of another sends no header of the user: the key decides
	if code := api.do("POST", "/sync", nil, "Authorization", bearer(apiKeyPrefix+strings.Repeat("C", apiKeyLength))).Code; code != http.StatusUnauthorized {
		t.Fatalf("a wrong key: %v", code)
	}
}

// Log out sends a person signed in by the portal to the exit of the portal, or tells them the
// portal keeps the session.
func TestPortalLogout(t *testing.T) {
	web := portalWeb(t, headerPortal)
	proxy := &client{t: t, handler: web.auth.middleware(web.router), ip: proxyAddress}
	out := proxy.do("POST", "/logout", nil, "Remote-User", "alice")
	if out.Header().Get("Location") != "/login.html?portal=out" {
		t.Fatalf("without the exit of the portal: %q", out.Header().Get("Location"))
	}
	if page := proxy.do("GET", "/login.html?portal=out", nil, "Remote-User", "alice"); page.Code != http.StatusOK {
		t.Fatalf("the page that tells it, not the library: %v %q", page.Code, page.Header().Get("Location"))
	}
	setPortal(web, settings.PortalOptions{Header: true, LogoutUrl: "https://auth.example.com/logout"})
	if location := proxy.do("POST", "/logout", nil, "Remote-User", "alice").Header().Get("Location"); location != "https://auth.example.com/logout" {
		t.Fatalf("to the exit of the portal: %q", location)
	}
	// signed in with a password, Log out is the one of always
	password := &client{t: t, handler: proxy.handler}
	password.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	if location := password.do("POST", "/logout", nil).Header().Get("Location"); location != "/login.html" || len(password.cookies) != 0 {
		t.Fatalf("a session of the app: %q", location)
	}
}

// An environment variable wins over its option of Settings.
func TestPortalEnvironmentWinsOverSettings(t *testing.T) {
	t.Setenv(PROXY_HEADER_ENV, "X-Auth-User")
	t.Setenv(PROXY_NEW_USERS_ENV, "player")
	t.Setenv(PROXY_LOGOUT_URL_ENV, "/portal/logout")
	web := portalWeb(t, settings.PortalOptions{Header: false, UserHeader: "Remote-User", NewUsers: "", LogoutUrl: "https://elsewhere.example.com"})
	options := web.auth.portal()
	if !options.Header || options.UserHeader != "X-Auth-User" || options.NewUsers != ROLE_VIEWER || options.LogoutUrl != "/portal/logout" || options.EmailHeader != defaultEmailHeader {
		t.Fatalf("the environment wins: %+v", options)
	}
	proxy := &client{t: t, handler: web.auth.middleware(web.router), ip: proxyAddress}
	if code := proxy.do("GET", "/index.html", nil, "X-Auth-User", "frank").Code; code != http.StatusOK {
		t.Fatalf("the header of the environment: %v", code)
	}

	// the form of Settings does not change what the environment sets
	admin := &client{t: t, handler: proxy.handler}
	admin.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	saved := admin.do("POST", "/settings/portal", url.Values{"portal_user_header": {"Remote-User"}, "portal_new_users": {""}, "portal_admin_group": {"slm-admins"}})
	if saved.Header().Get("Location") != "/settings.html?done=portal#portal" {
		t.Fatalf("saved: %q", saved.Header().Get("Location"))
	}
	stored := settings.ReadSettings(web.dataFolder).Portal
	if stored.UserHeader != "Remote-User" || stored.Header || stored.AdminGroup != "slm-admins" {
		t.Fatalf("the options not locked are saved, the locked ones kept: %+v", stored)
	}
	if options := web.auth.portal(); options.UserHeader != "X-Auth-User" || options.AdminGroup != "slm-admins" {
		t.Fatalf("in use: %+v", options)
	}
}

// The options of Settings are checked before they are saved, by an administrator only.
func TestPortalSettingsForm(t *testing.T) {
	web := portalWeb(t, settings.PortalOptions{})
	admin := &client{t: t, handler: web.auth.middleware(web.withActivityLog(web.router))}
	admin.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	good := url.Values{"portal_header": {"true"}, "portal_user_header": {"Remote-User"}, "portal_cloudflare": {"true"},
		"portal_cf_team": {"https://MyTeam.cloudflareaccess.com/"}, "portal_cf_aud": {strings.Repeat("ab", 32)}, "portal_match": {MATCH_EMAIL},
		"portal_new_users": {ROLE_GUEST}, "portal_admin_group": {"slm-admins"}, "portal_logout_url": {"https://auth.example.com/logout"}}
	if location := admin.do("POST", "/settings/portal", good).Header().Get("Location"); location != "/settings.html?done=portal#portal" {
		t.Fatalf("saved: %q", location)
	}
	stored := settings.ReadSettings(web.dataFolder).Portal
	if !stored.Header || !stored.Cloudflare || stored.CloudflareTeam != "myteam" || stored.MatchBy != MATCH_EMAIL || stored.NewUsers != ROLE_GUEST {
		t.Fatalf("the options: %+v", stored)
	}
	if n := activityCount(web, ACTION_PORTAL_SETTINGS, ""); n != 1 {
		t.Fatalf("a change of the portal is in the activity: %d", n)
	}
	for field, value := range map[string]string{"portal_user_header": "Authorization", "portal_email_header": "Remote User", "portal_cf_team": "my team",
		"portal_cf_aud": "short", "portal_match": "anything", "portal_new_users": ROLE_ADMIN, "portal_logout_url": "javascript:alert(1)", "portal_admin_group": "a,b"} {
		bad := url.Values{}
		for key, values := range good {
			bad[key] = values
		}
		bad.Set(field, value)
		location := admin.do("POST", "/settings/portal", bad).Header().Get("Location")
		if !strings.Contains(location, "error=") || !strings.HasSuffix(location, "#portal") {
			t.Fatalf("%s=%q is refused: %q", field, value, location)
		}
	}
	if settings.ReadSettings(web.dataFolder).Portal != stored {
		t.Fatal("a refused form changes nothing")
	}
	viewer := &client{t: t, handler: admin.handler}
	viewer.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	if code := viewer.do("POST", "/settings/portal", url.Values{}).Code; code != http.StatusForbidden {
		t.Fatalf("only administrators: %v", code)
	}
}

func TestPortalHelpers(t *testing.T) {
	for address, valid := range map[string]bool{"": true, "/logout": true, "https://auth.example.com/logout?rd=https://slm.example.com": true, "http://10.0.0.2/logout": true,
		"//evil.example.com": false, "javascript:alert(1)": false, "https://user:pass@auth.example.com": false, "/a b": false, "ftp://x": false} {
		if validLogoutUrl(address) != valid {
			t.Errorf("logout address %q: %v", address, !valid)
		}
	}
	for team, want := range map[string]string{"myteam": "myteam", "MyTeam.cloudflareaccess.com": "myteam", "https://my-team.cloudflareaccess.com/": "my-team"} {
		if got, ok := cfTeamName(team); !ok || got != want {
			t.Errorf("team %q: %q %v", team, got, ok)
		}
	}
	for _, team := range []string{"", "my team", "-team", "evil.com/x", "https://evil.com"} {
		if _, ok := cfTeamName(team); ok {
			t.Errorf("team %q is not valid", team)
		}
	}
	for name, want := range map[string]string{"carol": "carol", "Émile Zola": "mile.Zola", "@@@": "user", "...": "...", strings.Repeat("x", 40): strings.Repeat("x", 32)} {
		if got := portalUserName(name, ""); got != want {
			t.Errorf("name %q: %q", name, got)
		}
	}
	if got := portalUserName("Émile Zola", "emile.zola@example.com"); got != "emile.zola" {
		t.Errorf("from the email: %q", got)
	}
	for header, valid := range map[string]bool{"Remote-User": true, "X-authentik-username": true, "Cookie": false, "cf-access-jwt-assertion": false, "Remote User": false, "": false} {
		if validHeaderName(header) != valid {
			t.Errorf("header %q: %v", header, !valid)
		}
	}
}
