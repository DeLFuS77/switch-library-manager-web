package web

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const (
	testOidcClient = "slm-test"
	testOidcSecret = "test-client-secret-only"
	testOidcKid    = "oidc-key-1"
)

// fakeProvider is a provider of OpenID Connect for the tests, over https, with a key made for
// the test: it checks the client, the redirect address and PKCE, and signs the ID tokens with
// the claims the test sets.
type fakeProvider struct {
	t      *testing.T
	server *httptest.Server
	key    *rsa.PrivateKey
	mutex  sync.Mutex
	grants map[string]fakeGrant
	// what the next ID tokens say over the usual claims; a nil value removes a claim
	claims map[string]any
	// the codes changed for tokens, and the configuration served without a page to log out
	exchanges    atomic.Int32
	noEndSession bool
}

type fakeGrant struct {
	challenge, nonce, redirect string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{t: t, key: key, grants: map[string]fakeGrant{}, claims: map[string]any{}}
	p.server = httptest.NewTLSServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeProvider) serve(w http.ResponseWriter, r *http.Request) {
	issuer := p.server.URL
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		configuration := map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
			"jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}}
		if !p.noEndSession {
			configuration["end_session_endpoint"] = issuer + "/end-session"
		}
		json.NewEncoder(w).Encode(configuration)
	case "/keys":
		e := big.NewInt(int64(p.key.E)).Bytes()
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kid": testOidcKid, "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(p.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)},
		}})
	case "/token":
		p.exchanges.Add(1)
		r.ParseForm()
		id, secret, basic := r.BasicAuth()
		if !basic {
			id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		p.mutex.Lock()
		grant, ok := p.grants[r.PostForm.Get("code")]
		delete(p.grants, r.PostForm.Get("code"))
		p.mutex.Unlock()
		verifier := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		switch {
		case id != testOidcClient || secret != testOidcSecret:
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return
		case !ok || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("redirect_uri") != grant.redirect ||
			base64.RawURLEncoding.EncodeToString(verifier[:]) != grant.challenge:
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		now := time.Now()
		claims := map[string]any{"iss": issuer, "aud": testOidcClient, "sub": "user-1", "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "nonce": grant.nonce}
		p.mutex.Lock()
		for name, value := range p.claims {
			if value == nil {
				delete(claims, name)
			} else {
				claims[name] = value
			}
		}
		// an account of its own for each name, unless the test says which
		if _, set := p.claims["sub"]; !set {
			if name, ok := claims["preferred_username"].(string); ok {
				claims["sub"] = "sub-" + name
			}
		}
		p.mutex.Unlock()
		idToken := cfToken(p.t, p.key, map[string]any{"alg": "RS256", "kid": testOidcKid, "typ": "JWT"}, claims)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 300, "id_token": idToken})
	default:
		http.NotFound(w, r)
	}
}

// says sets what the next ID tokens say.
func (p *fakeProvider) says(claims map[string]any) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.claims = claims
}

// authorize is the provider letting in the person a browser sent with the address of the
// button: it checks what the app asks for, and returns the code and the state to send back.
func (p *fakeProvider) authorize(address string) (string, string) {
	p.t.Helper()
	target, err := url.Parse(address)
	if err != nil || !strings.HasPrefix(address, p.server.URL+"/authorize?") {
		p.t.Fatalf("the button goes to the provider: %q", address)
	}
	query := target.Query()
	if query.Get("response_type") != "code" || query.Get("client_id") != testOidcClient || query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") == "" || query.Get("state") == "" || query.Get("nonce") == "" || !strings.Contains(" "+query.Get("scope")+" ", " openid ") {
		p.t.Fatalf("what the app asks for: %v", query)
	}
	code := rand.Text()
	p.mutex.Lock()
	p.grants[code] = fakeGrant{challenge: query.Get("code_challenge"), nonce: query.Get("nonce"), redirect: query.Get("redirect_uri")}
	p.mutex.Unlock()
	return code, query.Get("state")
}

// options turn OpenID Connect on with this provider.
func (p *fakeProvider) options(more settings.PortalOptions) settings.PortalOptions {
	more.Oidc, more.OidcIssuer, more.OidcClientId, more.OidcClientSecret = true, p.server.URL, testOidcClient, testOidcSecret
	more.OidcButton = "PocketID"
	return more
}

// oidcWeb is portalWeb with OpenID Connect on with a fake provider, reached over https.
func oidcWeb(t *testing.T, more settings.PortalOptions) (*Web, *fakeProvider) {
	t.Helper()
	provider := newFakeProvider(t)
	web := portalWeb(t, provider.options(more))
	web.auth.oidc.client = newOidcClient(provider.server.Client().Transport)
	return web, provider
}

// browser keeps every cookie of the app, as a browser does, by path.
type browser struct {
	t       *testing.T
	handler http.Handler
	jar     *cookiejar.Jar
}

func newBrowser(t *testing.T, web *Web) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, handler: web.auth.middleware(web.withActivityLog(web.router)), jar: jar}
}

