package web

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const (
	testCfTeam = "myteam"
	testCfKid  = "key-1"
)

var testCfAud = strings.Repeat("a1", 32)

// cfKeyServer serves the public keys of a team of Cloudflare Access, made for the test, over
// https; it counts the times they are fetched.
type cfKeyServer struct {
	key     *rsa.PrivateKey
	fetches atomic.Int32
}

func newCfKeyServer(t *testing.T) *cfKeyServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys := &cfKeyServer{key: key}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys.fetches.Add(1)
		if r.URL.Path != "/cdn-cgi/access/certs" {
			http.NotFound(w, r)
			return
		}
		e := big.NewInt(int64(key.E)).Bytes()
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kid": testCfKid, "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)},
		}})
	}))
	savedClient, savedURL := cfAccessClient, cfAccessCertsURL
	t.Cleanup(func() {
		server.Close()
		cfAccessClient, cfAccessCertsURL = savedClient, savedURL
	})
	cfAccessClient = server.Client()
	cfAccessCertsURL = func(team string) string { return server.URL + "/cdn-cgi/access/certs" }
	return keys
}

// cfToken signs claims as Cloudflare Access does, with key and the id kid.
func cfToken(t *testing.T, key *rsa.PrivateKey, header map[string]any, claims map[string]any) string {
	t.Helper()
	encode := func(value any) string {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(data)
	}
	unsigned := encode(header) + "." + encode(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// cfClaimsFor are the claims of a token of the team for the application, valid for an hour.
func cfClaimsFor(email string) map[string]any {
	now := time.Now()
	return map[string]any{"aud": []string{testCfAud}, "email": email, "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nbf": now.Unix(),
		"iss": cfAccessIssuer(testCfTeam), "type": "app", "identity_nonce": "nonce", "sub": "7335d417-61da-459d-899c-0a01c76a2f94", "country": "ES"}
}

var cfHeader = map[string]any{"alg": "RS256", "kid": testCfKid, "typ": "JWT"}

var cloudflarePortal = settings.PortalOptions{Cloudflare: true, CloudflareTeam: testCfTeam, CloudflareAud: testCfAud}

// A token of Cloudflare Access signed by the team, for the application and not expired signs in
// the user with its email, wherever the request comes from; any other token signs in nobody and
// is told once an hour.
func TestCloudflareAccessTokens(t *testing.T) {
	keys := newCfKeyServer(t)
	web := portalWeb(t, cloudflarePortal)
	trustedProxyNets = parseTrustedProxies("172.17.0.0/16")
	web.auth.users.SetEmail("alice", "alice@example.com")
	web.auth.users.SetEmail("bob", "bob@example.com")
	// cloudflared on the local network, as in a container next to the app
	tunnel := &client{t: t, handler: web.auth.middleware(web.router), ip: "172.17.0.5"}
	signIn := func(token string) int {
		return tunnel.do("GET", "/settings.html", nil, CF_ACCESS_HEADER, token, "Cf-Connecting-Ip", "203.0.113.20").Code
	}

	if code := signIn(cfToken(t, keys.key, cfHeader, cfClaimsFor("Alice@Example.com"))); code != http.StatusOK {
		t.Fatalf("a valid token: %v", code)
	}
	if code := signIn(cfToken(t, keys.key, cfHeader, cfClaimsFor("bob@example.com"))); code != http.StatusForbidden {
		t.Fatalf("a player keeps the role of a player: %v", code)
	}
	if n := keys.fetches.Load(); n != 1 {
		t.Fatalf("the keys are kept: fetched %d times", n)
	}
	if n := activityCount(web, activity.ACTION_PORTAL_CF, ""); n != 2 {
		t.Fatalf("told once a day by user: %d", n)
	}
	for _, entry := range web.activities().List() {
		if entry.Action == activity.ACTION_PORTAL_CF && entry.IP != "203.0.113.20" {
			t.Fatalf("the address of the person, from Cloudflare: %q", entry.IP)
		}
	}

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	expired := cfClaimsFor("alice@example.com")
	expired["exp"] = time.Now().Add(-2 * time.Minute).Unix()
	otherAud := cfClaimsFor("alice@example.com")
	otherAud["aud"] = []string{strings.Repeat("b2", 32)}
	oneAud := cfClaimsFor("alice@example.com")
	oneAud["aud"] = strings.Repeat("b2", 32)
	otherTeam := cfClaimsFor("alice@example.com")
	otherTeam["iss"] = "https://otherteam.cloudflareaccess.com"
	early := cfClaimsFor("alice@example.com")
	early["nbf"] = time.Now().Add(10 * time.Minute).Unix()
	noExpiry := cfClaimsFor("alice@example.com")
	delete(noExpiry, "exp")
	service := cfClaimsFor("")
	service["common_name"] = "abc.access"
	valid := cfToken(t, keys.key, cfHeader, cfClaimsFor("alice@example.com"))
	parts := strings.Split(valid, ".")
	tampered := cfClaimsFor("alice@example.com")
	tampered["email"] = "bob@example.com"
	unsigned := func(header map[string]any) string {
		data, _ := json.Marshal(header)
		claims, _ := json.Marshal(cfClaimsFor("alice@example.com"))
		return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(claims) + "."
	}
	for name, token := range map[string]string{
		"expired":            cfToken(t, keys.key, cfHeader, expired),
		"another audience":   cfToken(t, keys.key, cfHeader, otherAud),
		"one other audience": cfToken(t, keys.key, cfHeader, oneAud),
		"another team":       cfToken(t, keys.key, cfHeader, otherTeam),
		"not valid yet":      cfToken(t, keys.key, cfHeader, early),
		"no expiry":          cfToken(t, keys.key, cfHeader, noExpiry),
		"a service token":    cfToken(t, keys.key, cfHeader, service),
		"another key":        cfToken(t, other, cfHeader, cfClaimsFor("alice@example.com")),
		"claims changed":     parts[0] + "." + strings.Split(cfToken(t, other, cfHeader, tampered), ".")[1] + "." + parts[2],
		"no signature":       parts[0] + "." + parts[1] + ".",
		"alg none":           unsigned(map[string]any{"alg": "none", "kid": testCfKid}),
		"alg HS256":          unsigned(map[string]any{"alg": "HS256", "kid": testCfKid}),
		"an unknown key id":  cfToken(t, keys.key, map[string]any{"alg": "RS256", "kid": "key-2"}, cfClaimsFor("alice@example.com")),
		"not a token":        "garbage",
	} {
		if code := signIn(token); code != http.StatusUnauthorized {
			t.Errorf("%s: %v", name, code)
		}
	}
	if n := activityCount(web, activity.ACTION_LOGIN_FAIL, portalTokenDetail); n != 1 {
		t.Fatalf("tokens not valid are told once an hour by address: %d", n)
	}
	// a person Cloudflare let in without a user of the app
	if code := signIn(cfToken(t, keys.key, cfHeader, cfClaimsFor("carol@example.com"))); code != http.StatusUnauthorized {
		t.Fatalf("an email no user has: %v", code)
	}
	// the token says the email only: a name is never matched with it
	web.auth.users.Add("dave", "delta-secret-44", ROLE_ADMIN)
	if code := signIn(cfToken(t, keys.key, cfHeader, cfClaimsFor("dave"))); code != http.StatusUnauthorized {
		t.Fatalf("a name in the email: %v", code)
	}

	// made as a guest when the options say so, and found by the email the next time
	setPortal(web, settings.PortalOptions{Cloudflare: true, CloudflareTeam: testCfTeam, CloudflareAud: testCfAud, NewUsers: ROLE_GUEST})
	for range 2 {
		if code := tunnel.do("GET", "/index.html", nil, CF_ACCESS_HEADER, cfToken(t, keys.key, cfHeader, cfClaimsFor("carol@example.com"))).Code; code != http.StatusOK {
			t.Fatalf("a new guest: %v", code)
		}
	}
	if user, ok := web.auth.users.FindByEmail("carol@example.com"); !ok || user.Name != "carol" || user.Role != ROLE_GUEST || web.auth.users.Count() != 4 {
		t.Fatalf("one guest made: %+v", user)
	}

	// the token in the header of a header portal is not a name, nor the other way round
	if code := tunnel.do("GET", "/settings.html", nil, "Remote-User", "alice").Code; code != http.StatusUnauthorized {
		t.Fatalf("a header with Cloudflare Access only: %v", code)
	}
	// Log out ends the session of Cloudflare Access
	if location := tunnel.do("POST", "/logout", nil, CF_ACCESS_HEADER, valid).Header().Get("Location"); location != cfAccessLogoutPath {
		t.Fatalf("to the logout page of Cloudflare Access: %q", location)
	}
}

// The keys are fetched again when they are old, or when a token names a key not known, at most
// once a minute; while a fetch fails the keys there are stay in use.
func TestCloudflareAccessKeysAreRenewed(t *testing.T) {
	keys := newCfKeyServer(t)
	cache := &cfAccessKeys{}
	now := time.Now()
	// valid for longer than the keys are kept
	claims := cfClaimsFor("alice@example.com")
	claims["exp"] = now.Add(cfKeysMaxAge + 24*time.Hour).Unix()
	token := cfToken(t, keys.key, cfHeader, claims)
	verify := func(token string, at time.Time) error {
		_, err := cache.verify(token, testCfTeam, testCfAud, at)
		return err
	}
	if err := verify(token, now); err != nil {
		t.Fatal(err)
	}
	unknown := cfToken(t, keys.key, map[string]any{"alg": "RS256", "kid": "key-2"}, cfClaimsFor("alice@example.com"))
	if err := verify(unknown, now.Add(time.Second)); !errors.Is(err, errCfKey) || keys.fetches.Load() != 1 {
		t.Fatalf("an unknown key right after a fetch: %v (%d fetches)", err, keys.fetches.Load())
	}
	if err := verify(unknown, now.Add(2*time.Minute)); !errors.Is(err, errCfKey) || keys.fetches.Load() != 2 {
		t.Fatalf("an unknown key a while later fetches them again: %v (%d fetches)", err, keys.fetches.Load())
	}
	for range 5 {
		verify(unknown, now.Add(2*time.Minute+time.Second))
	}
	if keys.fetches.Load() != 2 {
		t.Fatalf("at most once a minute: %d fetches", keys.fetches.Load())
	}
	// old keys are fetched again; a fetch that fails keeps them
	cfAccessCertsURL = func(team string) string { return "https://127.0.0.1:1/certs" }
	if err := verify(token, now.Add(cfKeysMaxAge+time.Hour)); err != nil {
		t.Fatalf("the keys there are stay in use: %v", err)
	}
	// another team starts with no keys
	if _, err := cache.verify(token, "otherteam", testCfAud, now.Add(cfKeysMaxAge+2*time.Hour)); err == nil {
		t.Fatal("the keys of another team")
	}
}

// The keys decide who signs in: they are only fetched over https.
func TestCloudflareAccessKeysOnlyOverHttps(t *testing.T) {
	if address := cfAccessCertsURL("myteam"); address != "https://myteam.cloudflareaccess.com/cdn-cgi/access/certs" {
		t.Fatalf("the keys of a team: %q", address)
	}
	keys := newCfKeyServer(t)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the keys are never asked for over http")
	}))
	defer plain.Close()
	cfAccessCertsURL = func(team string) string { return plain.URL + "/cdn-cgi/access/certs" }
	cache := &cfAccessKeys{}
	if _, err := cache.verify(cfToken(t, keys.key, cfHeader, cfClaimsFor("alice@example.com")), testCfTeam, testCfAud, time.Now()); !errors.Is(err, errNotHttps) {
		t.Fatalf("an http address: %v", err)
	}
}

