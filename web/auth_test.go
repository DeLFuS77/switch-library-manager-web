package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// envAuth returns an Auth with the user set in the environment.
func envAuth(t *testing.T, user string, password string) *Auth {
	t.Helper()
	t.Setenv("SLM_AUTH_USERNAME", user)
	t.Setenv("SLM_AUTH_PASSWORD", password)
	auth, err := newAuth(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

// usersWeb returns a web with the user pages, without environment user.
func usersWeb(t *testing.T) *Web {
	t.Helper()
	t.Setenv("SLM_AUTH_USERNAME", "")
	t.Setenv("SLM_AUTH_PASSWORD", "")
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	auth, err := newAuth(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	web.auth = auth
	web.HandleUsers()
	web.router.HandleFunc("/index.html", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("library")) }).Methods("GET")
	web.router.HandleFunc("/settings.html", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("settings")) }).Methods("GET")
	web.router.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }).Methods("POST")
	return web
}

type client struct {
	t       *testing.T
	handler http.Handler
	cookies []*http.Cookie
	ip      string
}

func (c *client) do(method string, path string, form url.Values, headers ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	var request *http.Request
	if form != nil {
		request = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		request = httptest.NewRequest(method, path, nil)
	}
	// a client of the local network, unless the test sets another address
	request.RemoteAddr = "192.168.1.10:1234"
	if c.ip != "" {
		request.RemoteAddr = c.ip + ":1234"
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	for _, cookie := range c.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == SESSION_COOKIE {
			c.cookies = nil
			if cookie.MaxAge >= 0 && cookie.Value != "" {
				c.cookies = []*http.Cookie{cookie}
			}
		}
	}
	return recorder
}

func TestUserStore(t *testing.T) {
	folder := t.TempDir()
	store, err := loadUserStore(folder, "envadmin")
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name, password, role string
		want                 error
	}{
		{"bad name!", "alpha-secret-11", ROLE_ADMIN, ErrUserName},
		{"", "alpha-secret-11", ROLE_ADMIN, ErrUserName},
		{"alice", "short", ROLE_ADMIN, ErrPasswordLength},
		{"alice", strings.Repeat("x", 73), ROLE_ADMIN, ErrPasswordLength},
		{"alice", "alpha-secret-11", "root", ErrRole},
		{"EnvAdmin", "alpha-secret-11", ROLE_ADMIN, ErrUserExists},
		{"alice", "alpha-secret-11", ROLE_ADMIN, nil},
		{"ALICE", "alpha-secret-11", ROLE_VIEWER, ErrUserExists},
		{"bob", "bravo-secret-22", ROLE_VIEWER, nil},
	} {
		if err := store.Add(tt.name, tt.password, tt.role); !errors.Is(err, tt.want) {
			t.Errorf("Add(%q, %q): got %v, want %v", tt.name, tt.role, err, tt.want)
		}
	}

	if _, ok := store.Verify("alice", "alpha-secret-11"); !ok {
		t.Fatal("valid password rejected")
	}
	if _, ok := store.Verify("Alice", "alpha-secret-11"); !ok {
		t.Fatal("user names are not case sensitive")
	}
	if _, ok := store.Verify("alice", "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, ok := store.Verify("nobody", "alpha-secret-11"); ok {
		t.Fatal("unknown user accepted")
	}

	data, _ := os.ReadFile(filepath.Join(folder, USERS_FILENAME))
	if strings.Contains(string(data), "alpha-secret-11") {
		t.Fatal("passwords must be stored hashed")
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(folder, USERS_FILENAME)); info.Mode().Perm() != 0600 {
			t.Fatalf("users.json must only be readable by the owner: %v", info.Mode().Perm())
		}
	}

	reloaded, err := loadUserStore(folder, "")
	if err != nil || reloaded.Count() != 2 {
		t.Fatalf("users must be saved: %v %v", reloaded.Count(), err)
	}
	if user, _ := reloaded.Get("bob"); user.IsAdmin() {
		t.Fatal("roles must be saved")
	}
}