const browserSite = "http://slm.example.com"

func (b *browser) do(method string, path string, form url.Values, headers ...string) *httptest.ResponseRecorder {
	b.t.Helper()
	var request *http.Request
	if form != nil {
		request = httptest.NewRequest(method, browserSite+path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		request = httptest.NewRequest(method, browserSite+path, nil)
	}
	request.RemoteAddr = "192.168.1.10:1234"
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	for _, cookie := range b.jar.Cookies(request.URL) {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	b.handler.ServeHTTP(recorder, request)
	b.jar.SetCookies(request.URL, recorder.Result().Cookies())
	return recorder
}

func (b *browser) cookie(name string, path string) string {
	address, _ := url.Parse(browserSite + path)
	for _, cookie := range b.jar.Cookies(address) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

// signIn presses the button of the login page, lets the provider take the person in, and
// comes back to the app: the answer of the callback.
func (b *browser) signIn(provider *fakeProvider, next string) *httptest.ResponseRecorder {
	b.t.Helper()
	start := b.do("GET", oidcLoginPath+"?next="+url.QueryEscape(next), nil)
	if start.Code != http.StatusSeeOther {
		b.t.Fatalf("the button: %v %q", start.Code, start.Header().Get("Location"))
	}
	code, state := provider.authorize(start.Header().Get("Location"))
	return b.do("GET", oidcCallbackPath+"?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), nil)
}

// A person the provider names gets the usual session of the app, as their user: the ID token
// is checked, the code changed with PKCE and the client secret, the page asked for opened.
func TestOidcSignIn(t *testing.T) {
	web, provider := oidcWeb(t, settings.PortalOptions{})
	provider.says(map[string]any{"preferred_username": "bob", "email": "bob@example.com", "email_verified": true})
	b := newBrowser(t, web)

	login := b.do("GET", "/login.html?next=/collections.html", nil)
	if body := login.Body.String(); !strings.Contains(body, `href="/login/oidc?next=%2fcollections.html"`) || !strings.Contains(body, "PocketID") {
		t.Fatalf("the login page has the button of the provider: %s", body)
	}
	back := b.signIn(provider, "/collections.html")
	if back.Code != http.StatusSeeOther || back.Header().Get("Location") != "/collections.html" {
		t.Fatalf("back in the app, on the page asked for: %v %q", back.Code, back.Header().Get("Location"))
	}
	session := b.cookie(SESSION_COOKIE, "/")
	if !strings.HasSuffix(session, "."+sessionOidc) || b.cookie(oidcFlowCookie, oidcLoginPath) != "" {
		t.Fatalf("a session of the app, marked, and the sign-in used up: %q", session)
	}
	if b.cookie(oidcHintCookie, "/logout") == "" || b.cookie(oidcHintCookie, "/") != "" {
		t.Fatal("the ID token is kept for Log out only")
	}
	if code := b.do("GET", "/index.html", nil).Code; code != http.StatusOK {
		t.Fatalf("the session opens the library: %v", code)
	}
	if code := b.do("GET", "/settings.html", nil).Code; code != http.StatusForbidden {
		t.Fatalf("bob keeps the role of a player: %v", code)
	}
	if n := activityCount(web, activity.ACTION_PORTAL_OIDC, ""); n != 1 {
		t.Fatalf("the sign-in is in the activity: %d", n)
	}
	if user, _ := web.auth.users.Get("bob"); user.LastLogin.IsZero() {
		t.Fatal("the sign-in is shown in Users")
	}
	if view := web.auth.portalView(); view.LastOk.User != "bob" || view.LastOk.Via != VIA_OIDC || !view.OidcReady || view.OidcButton != "PocketID" || view.Options.OidcClientSecret != "" || !view.OidcSecretSaved {
		t.Fatalf("Settings shows the last sign-in, never the secret: %+v", view)
	}
	request := httptest.NewRequest("GET", "/account.html", nil)
	request.AddCookie(&http.Cookie{Name: SESSION_COOKIE, Value: session})
	if principal := web.auth.fromSession(request); principal == nil || principal.Via != VIA_OIDC || principal.Role != ROLE_VIEWER {
		t.Fatalf("the session says it was opened with OpenID Connect: %+v", principal)
	}

	// signed in already, the login page goes on to the library
	if location := b.do("GET", "/login.html", nil).Header().Get("Location"); location != "/index.html" {
		t.Fatalf("the login page is not needed: %q", location)
	}
	// Log out ends the session of the app and goes to the page of the provider that ends its
	// own, through a page of the app (form-action 'self')
	out := b.do("POST", "/logout", nil)
	body := out.Body.String()
	exit := provider.server.URL + "/end-session?client_id=" + testOidcClient + "&amp;id_token_hint="
	if out.Code != http.StatusOK || !strings.Contains(body, `http-equiv="refresh"`) || !strings.Contains(body, exit) {
		t.Fatalf("to the page of the provider: %v %s", out.Code, body)
	}
	if b.cookie(SESSION_COOKIE, "/") != "" || b.cookie(oidcHintCookie, "/logout") != "" {
		t.Fatal("the cookies are gone")
	}
	stolen := httptest.NewRequest("GET", "/index.html", nil)
	stolen.AddCookie(&http.Cookie{Name: SESSION_COOKIE, Value: session})
	if web.auth.fromSession(stolen) != nil {
		t.Fatal("the session ended also where it was copied")
	}
	// without a page to log out of the provider, back to the login page
	provider.noEndSession = true
	web.auth.oidc.check(provider.server.URL)
	b.signIn(provider, "")
	if location := b.do("POST", "/logout", nil).Header().Get("Location"); location != "/login.html" {
		t.Fatalf("a provider without a page to log out: %q", location)
	}
}

// The answers that are not of this sign-in, or whose ID token is not valid, open nothing: the
// login page says it did not work, and the activity notes it.
func TestOidcRefusesWhatIsNotValid(t *testing.T) {
	web, provider := oidcWeb(t, settings.PortalOptions{})
	refused := func(name string, back *httptest.ResponseRecorder, b *browser) {
		t.Helper()
		if back.Code != http.StatusSeeOther || !strings.HasPrefix(back.Header().Get("Location"), "/login.html?oidc="+OIDC_FAILED) {
			t.Fatalf("%s: %v %q", name, back.Code, back.Header().Get("Location"))
		}
		if b.cookie(SESSION_COOKIE, "/") != "" || b.do("GET", "/index.html", nil, "Accept", "text/html").Code != http.StatusSeeOther {
			t.Fatalf("%s: no session", name)
		}
	}
	for name, claims := range map[string]map[string]any{
		"another nonce":    {"preferred_username": "bob", "nonce": "not-the-one-of-this-sign-in"},
		"another audience": {"preferred_username": "bob", "aud": "another-client"},
		"another issuer":   {"preferred_username": "bob", "iss": "https://elsewhere.example.com"},
		"expired":          {"preferred_username": "bob", "exp": time.Now().Add(-time.Minute).Unix(), "iat": time.Now().Add(-time.Hour).Unix()},
		"nobody":           {"preferred_username": nil},
	} {
		provider.says(claims)
		b := newBrowser(t, web)
		refused(name, b.signIn(provider, ""), b)
		web.auth.oidcLimiter = newLoginLimiter()
	}

	// a state that is not the one of this browser: a sign-in started by somebody else
	provider.says(map[string]any{"preferred_username": "bob"})
	b := newBrowser(t, web)
	start := b.do("GET", oidcLoginPath, nil)
	code, _ := provider.authorize(start.Header().Get("Location"))
	exchanges := provider.exchanges.Load()
	refused("another state", b.do("GET", oidcCallbackPath+"?code="+code+"&state=forged", nil), b)
	// and no sign-in started in this browser at all
	other := newBrowser(t, web)
	_, state := provider.authorize(b.do("GET", oidcLoginPath, nil).Header().Get("Location"))
	refused("another browser", other.do("GET", oidcCallbackPath+"?code="+code+"&state="+state, nil), other)
	if provider.exchanges.Load() != exchanges {
		t.Fatal("a code is not even changed without the state of this browser")
	}
	// a sign-in comes back once
	b = newBrowser(t, web)
	start = b.do("GET", oidcLoginPath, nil)
	flow := b.cookie(oidcFlowCookie, oidcLoginPath)
	code, state = provider.authorize(start.Header().Get("Location"))
	if back := b.do("GET", oidcCallbackPath+"?code="+code+"&state="+state, nil); back.Header().Get("Location") != "/index.html" {
		t.Fatalf("the sign-in: %q", back.Header().Get("Location"))
	}
	again := httptest.NewRequest("GET", oidcCallbackPath+"?code="+code+"&state="+state, nil)
	again.AddCookie(&http.Cookie{Name: oidcFlowCookie, Value: flow})
	recorder := httptest.NewRecorder()
	web.auth.middleware(web.router).ServeHTTP(recorder, again)
	if !strings.Contains(recorder.Header().Get("Location"), OIDC_FAILED) {
		t.Fatalf("a code used twice: %q", recorder.Header().Get("Location"))
	}
	if n := activityCount(web, activity.ACTION_LOGIN_FAIL, oidcFailedDetail); n == 0 {
		t.Fatal("the failures are in the activity")
	}
	if web.auth.portalView().LastProblem.Result != "oidc" {
		t.Fatal("Settings shows the last failure")
	}
	// the login page says it
	if page := newBrowser(t, web).do("GET", "/login.html?oidc="+OIDC_FAILED, nil).Body.String(); !strings.Contains(page, "alert-danger") || !strings.Contains(page, "PocketID") {
		t.Fatalf("the login page tells the failure: %s", page)
	}

	// cancelled at the provider: told on the login page, not as a failure
	failures := activityCount(web, activity.ACTION_LOGIN_FAIL, oidcFailedDetail)
	b = newBrowser(t, web)
	_, state = provider.authorize(b.do("GET", oidcLoginPath, nil).Header().Get("Location"))
	if location := b.do("GET", oidcCallbackPath+"?error=access_denied&state="+state, nil).Header().Get("Location"); !strings.HasPrefix(location, "/login.html?oidc="+OIDC_CANCELLED) {
		t.Fatalf("cancelled: %q", location)
	}
	if activityCount(web, activity.ACTION_LOGIN_FAIL, oidcFailedDetail) != failures {
		t.Fatal("a person who cancels did nothing wrong")
	}
}

// Many failed sign-ins from an address stop asking the provider for a while, without keeping
// the address out of the login with a password.
func TestOidcFailuresAreLimited(t *testing.T) {
	web, provider := oidcWeb(t, settings.PortalOptions{})
	provider.says(map[string]any{"preferred_username": "bob", "nonce": "wrong"})
	b := newBrowser(t, web)
	for range maxLoginFailures {
		b.signIn(provider, "")
	}
	exchanges := provider.exchanges.Load()
	provider.says(map[string]any{"preferred_username": "bob"})
	if back := b.signIn(provider, ""); !strings.Contains(back.Header().Get("Location"), OIDC_FAILED) || provider.exchanges.Load() != exchanges {
		t.Fatalf("blocked for a while: %q", back.Header().Get("Location"))
	}
	if login := b.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}}); login.Code != http.StatusSeeOther {
		t.Fatalf("the password still works: %v", login.Code)
	}
}

// A person without a user is told so on the login page, with the name the provider gave, unless
// the options make users: then a guest or a player without a password.
func TestOidcPersonWithoutUser(t *testing.T) {
	web, provider := oidcWeb(t, settings.PortalOptions{})
	provider.says(map[string]any{"preferred_username": "carol", "email": "carol@example.com", "email_verified": true})
	b := newBrowser(t, web)
	back := b.signIn(provider, "/collections.html")
	if location := back.Header().Get("Location"); !strings.HasPrefix(location, "/login.html?oidc="+OIDC_UNKNOWN) || b.cookie(SESSION_COOKIE, "/") != "" {
		t.Fatalf("no session: %q", location)
	}
	page := b.do("GET", back.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "<strong>carol</strong>") || !strings.Contains(page, "alert-info") {
		t.Fatalf("the login page names the person: %s", page)
	}
	if again := b.do("GET", back.Header().Get("Location"), nil).Body.String(); strings.Contains(again, "carol") {
		t.Fatal("told once")
	}
	if _, ok := web.auth.users.Get("carol"); ok {
		t.Fatal("no user is made")
	}
	if n := activityCount(web, activity.ACTION_LOGIN_FAIL, portalUnknownDetail); n != 1 {
		t.Fatalf("the activity notes it: %d", n)
	}
	// a name sent to the login page by another site is not shown
	forged := newBrowser(t, web)
	forged.jar.SetCookies(&url.URL{Scheme: "http", Host: "slm.example.com", Path: "/login.html"}, []*http.Cookie{{Name: oidcWhoCookie, Value: "bWFsbG9yeQ.bad", Path: "/login.html"}})
	if page := forged.do("GET", "/login.html?oidc="+OIDC_UNKNOWN, nil).Body.String(); strings.Contains(page, "alert-info") {
		t.Fatal("only a name the app signed")
	}

	setPortal(web, provider.options(settings.PortalOptions{NewUsers: ROLE_VIEWER}))
	b = newBrowser(t, web)
	if back := b.signIn(provider, ""); back.Header().Get("Location") != "/index.html" {
		t.Fatalf("a user is made: %q", back.Header().Get("Location"))
	}
	carol, ok := web.auth.users.Get("carol")
	if !ok || carol.Role != ROLE_VIEWER || carol.HasPassword() || carol.Email != "carol@example.com" {
		t.Fatalf("a player without a password: %+v", carol)
	}
	if n := activityCount(web, activity.ACTION_USER_NEW, "carol: viewer"); n != 1 {
		t.Fatalf("the user made is in the activity: %d", n)
	}
}

// An email the provider does not say it checked is not used: anybody could have given the email
// of another person to the provider.
func TestOidcUnverifiedEmail(t *testing.T) {
	web, provider := oidcWeb(t, settings.PortalOptions{MatchBy: MATCH_EMAIL})
	web.auth.users.SetEmail("bob", "bob@example.com")
	for _, verified := range []any{false, "false", nil, "yes", 1} {
		provider.says(map[string]any{"preferred_username": "mallory", "email": "bob@example.com", "email_verified": verified})
		b := newBrowser(t, web)
		if location := b.signIn(provider, "").Header().Get("Location"); !strings.Contains(location, OIDC_UNKNOWN) {
			t.Fatalf("not checked (%v): %q", verified, location)
		}
	}
	for _, verified := range []any{true, "true"} {
		provider.says(map[string]any{"preferred_username": "robert", "email": "BOB@example.com", "email_verified": verified})
		b := newBrowser(t, web)
		if location := b.signIn(provider, "").Header().Get("Location"); location != "/index.html" || sessionName(web, b) != "bob" {
			t.Fatalf("checked (%v): %q", verified, location)
		}
	}
}

// sessionName is the user of the session of a browser.
func sessionName(web *Web, b *browser) string {
	request := httptest.NewRequest("GET", "/index.html", nil)
	request.AddCookie(&http.Cookie{Name: SESSION_COOKIE, Value: b.cookie(SESSION_COOKIE, "/")})
	if principal := web.auth.fromSession(request); principal != nil {
		return principal.Name
	}
	return ""
}

// The first sign-in links the user with the account of the provider: from then on that account
// is the user whatever name it has, and another account that takes the name or the email is not.
func TestOidcLinksTheAccount(t *testing.T) {
	web, provider := oidcWeb(t, settings.PortalOptions{})
	web.auth.users.SetEmail("bob", "bob@example.com")
	provider.says(map[string]any{"preferred_username": "bob"})
	if newBrowser(t, web).signIn(provider, ""); func() bool { bob, _ := web.auth.users.Get("bob"); return bob.OidcSubject == "" }() {
		t.Fatal("linked at the first sign-in")
	}
	provider.says(map[string]any{"sub": "sub-mallory", "preferred_username": "BOB", "email": "bob@example.com", "email_verified": true})
	if location := newBrowser(t, web).signIn(provider, "").Header().Get("Location"); !strings.Contains(location, OIDC_UNKNOWN) {
		t.Fatalf("another account renamed bob, with the email of bob: %q", location)
	}
	provider.says(map[string]any{"sub": "sub-bob", "preferred_username": "robert"})
	b := newBrowser(t, web)
	if location := b.signIn(provider, "").Header().Get("Location"); location != "/index.html" || sessionName(web, b) != "bob" {
		t.Fatalf("bob renamed at the provider is still bob: %q %q", location, sessionName(web, b))
	}
	// with NewUsers, the other account gets a user of its own
	setPortal(web, provider.options(settings.PortalOptions{NewUsers: ROLE_GUEST}))
	provider.says(map[string]any{"sub": "sub-mallory", "preferred_username": "bob"})
	b = newBrowser(t, web)
	b.signIn(provider, "")
	if name := sessionName(web, b); name != "bob-2" {
		t.Fatalf("a guest of its own: %q", name)
	}
	// an administrator who sets another email links the user again at the next sign-in
	web.auth.users.SetEmail("bob", "robert@example.com")
	if bob, _ := web.auth.users.Get("bob"); bob.OidcSubject != "" {
		t.Fatal("a new email unlinks the account")
	}
	// the user of the environment signs in with its password only: it has nowhere to keep the link
	setPortal(web, provider.options(settings.PortalOptions{}))
	web.auth.envUser = "root"
	provider.says(map[string]any{"preferred_username": "root"})
	if location := newBrowser(t, web).signIn(provider, "").Header().Get("Location"); !strings.Contains(location, OIDC_UNKNOWN) {
		t.Fatalf("the user of the environment: %q", location)
	}
}

// A session renewed by a new password stays the one of OpenID Connect, with its group.
func TestOidcSessionKeepsItsMark(t *testing.T) {
	web, provider := oidcWeb(t, settings.PortalOptions{AdminGroup: "slm-admins"})
	provider.says(map[string]any{"preferred_username": "bob", "groups": []string{"slm-admins"}})
	b := newBrowser(t, web)
	b.signIn(provider, "")
	if changed := b.do("POST", "/account/password", url.Values{"current": {"bravo-secret-22"}, "password": {"kilo-secret-77"}}); changed.Code != http.StatusSeeOther {
		t.Fatalf("the password: %v", changed.Code)
	}
	if !strings.HasSuffix(b.cookie(SESSION_COOKIE, "/"), "."+oidcAdminMark("slm-admins")) || b.do("GET", "/settings.html", nil).Code != http.StatusOK {
		t.Fatal("the renewed session keeps its mark")
	}
}

// The redirect address is the one of the login page the browser opened, else of the request.
func TestOidcRedirectAddress(t *testing.T) {
	request := httptest.NewRequest("GET", "http://10.0.0.5:3000/login/oidc", nil)
	if address := oidcRedirect(request); address != "http://10.0.0.5:3000/login/oidc/callback" {
		t.Fatalf("of the request: %q", address)
	}
	request.Header.Set("Referer", "https://switch.example.com/login.html?next=%2F")
	if address := oidcRedirect(request); address != "https://switch.example.com/login/oidc/callback" {
		t.Fatalf("of the login page, through a proxy: %q", address)
	}
	for _, referer := range []string{"https://elsewhere.example.com/", "javascript:alert(1)", "https://user@switch.example.com/login.html"} {
		request.Header.Set("Referer", referer)
		if address := oidcRedirect(request); address != "http://10.0.0.5:3000/login/oidc/callback" {
			t.Fatalf("%q: %q", referer, address)
		}
	}
}

// The members of the administrators group are administrators while the group is set: the role
// stored does not change.
func TestOidcAdministratorsGroup(t *testing.T) {
	web, provider := oidcWeb(t, settings.PortalOptions{AdminGroup: "slm-admins"})
	provider.says(map[string]any{"preferred_username": "bob", "groups": []string{"family", "SLM-Admins"}})
	b := newBrowser(t, web)
	b.signIn(provider, "")
	if !strings.HasSuffix(b.cookie(SESSION_COOKIE, "/"), "."+oidcAdminMark("slm-admins")) {
		t.Fatal("a session of an administrator by the group")
	}
	if code := b.do("GET", "/settings.html", nil).Code; code != http.StatusOK {
		t.Fatalf("an administrator: %v", code)
	}
	if bob, _ := web.auth.users.Get("bob"); bob.Role != ROLE_VIEWER {
		t.Fatal("the role stored stays")
	}
	if n := activityCount(web, activity.ACTION_PORTAL_OIDC, ROLE_ADMIN); n != 1 {
		t.Fatalf("the activity says the group made an administrator: %d", n)
	}
	// without the group set, or with another group, the same session is the one of a player
	setPortal(web, provider.options(settings.PortalOptions{}))
	if code := b.do("GET", "/settings.html", nil).Code; code != http.StatusForbidden {
		t.Fatalf("the group no longer counts: %v", code)
	}
	setPortal(web, provider.options(settings.PortalOptions{AdminGroup: "family"}))
	if code := b.do("GET", "/settings.html", nil).Code; code != http.StatusForbidden {
		t.Fatalf("another group: %v", code)
	}
	// a mark changed in the cookie breaks its signature
	session := b.cookie(SESSION_COOKIE, "/")
	setPortal(web, provider.options(settings.PortalOptions{AdminGroup: "slm-admins"}))
	provider.says(map[string]any{"preferred_username": "bob", "groups": "family"})
	b = newBrowser(t, web)
	b.signIn(provider, "")
	forged := strings.TrimSuffix(b.cookie(SESSION_COOKIE, "/"), "."+sessionOidc) + "." + oidcAdminMark("slm-admins")
	request := httptest.NewRequest("GET", "/settings.html", nil)
	request.AddCookie(&http.Cookie{Name: SESSION_COOKIE, Value: forged})
	if web.auth.fromSession(request) != nil {
		t.Fatal("a mark made up")
	}
	request = httptest.NewRequest("GET", "/settings.html", nil)
	request.AddCookie(&http.Cookie{Name: SESSION_COOKIE, Value: session})
	if principal := web.auth.fromSession(request); principal == nil || principal.Role != ROLE_ADMIN {
		t.Fatal("the session of the group, with the group set again")
	}
}

// While there is no user nothing works: the first administrator needs the setup code.
func TestOidcNeverSkipsTheSetupCode(t *testing.T) {
	provider := newFakeProvider(t)
	web := usersWeb(t)
	web.auth.portalSettings = func() settings.PortalOptions { return settings.ReadSettings(web.dataFolder).Portal }
	web.auth.oidc.client = newOidcClient(provider.server.Client().Transport)
	setPortal(web, provider.options(settings.PortalOptions{NewUsers: ROLE_VIEWER, AdminGroup: "slm-admins"}))
	provider.says(map[string]any{"preferred_username": "mallory", "groups": []string{"slm-admins"}})
	b := newBrowser(t, web)
	if location := b.do("GET", oidcLoginPath, nil).Header().Get("Location"); location != "/login.html" {
		t.Fatalf("the button does nothing: %q", location)
	}
	code, state := provider.authorize(provider.server.URL + "/authorize?response_type=code&client_id=" + testOidcClient + "&code_challenge=x&code_challenge_method=S256&state=s&nonce=n&scope=openid")
	b.do("GET", oidcCallbackPath+"?code="+code+"&state="+state, nil)
	if web.auth.Enabled() || b.cookie(SESSION_COOKIE, "/") != "" || provider.exchanges.Load() != 0 {
		t.Fatal("no user is made and nobody signs in")
	}
}

// The passwords, basic authentication and the API keys keep working with OpenID Connect on.
func TestOidcKeepsPasswordsAndAPIKeys(t *testing.T) {
	web, _ := oidcWeb(t, settings.PortalOptions{AdminGroup: "slm-admins"})
	handler := web.auth.middleware(web.router)
	admin := &client{t: t, handler: handler}
	admin.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	if len(admin.cookies) != 1 || strings.Count(admin.cookies[0].Value, ".") != 2 || admin.do("GET", "/settings.html", nil).Code != http.StatusOK {
		t.Fatal("the login page opens a session without a mark")
	}
	key := createKey(t, admin, "Home Assistant", ROLE_VIEWER)
	api := &client{t: t, handler: handler}
	if code := api.do("GET", "/index.html", nil, "Authorization", bearer(key.Key)).Code; code != http.StatusOK {
		t.Fatalf("an API key: %v", code)
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
	if location := admin.do("POST", "/logout", nil).Header().Get("Location"); location != "/login.html" {
		t.Fatalf("Log out of a session with a password stays in the app: %q", location)
	}
}

// The provider is read over https only: its address, the addresses it gives, and a redirect.
func TestOidcHttpsOnly(t *testing.T) {
	provider := newFakeProvider(t)
	client := newOidcClient(provider.server.Client().Transport)
	if _, err := discoverOidc(client, provider.server.URL); err != nil {
		t.Fatalf("over https: %v", err)
	}
	if _, err := discoverOidc(client, strings.Replace(provider.server.URL, "https://", "http://", 1)); err == nil {
		t.Fatal("over http")
	}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": "x"})
	}))
	defer plain.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirect.Close()
	if _, err := discoverOidc(newOidcClient(redirect.Client().Transport), redirect.URL); !errors.Is(err, errOidcHttps) {
		t.Fatalf("a redirect to http: %v", err)
	}
	var mixedURL string
	mixed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": mixedURL, "authorization_endpoint": mixedURL + "/authorize", "token_endpoint": "http://example.com/token", "jwks_uri": mixedURL + "/keys"})
	}))
	defer mixed.Close()
	mixedURL = mixed.URL
	if _, err := discoverOidc(newOidcClient(mixed.Client().Transport), mixed.URL); !errors.Is(err, errOidcHttps) {
		t.Fatalf("a provider that gives an http address: %v", err)
	}
	for issuer, valid := range map[string]bool{"https://id.example.com": true, "https://auth.example.com/application/o/switch/": true, "https://kc.example.com/realms/home": true,
		"http://id.example.com": false, "id.example.com": false, "https://": false, "https://user@id.example.com": false, "https://id.example.com/?a=b": false, "https://id.example.com/#x": false, "": false} {
		if validIssuer(issuer) != valid {
			t.Errorf("issuer %q: %v", issuer, !valid)
		}
	}
}