// Without a user, a valid token of Cloudflare Access does nothing either.
func TestCloudflareAccessNeverSkipsTheSetupCode(t *testing.T) {
	keys := newCfKeyServer(t)
	web := usersWeb(t)
	web.auth.portalSettings = func() settings.PortalOptions {
		return settings.PortalOptions{Cloudflare: true, CloudflareTeam: testCfTeam, CloudflareAud: testCfAud, NewUsers: ROLE_VIEWER}
	}
	tunnel := &client{t: t, handler: web.auth.middleware(web.router), ip: "172.17.0.5"}
	token := cfToken(t, keys.key, cfHeader, cfClaimsFor("mallory@example.com"))
	tunnel.do("GET", "/index.html", nil, CF_ACCESS_HEADER, token)
	created := tunnel.do("POST", "/users/create", url.Values{"name": {"mallory"}, "password": {"mallory-secret-1"}}, CF_ACCESS_HEADER, token)
	if web.auth.users.Count() != 0 || web.auth.Enabled() || !strings.Contains(created.Header().Get("Location"), "error=") {
		t.Fatalf("the first administrator still needs the setup code: %q", created.Header().Get("Location"))
	}
	if keys.fetches.Load() != 0 {
		t.Fatal("while login is off, no token is even looked at")
	}
}