func TestUserStoreKeepsAnAdministrator(t *testing.T) {
	store, _ := loadUserStore(t.TempDir(), "")
	store.Add("alice", "alpha-secret-11", ROLE_ADMIN)
	store.Add("bob", "bravo-secret-22", ROLE_VIEWER)

	if err := store.SetRole("alice", ROLE_VIEWER); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demoting the last administrator: %v", err)
	}
	if err := store.Delete("alice"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("deleting the last administrator: %v", err)
	}
	if err := store.SetRole("bob", ROLE_ADMIN); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRole("alice", ROLE_VIEWER); err != nil {
		t.Fatalf("with another administrator the role can change: %v", err)
	}
	if err := store.SetPassword("ghost", "alpha-secret-11"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown user: %v", err)
	}

	// with an administrator in the environment, the store needs none
	withEnv, _ := loadUserStore(t.TempDir(), "envadmin")
	withEnv.Add("carol", "charlie-sec-33", ROLE_ADMIN)
	if err := withEnv.SetRole("carol", ROLE_VIEWER); err != nil {
		t.Fatalf("the environment administrator remains: %v", err)
	}
}

func TestLoginDisabledWithoutUsers(t *testing.T) {
	web := usersWeb(t)
	c := &client{t: t, handler: web.auth.middleware(web.router)}
	if code := c.do("GET", "/settings.html", nil).Code; code != http.StatusOK {
		t.Fatalf("without users everything is open: %v", code)
	}
	if location := c.do("GET", "/login.html", nil).Header().Get("Location"); location != "/index.html" {
		t.Fatalf("the login page is not needed: %q", location)
	}
}

func TestFirstUserEnablesLogin(t *testing.T) {
	web := usersWeb(t)
	c := &client{t: t, handler: web.auth.middleware(web.router)}

	created := c.do("POST", "/users/create", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}, "role": {ROLE_VIEWER}})
	if created.Code != http.StatusSeeOther || !strings.Contains(created.Header().Get("Location"), "done=enabled") {
		t.Fatalf("create: %v %q", created.Code, created.Header().Get("Location"))
	}
	if user, _ := web.auth.users.Get("alice"); !user.IsAdmin() {
		t.Fatal("the first user must be an administrator")
	}
	if !web.auth.Enabled() || len(c.cookies) != 1 {
		t.Fatal("the creator must be logged in")
	}
	if code := c.do("GET", "/settings.html", nil).Code; code != http.StatusOK {
		t.Fatalf("the new administrator keeps access: %v", code)
	}

	anonymous := &client{t: t, handler: c.handler}
	if code := anonymous.do("GET", "/settings.html", nil, "Accept", "text/html").Code; code != http.StatusSeeOther {
		t.Fatalf("others must log in: %v", code)
	}
}