// The options of Settings: the provider is read before they are saved, the client secret never
// goes back to the page and an empty field keeps it.
func TestOidcSettingsForm(t *testing.T) {
	provider := newFakeProvider(t)
	web := portalWeb(t, settings.PortalOptions{})
	web.auth.oidc.client = newOidcClient(provider.server.Client().Transport)
	admin := &client{t: t, handler: web.auth.middleware(web.withActivityLog(web.router))}
	admin.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	good := url.Values{"portal_oidc": {"true"}, "portal_oidc_issuer": {provider.server.URL + "/.well-known/openid-configuration"}, "portal_oidc_client_id": {testOidcClient},
		"portal_oidc_client_secret": {testOidcSecret}, "portal_oidc_button": {"PocketID"}, "portal_oidc_scopes": {" openid  profile email groups "}}
	if saved := admin.do("POST", "/settings/portal", good); saved.Code != http.StatusOK {
		t.Fatalf("saved: %v %s", saved.Code, saved.Body.String())
	}
	stored := settings.ReadSettings(web.dataFolder).Portal
	if !stored.Oidc || stored.OidcIssuer != provider.server.URL || stored.OidcClientSecret != testOidcSecret || stored.OidcScopes != "openid profile email groups" {
		t.Fatalf("the options: %+v", stored)
	}
	// an empty secret keeps the saved one; emptying the client ID removes both
	kept := url.Values{}
	for key, values := range good {
		kept[key] = values
	}
	kept.Set("portal_oidc_client_secret", "")
	admin.do("POST", "/settings/portal", kept)
	if settings.ReadSettings(web.dataFolder).Portal.OidcClientSecret != testOidcSecret {
		t.Fatal("an empty field keeps the secret")
	}
	admin.do("POST", "/settings/portal", url.Values{"portal_oidc_issuer": {provider.server.URL}, "portal_oidc_client_secret": {""}})
	if stored := settings.ReadSettings(web.dataFolder).Portal; stored.OidcClientSecret != "" || stored.Oidc {
		t.Fatalf("emptying the client ID removes the secret: %+v", stored)
	}
	for field, value := range map[string]string{"portal_oidc_issuer": "http://id.example.com", "portal_oidc_client_id": "", "portal_oidc_button": strings.Repeat("x", 41),
		"portal_oidc_scopes": `openid "profile"`} {
		bad := url.Values{}
		for key, values := range good {
			bad[key] = values
		}
		bad.Set(field, value)
		refused := admin.do("POST", "/settings/portal", bad)
		var answer ErrorResponse
		json.Unmarshal(refused.Body.Bytes(), &answer)
		if refused.Code != http.StatusBadRequest || len(answer.FieldErrors) != 1 || answer.FieldErrors[0].Field != field {
			t.Errorf("%s=%q: %v %s", field, value, refused.Code, refused.Body.String())
		}
	}
	// the provider names itself with another address: the right one is told
	wrong := url.Values{}
	for key, values := range good {
		wrong[key] = values
	}
	wrong.Set("portal_oidc_issuer", provider.server.URL+"/")
	refused := admin.do("POST", "/settings/portal", wrong)
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "calls itself "+provider.server.URL+":") {
		t.Fatalf("the address of the provider: %v %s", refused.Code, refused.Body.String())
	}
	wrong.Set("portal_oidc_issuer", "https://127.0.0.1:1")
	if refused := admin.do("POST", "/settings/portal", wrong); refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "portal_oidc_issuer") {
		t.Fatalf("a provider that does not answer: %v %s", refused.Code, refused.Body.String())
	}
	// a player cannot change them
	player := &client{t: t, handler: admin.handler}
	player.do("POST", "/login.html", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}})
	if code := player.do("POST", "/settings/portal", good).Code; code != http.StatusForbidden {
		t.Fatalf("a player: %v", code)
	}
}