func TestLoginLogoutAndRoles(t *testing.T) {
	web := usersWeb(t)
	web.auth.users.Add("alice", "alpha-secret-11", ROLE_ADMIN)
	web.auth.users.Add("bob", "bravo-secret-22", ROLE_VIEWER)
	handler := web.auth.middleware(web.router)

	anonymous := &client{t: t, handler: handler}
	redirect := anonymous.do("GET", "/settings.html", nil, "Accept", "text/html")
	if redirect.Code != http.StatusSeeOther || redirect.Header().Get("Location") != "/login.html?next=%2Fsettings.html" {
		t.Fatalf("browsers go to the login page: %v %q", redirect.Code, redirect.Header().Get("Location"))
	}
	api := anonymous.do("GET", "/index.html", nil)
	if api.Code != http.StatusUnauthorized || api.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("API clients are asked for credentials: %v", api.Code)
	}
	script := anonymous.do("POST", "/sync", nil, "Sec-Fetch-Site", "same-origin")
	if script.Code != http.StatusUnauthorized || script.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("scripts of the pages must not open the browser's login dialog")
	}
	if page := anonymous.do("GET", "/login.html", nil); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="password"`) {
		t.Fatalf("login page: %v", page.Code)
	}

	wrong := anonymous.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"nope"}})
	if wrong.Code != http.StatusUnauthorized || len(anonymous.cookies) != 0 || !strings.Contains(wrong.Body.String(), "Wrong user name or password.") {
		t.Fatalf("wrong password: %v", wrong.Code)
	}

	viewer := &client{t: t, handler: handler}
	login := viewer.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}, "next": {"/index.html?page=2"}})
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != "/index.html?page=2" || len(viewer.cookies) != 1 {
		t.Fatalf("login: %v %q", login.Code, login.Header().Get("Location"))
	}
	if !viewer.cookies[0].HttpOnly || viewer.cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("the session cookie must be HttpOnly and SameSite")
	}
	if code := viewer.do("GET", "/index.html", nil).Code; code != http.StatusOK {
		t.Fatalf("read-only users can browse: %v", code)
	}
	for _, request := range [][2]string{{"GET", "/settings.html"}, {"GET", "/users.html"}, {"POST", "/sync"}, {"POST", "/users/create"}} {
		if code := viewer.do(request[0], request[1], url.Values{}).Code; code != http.StatusForbidden {
			t.Errorf("%s %s by a read-only user: %v", request[0], request[1], code)
		}
	}

	admin := &client{t: t, handler: handler}
	admin.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	if code := admin.do("POST", "/sync", nil).Code; code != http.StatusAccepted {
		t.Fatalf("administrators can synchronize: %v", code)
	}
	if page := admin.do("GET", "/users.html", nil); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "bob") {
		t.Fatalf("users page: %v", page.Code)
	}

	logout := viewer.do("POST", "/logout", nil)
	if logout.Code != http.StatusSeeOther || len(viewer.cookies) != 0 {
		t.Fatalf("logout: %v", logout.Code)
	}
	if code := viewer.do("GET", "/index.html", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("after logout: %v", code)
	}
}

func TestSessionsEndWhenThePasswordChanges(t *testing.T) {
	web := usersWeb(t)
	web.auth.users.Add("alice", "alpha-secret-11", ROLE_ADMIN)
	handler := web.auth.middleware(web.router)

	first := &client{t: t, handler: handler}
	first.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	second := &client{t: t, handler: handler}
	second.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})

	changed := first.do("POST", "/account/password", url.Values{"current": {"alpha-secret-11"}, "password": {"india-secret-99"}})
	if changed.Code != http.StatusSeeOther || !strings.Contains(changed.Header().Get("Location"), "done=password") {
		t.Fatalf("change password: %v %q", changed.Code, changed.Header().Get("Location"))
	}
	if code := first.do("GET", "/index.html", nil).Code; code != http.StatusOK {
		t.Fatalf("the session that changed the password stays: %v", code)
	}
	if code := second.do("GET", "/index.html", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("other sessions must end: %v", code)
	}

	wrong := first.do("POST", "/account/password", url.Values{"current": {"nope"}, "password": {"hotel-secret-88"}})
	if !strings.Contains(wrong.Header().Get("Location"), "error=") {
		t.Fatalf("the current password is checked: %q", wrong.Header().Get("Location"))
	}
}

func TestTamperedSessionsAreRejected(t *testing.T) {
	web := usersWeb(t)
	web.auth.users.Add("alice", "alpha-secret-11", ROLE_ADMIN)
	web.auth.users.Add("bob", "bravo-secret-22", ROLE_VIEWER)
	handler := web.auth.middleware(web.router)

	viewer := &client{t: t, handler: handler}
	viewer.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	parts := strings.Split(viewer.cookies[0].Value, ".")
	// claim to be alice with bob's signature
	forged := &client{t: t, handler: handler, cookies: []*http.Cookie{{Name: SESSION_COOKIE, Value: "YWxpY2U." + parts[1] + "." + parts[2]}}}
	if code := forged.do("GET", "/index.html", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("a forged session must be rejected: %v", code)
	}
	expired := &client{t: t, handler: handler, cookies: []*http.Cookie{{Name: SESSION_COOKIE, Value: parts[0] + ".1." + web.auth.sign("bob", 1, "x")}}}
	if code := expired.do("GET", "/index.html", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("an expired session must be rejected: %v", code)
	}
}

func TestDeleteAndRoleChangesFromTheUsersPage(t *testing.T) {
	web := usersWeb(t)
	web.auth.users.Add("alice", "alpha-secret-11", ROLE_ADMIN)
	web.auth.users.Add("bob", "bravo-secret-22", ROLE_VIEWER)
	admin := &client{t: t, handler: web.auth.middleware(web.router)}
	admin.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})

	if location := admin.do("POST", "/users/delete", url.Values{"name": {"alice"}}).Header().Get("Location"); !strings.Contains(location, "error=") {
		t.Fatalf("deleting yourself must be refused: %q", location)
	}
	if location := admin.do("POST", "/users/role", url.Values{"name": {"bob"}, "role": {ROLE_ADMIN}}).Header().Get("Location"); !strings.Contains(location, "done=role") {
		t.Fatalf("role change: %q", location)
	}
	if location := admin.do("POST", "/users/password", url.Values{"name": {"bob"}, "password": {"newpassword"}}).Header().Get("Location"); !strings.Contains(location, "done=password") {
		t.Fatalf("password reset: %q", location)
	}
	if _, ok := web.auth.users.Verify("bob", "newpassword"); !ok {
		t.Fatal("the new password must work")
	}
	if location := admin.do("POST", "/users/delete", url.Values{"name": {"bob"}}).Header().Get("Location"); !strings.Contains(location, "done=deleted") {
		t.Fatalf("delete: %q", location)
	}
	page := admin.do("GET", "/users.html?error=0", nil)
	if !strings.Contains(page.Body.String(), "letters, numbers") {
		t.Fatal("errors are shown on the page")
	}
}

func TestBasicAuthForUsersAndRateLimit(t *testing.T) {
	web := usersWeb(t)
	web.auth.users.Add("bob", "bravo-secret-22", ROLE_VIEWER)
	c := &client{t: t, handler: web.auth.middleware(web.router), ip: "192.0.2.7"}

	if code := c.do("GET", "/index.html", nil, "Authorization", basic("bob", "bravo-secret-22")).Code; code != http.StatusOK {
		t.Fatalf("basic authentication of a user: %v", code)
	}
	if code := c.do("POST", "/sync", nil, "Authorization", basic("bob", "bravo-secret-22")).Code; code != http.StatusForbidden {
		t.Fatalf("roles apply to basic authentication: %v", code)
	}
	// a flood of wrong passwords for an account blocks it, from every address: a botnet
	// cannot keep guessing one user name from many addresses
	for i := 0; i < maxLoginFailures; i++ {
		c.do("GET", "/index.html", nil, "Authorization", basic("bob", "wrong"))
	}
	if code := c.do("GET", "/index.html", nil, "Authorization", basic("bob", "bravo-secret-22")).Code; code != http.StatusTooManyRequests {
		t.Fatalf("too many failures must block the account: %v", code)
	}
	login := c.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	if login.Code != http.StatusTooManyRequests || len(c.cookies) != 0 {
		t.Fatalf("the login form is blocked too: %v", login.Code)
	}
	other := &client{t: t, handler: c.handler, ip: "192.0.2.8"}
	if code := other.do("GET", "/index.html", nil, "Authorization", basic("bob", "bravo-secret-22")).Code; code != http.StatusTooManyRequests {
		t.Fatalf("the account stays blocked from another address too: %v", code)
	}
}

func basic(user string, password string) string {
	request := httptest.NewRequest("GET", "/", nil)
	request.SetBasicAuth(user, password)
	return request.Header.Get("Authorization")
}

func TestSafeNext(t *testing.T) {
	for next, want := range map[string]string{
		"/title/0100.html?x=1": "/title/0100.html?x=1",
		"":                     "/index.html",
		"https://evil.example": "/index.html",
		"//evil.example":       "/index.html",
		"/\\evil.example":      "/index.html",
		"/login.html":          "/index.html",
	} {
		if got := safeNext(next); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", next, got, want)
		}
	}
}

func TestWithAuthFillsPageData(t *testing.T) {
	info := AuthInfo{Enabled: true, User: "bob"}
	data := withAuth(StatisticsPageData{GlobalPageData: GlobalPageData{Page: "statistics"}}, info).(StatisticsPageData)
	if data.Auth != info || data.Page != "statistics" {
		t.Fatalf("auth not set: %+v", data.GlobalPageData)
	}
	if got := withAuth("text", info); got != "text" {
		t.Fatal("other data is left alone")
	}
}

func TestUserMessagesAreTranslated(t *testing.T) {
	texts := []string{errTooManyLogins.Error(), "Wrong user name or password."}
	for _, err := range userErrors {
		texts = append(texts, err.Error())
	}
	for _, message := range userMessages {
		texts = append(texts, message)
	}
	for _, text := range texts {
		if _, ok := translations["es"][text]; !ok {
			t.Errorf("no Spanish translation for %q", text)
		}
	}
}