// With the client ID set in the environment, saving Settings keeps the secret saved there.
func TestOidcSecretWithTheClientOfTheEnvironment(t *testing.T) {
	provider := newFakeProvider(t)
	t.Setenv(OIDC_CLIENT_ID_ENV, testOidcClient)
	web := portalWeb(t, settings.PortalOptions{Oidc: true, OidcIssuer: provider.server.URL, OidcClientSecret: testOidcSecret})
	web.auth.oidc.client = newOidcClient(provider.server.Client().Transport)
	admin := &client{t: t, handler: web.auth.middleware(web.router)}
	admin.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	if saved := admin.do("POST", "/settings/portal", url.Values{"portal_oidc": {"true"}, "portal_oidc_issuer": {provider.server.URL}, "portal_oidc_client_secret": {""}}); saved.Code != http.StatusOK {
		t.Fatalf("saved: %v %s", saved.Code, saved.Body.String())
	}
	if secret := settings.ReadSettings(web.dataFolder).Portal.OidcClientSecret; secret != testOidcSecret {
		t.Fatal("the secret is kept")
	}
}

// An environment variable wins over its option of Settings, and turns it on.
func TestOidcEnvironmentWinsOverSettings(t *testing.T) {
	provider := newFakeProvider(t)
	t.Setenv(OIDC_ISSUER_ENV, provider.server.URL+"/.well-known/openid-configuration")
	t.Setenv(OIDC_CLIENT_ID_ENV, testOidcClient)
	t.Setenv(OIDC_CLIENT_SECRET_ENV, testOidcSecret)
	t.Setenv(OIDC_SCOPES_ENV, "openid email")
	web := portalWeb(t, settings.PortalOptions{OidcIssuer: "https://elsewhere.example.com", OidcClientId: "other", OidcButton: "Keycloak"})
	web.auth.oidc.client = newOidcClient(provider.server.Client().Transport)
	options := web.auth.portal()
	if !oidcReady(options) || options.OidcIssuer != provider.server.URL || options.OidcClientSecret != testOidcSecret || options.OidcButton != "Keycloak" {
		t.Fatalf("the environment wins: %+v", options)
	}
	if scopes := oidcScopes(options); strings.Join(scopes, " ") != "openid email" {
		t.Fatalf("the scopes: %v", scopes)
	}
	if scopes := oidcScopes(settings.PortalOptions{OidcScopes: "profile email"}); strings.Join(scopes, " ") != "openid profile email" {
		t.Fatalf("openid is always asked for: %v", scopes)
	}
	if scopes := oidcScopes(settings.PortalOptions{}); strings.Join(scopes, " ") != defaultOidcScopes {
		t.Fatalf("the default scopes: %v", scopes)
	}
	provider.says(map[string]any{"preferred_username": "bob"})
	b := newBrowser(t, web)
	if location := b.signIn(provider, "").Header().Get("Location"); location != "/index.html" {
		t.Fatalf("a sign-in with the options of the environment: %q", location)
	}
	if view := web.auth.portalView(); !view.Env[OIDC_CLIENT_SECRET_ENV] || view.Options.OidcClientSecret != "" {
		t.Fatal("the secret of the environment never goes to the page either")
	}
}

// A session opened with a password before this version keeps working, and one with a mark
// nobody knows does not.
func TestSessionMarks(t *testing.T) {
	web := portalWeb(t, settings.PortalOptions{})
	request := httptest.NewRequest("POST", "/login.html", nil)
	plain := web.auth.sessionCookie(request, "bob").Value
	marked := web.auth.markedSessionCookie(request, "bob", sessionOidc).Value
	admin := web.auth.markedSessionCookie(request, "bob", oidcAdminMark("slm-admins")).Value
	for value, valid := range map[string]bool{plain: true, marked: true, admin: true, plain + ".o": false, marked + "x": false, strings.TrimSuffix(marked, ".o") + ".zz": false,
		strings.TrimSuffix(marked, ".o") + ".oa-zz": false, strings.TrimSuffix(marked, ".o") + "." + oidcAdminMark("other"): false} {
		check := httptest.NewRequest("GET", "/index.html", nil)
		check.AddCookie(&http.Cookie{Name: SESSION_COOKIE, Value: value})
		if (web.auth.fromSession(check) != nil) != valid {
			t.Errorf("session %q: %v", value, !valid)
		}
	}
}
