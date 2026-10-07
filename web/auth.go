package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	SESSION_COOKIE   = "slm_session"
	SESSION_FILENAME = "session.key"
	sessionLifetime  = 30 * 24 * time.Hour

	// failed logins allowed per key (an address or an account) before it is blocked
	maxLoginFailures = 10
	loginWindow      = 15 * time.Minute
	// a block doubles with each further failure, up to this for an address and less for an
	// account, so flooding a user name cannot lock its owner out for a whole day
	maxLoginBlock   = 24 * time.Hour
	maxAccountBlock = 1 * time.Hour
	// from this many failures a small growing delay is added to each attempt
	tarpitAfter = 3
	tarpitStep  = 400 * time.Millisecond
	maxTarpit   = 4 * time.Second
	// failed logins from everywhere per minute before every attempt is delayed, and that delay
	globalFailureCap = 200
	globalFloodDelay = 2 * time.Second
	// addresses and accounts remembered at most, so an attack cannot fill memory
	maxLimiterEntries = 20000

	// successful basic authentication is remembered, so API clients do not pay for bcrypt
	// on every request
	basicAuthCacheTime = 5 * time.Minute
)

// Principal is who sends a request.
type Principal struct {
	Name string
	Role string
	// "env" for the user set with environment variables, "" when login is disabled
	Source string
}

func (p *Principal) IsAdmin() bool {
	return p != nil && p.Role == ROLE_ADMIN
}

// AuthInfo is what the templates know about the user.
type AuthInfo struct {
	Enabled bool
	User    string
	IsAdmin bool
	FromEnv bool
	// the password of SLM_AUTH_PASSWORD is weak: shown to the administrators
	WeakEnvPassword bool
}

type principalKey struct{}

// principalFrom returns who sent the request. Without the authentication middleware (in
// tests) the request is treated like one of an administrator with login disabled.
func principalFrom(r *http.Request) *Principal {
	if p, ok := r.Context().Value(principalKey{}).(*Principal); ok {
		return p
	}
	return &Principal{Role: ROLE_ADMIN}
}

// Auth authenticates and authorizes the requests.
type Auth struct {
	users       *UserStore
	envUser     string
	envPassword string
	secret      []byte
	limiter     *loginLimiter
	basicCache  sync.Map // sha256 of name and password -> expiry
	// sessions ended with Log out before they expire
	revoked *revokedSessions
	// login disabled: requests from outside the local network are refused, unless allowed
	remoteWithoutLogin bool
	// why the password of SLM_AUTH_PASSWORD is weak, or nil
	envPasswordWeak error
}

func newAuth(dataFolder string) (*Auth, error) {
	envUser, envPassword, envEnabled, err := authFromEnv()
	if err != nil {
		return nil, err
	}
	if !envEnabled {
		envUser, envPassword = "", ""
	}
	users, err := loadUserStore(dataFolder, envUser)
	if err != nil {
		return nil, err
	}
	secret, err := loadSessionSecret(dataFolder)
	if err != nil {
		return nil, err
	}
	return &Auth{users: users, envUser: envUser, envPassword: envPassword, secret: secret, limiter: newLoginLimiter(),
		revoked: loadRevokedSessions(dataFolder), remoteWithoutLogin: os.Getenv(REMOTE_WITHOUT_LOGIN_ENV) == "true",
		envPasswordWeak: weakEnvPassword(envUser, envPassword)}, nil
}

// loadSessionSecret reads the key that signs the session cookies, creating it on first use.
func loadSessionSecret(dataFolder string) ([]byte, error) {
	path := filepath.Join(dataFolder, SESSION_FILENAME)
	if data, err := os.ReadFile(path); err == nil {
		if secret, err := hex.DecodeString(strings.TrimSpace(string(data))); err == nil && len(secret) >= 32 {
			return secret, nil
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(secret)), 0600); err != nil {
		return nil, err
	}
	return secret, nil
}

// Enabled reports whether a login is required: a user exists or one is set in the environment.
func (a *Auth) Enabled() bool {
	return a.envUser != "" || a.users.Count() > 0
}

// verify checks a user name and password.
func (a *Auth) verify(name string, password string) (*Principal, bool) {
	if a.envUser != "" {
		expectedUser := sha256.Sum256([]byte(a.envUser))
		expectedPassword := sha256.Sum256([]byte(a.envPassword))
		givenUser := sha256.Sum256([]byte(name))
		givenPassword := sha256.Sum256([]byte(password))
		if subtle.ConstantTimeCompare(givenUser[:], expectedUser[:]) == 1 && subtle.ConstantTimeCompare(givenPassword[:], expectedPassword[:]) == 1 {
			return &Principal{Name: a.envUser, Role: ROLE_ADMIN, Source: "env"}, true
		}
	}
	if user, ok := a.users.Verify(name, password); ok {
		return &Principal{Name: user.Name, Role: user.Role}, true
	}
	return nil, false
}

// lookup returns the current role and session tag of a user. The tag changes with the
// password, so changing it ends the existing sessions.
func (a *Auth) lookup(name string) (*Principal, string, bool) {
	if a.envUser != "" && name == a.envUser {
		tag := sha256.Sum256([]byte("env\x00" + a.envPassword))
		return &Principal{Name: a.envUser, Role: ROLE_ADMIN, Source: "env"}, hex.EncodeToString(tag[:8]), true
	}
	user, ok := a.users.Get(name)
	if !ok {
		return nil, "", false
	}
	tag := sha256.Sum256([]byte(user.PasswordHash))
	return &Principal{Name: user.Name, Role: user.Role}, hex.EncodeToString(tag[:8]), true
}

func (a *Auth) sign(name string, expiry int64, tag string) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(name + "\x00" + strconv.FormatInt(expiry, 10) + "\x00" + tag))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// sessionCookie logs the principal in for sessionLifetime.
func (a *Auth) sessionCookie(r *http.Request, name string) *http.Cookie {
	_, tag, _ := a.lookup(name)
	expiry := time.Now().Add(sessionLifetime)
	value := base64.RawURLEncoding.EncodeToString([]byte(name)) + "." + strconv.FormatInt(expiry.Unix(), 10) + "." + a.sign(name, expiry.Unix(), tag)
	return &http.Cookie{Name: SESSION_COOKIE, Value: value, Path: "/", Expires: expiry, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHttps(r)}
}

// endSession makes the session cookie of the request invalid, also if it was copied elsewhere.
func (a *Auth) endSession(r *http.Request) {
	cookie, err := r.Cookie(SESSION_COOKIE)
	if err != nil || a.fromSession(r) == nil {
		return
	}
	parts := strings.Split(cookie.Value, ".")
	if expiry, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
		a.revoked.add(parts[2], expiry)
	}
}

func clearSessionCookie(r *http.Request) *http.Cookie {
	return &http.Cookie{Name: SESSION_COOKIE, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHttps(r)}
}

func isHttps(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// fromSession returns the principal of a valid session cookie.
func (a *Auth) fromSession(r *http.Request) *Principal {
	cookie, err := r.Cookie(SESSION_COOKIE)
	if err != nil {
		return nil
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		return nil
	}
	nameBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return nil
	}
	principal, tag, ok := a.lookup(string(nameBytes))
	if !ok {
		return nil
	}
	if !hmac.Equal([]byte(parts[2]), []byte(a.sign(string(nameBytes), expiry, tag))) {
		return nil
	}
	if a.revoked.has(parts[2]) {
		return nil
	}
	return principal
}

// fromBasicAuth checks the Authorization header, used by API clients.
func (a *Auth) fromBasicAuth(r *http.Request) (principal *Principal, attempted bool) {
	name, password, ok := r.BasicAuth()
	if !ok {
		return nil, false
	}
	key := sha256.Sum256([]byte(name + "\x00" + password))
	if expiry, ok := a.basicCache.Load(key); ok && time.Now().Before(expiry.(time.Time)) {
		if principal, _, ok := a.lookup(name); ok {
			return principal, true
		}
	}
	principal, ok = a.verify(name, password)
	if !ok {
		return nil, true
	}
	a.basicCache.Store(key, time.Now().Add(basicAuthCacheTime))
	return principal, true
}

// forget drops cached basic authentication, after a password or role change.
func (a *Auth) forget() {
	a.basicCache.Range(func(key, _ any) bool {
		a.basicCache.Delete(key)
		return true
	})
}

// public paths work without login: the login page and the files it needs
func isPublicPath(path string) bool {
	return path == "/login.html" || path == "/logout" || path == "/sw.js" ||
		strings.HasPrefix(path, "/resources/static/") || strings.HasPrefix(path, "/resources/vendor/")
}

// pages and actions of administrators; read-only users can only look and download
var adminOnlyPages = map[string]struct{}{"/settings.html": {}, "/organize.html": {}, "/users.html": {}, "/compress.html": {}, "/space.html": {}, "/update.html": {}, "/backup/download": {},
	"/compress/xci-list": {}, "/compress/nsz-list": {}, "/diagnostics.html": {}}

func viewerAllowed(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		_, adminOnly := adminOnlyPages[r.URL.Path]
		// the backups hold the settings and the users
		return !adminOnly && !strings.HasPrefix(r.URL.Path, "/backup/")
	case http.MethodPost:
		// the list of files of the SD card planner is a download, as the pages are
		return r.URL.Path == "/account/password" || r.URL.Path == "/account/language" || r.URL.Path == "/logout" || r.URL.Path == "/sd/list.txt"
	}
	return false
}

// middleware requires a login when users exist and limits read-only users.
func (a *Auth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var principal *Principal
		switch {
		case isPublicPath(r.URL.Path):
			principal = a.fromSession(r)
		case !a.Enabled():
			// without login everybody is an administrator: only on the local network, so an app
			// published on the internet before an administrator is created is not open to anyone
			if !a.remoteWithoutLogin && !isLocalRequest(r) {
				http.Error(w, remoteWithoutLoginMessage, http.StatusForbidden)
				return
			}
			principal = &Principal{Role: ROLE_ADMIN}
		default:
			principal = a.fromSession(r)
			if principal == nil {
				ip := clientIp(r)
				name, _, _ := r.BasicAuth()
				keys := loginKeys(ip, name)
				retryAfter, delay := a.limiter.check(keys...)
				if retryAfter > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
					http.Error(w, "too many failed logins, try again later", http.StatusTooManyRequests)
					return
				}
				if delay > 0 {
					sleepFor(delay)
				}
				var attempted bool
				principal, attempted = a.fromBasicAuth(r)
				if principal == nil {
					if attempted {
						a.limiter.fail(keys...)
					}
					a.unauthorized(w, r)
					return
				}
				a.limiter.succeed(keys...)
			}
		}

		if principal != nil && !principal.IsAdmin() && !viewerAllowed(r) {
			http.Error(w, "this action needs an administrator", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	})
}

// unauthorized sends browsers to the login page and asks other clients for credentials.
func (a *Auth) unauthorized(w http.ResponseWriter, r *http.Request) {
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.Contains(r.Header.Get("Accept"), "text/html") {
		target := "/login.html"
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			target += "?next=" + url.QueryEscape(r.URL.RequestURI())
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	// requests of the pages' scripts must not open the browser's login dialog
	if r.Header.Get("Sec-Fetch-Site") == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="Switch Library Manager", charset="UTF-8"`)
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// safeNext keeps redirects after login on this site: a path of it, without control characters
// or backslashes that browsers would turn into a link to another site.
func safeNext(next string) string {
	const home = "/index.html"
	for _, c := range next {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return home
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.HasPrefix(u.Path, "/login.html") {
		return home
	}
	return u.RequestURI()
}

const (
	// set to "true" when another service (a proxy with its own login, a VPN) protects the app:
	// without users it then also answers outside the local network
	REMOTE_WITHOUT_LOGIN_ENV  = "SLM_ALLOW_REMOTE_WITHOUT_LOGIN"
	remoteWithoutLoginMessage = "Login is disabled, so this app only answers on the local network. Create an administrator in Users from your local network, or set SLM_AUTH_USERNAME and SLM_AUTH_PASSWORD. If another service already protects the app, set " + REMOTE_WITHOUT_LOGIN_ENV + "=true."
)

// remoteHost is the address the request comes from.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isLocalAddress reports whether an address is of this computer or of a private network.
func isLocalAddress(address string) bool {
	ip := net.ParseIP(strings.Trim(strings.TrimSpace(address), "[]"))
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || sharedAddressSpace.Contains(ip))
}

// 100.64.0.0/10: carrier-grade NAT and VPNs such as Tailscale, never seen as a client address
// coming from the internet
var _, sharedAddressSpace, _ = net.ParseCIDR("100.64.0.0/10")

// forwardedAddresses are the client addresses that reverse proxies report.
func forwardedAddresses(r *http.Request) []string {
	addresses := []string{}
	for _, value := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(value, ",") {
			addresses = append(addresses, strings.TrimSpace(part))
		}
	}
	// X-Real-Ip from proxies such as nginx, the other two from Cloudflare and its tunnels
	for _, header := range []string{"X-Real-Ip", "Cf-Connecting-Ip", "True-Client-Ip"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			addresses = append(addresses, value)
		}
	}
	for _, value := range r.Header.Values("Forwarded") {
		for _, element := range strings.Split(value, ",") {
			for _, pair := range strings.Split(element, ";") {
				if key, value, ok := strings.Cut(strings.TrimSpace(pair), "="); ok && strings.EqualFold(key, "for") {
					value = strings.Trim(value, `"`)
					if host, _, err := net.SplitHostPort(value); err == nil {
						value = host
					}
					addresses = append(addresses, value)
				}
			}
		}
	}
	return addresses
}

// isLocalRequest reports whether a request comes from the local network: from a local address
// and, through a reverse proxy, without any address outside it among the forwarded ones (a
// client can add addresses, but not remove the one its proxy adds).
func isLocalRequest(r *http.Request) bool {
	if !isLocalAddress(remoteHost(r)) {
		return false
	}
	for _, address := range forwardedAddresses(r) {
		if address != "" && !isLocalAddress(address) {
			return false
		}
	}
	return true
}

// TRUSTED_PROXIES_ENV lists more proxies whose forwarded client addresses are believed, besides
// the ones of the local network: addresses or ranges, and "cloudflare" for its published ranges
// (when Cloudflare connects to the app directly instead of through a tunnel on this computer).
const TRUSTED_PROXIES_ENV = "SLM_TRUSTED_PROXIES"

// cloudflareRanges are the addresses Cloudflare connects from (https://www.cloudflare.com/ips/).
var cloudflareRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
	"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
	"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
	"2a06:98c0::/29", "2c0f:f248::/32",
}

// trustedProxyNets are the proxies set in SLM_TRUSTED_PROXIES.
var trustedProxyNets = parseTrustedProxies(os.Getenv(TRUSTED_PROXIES_ENV))

// parseTrustedProxies reads a list of addresses, ranges and "cloudflare", separated by commas
// or spaces; what cannot be read is left out.
func parseTrustedProxies(value string) []*net.IPNet {
	nets := []*net.IPNet{}
	add := func(cidr string) {
		if !strings.Contains(cidr, "/") {
			if ip := net.ParseIP(cidr); ip != nil {
				if ip.To4() != nil {
					cidr += "/32"
				} else {
					cidr += "/128"
				}
			}
		}
		if _, network, err := net.ParseCIDR(cidr); err == nil {
			nets = append(nets, network)
		}
	}
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if strings.EqualFold(item, "cloudflare") {
			for _, cidr := range cloudflareRanges {
				add(cidr)
			}
			continue
		}
		add(item)
	}
	return nets
}

// trustedProxy reports whether forwarded client addresses are believed from an address: one of
// the local network (a reverse proxy or a tunnel on it) or one set in SLM_TRUSTED_PROXIES.
func trustedProxy(address string) bool {
	ip := net.ParseIP(strings.Trim(strings.TrimSpace(address), "[]"))
	if ip == nil {
		return false
	}
	if isLocalAddress(address) {
		return true
	}
	for _, network := range trustedProxyNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIp is the address of the client, for the login limiter. Through a trusted proxy it is
// the address the proxy reports: Cloudflare's CF-Connecting-IP when there is one, else the last
// forwarded address that is not a proxy, so the clients do not all share the proxy's address.
// From any other address forwarded headers are ignored, so they cannot be forged from outside.
func clientIp(r *http.Request) string {
	host := remoteHost(r)
	if !trustedProxy(host) {
		return host
	}
	for _, header := range []string{"Cf-Connecting-Ip", "True-Client-Ip"} {
		if value := strings.Trim(strings.TrimSpace(r.Header.Get(header)), "[]"); net.ParseIP(value) != nil {
			return value
		}
	}
	addresses := forwardedAddresses(r)
	for i := len(addresses) - 1; i >= 0; i-- {
		address := strings.Trim(addresses[i], "[]")
		if address != "" && net.ParseIP(address) != nil && !trustedProxy(address) {
			return address
		}
	}
	return host
}

func (web *Web) authInfo(r *http.Request) AuthInfo {
	principal := principalFrom(r)
	enabled := web.auth != nil && web.auth.Enabled()
	weak := web.auth != nil && web.auth.envPasswordWeak != nil
	return AuthInfo{Enabled: enabled, User: principal.Name, IsAdmin: principal.IsAdmin(), FromEnv: principal.Source == "env", WeakEnvPassword: weak && principal.IsAdmin()}
}

// loginLimiter slows and blocks failed logins. It keys on both the client address and the
// account name, so one address trying many accounts and many addresses trying one account
// (a botnet with a list of users and passwords) are both stopped. After a few failures a small
// delay is added, barely felt by a person but costly for a bot; after maxLoginFailures the key
// is blocked, and each further failure doubles the block. A global cap slows every attempt
// while a flood from many addresses is going on.
type loginLimiter struct {
	mutex    sync.Mutex
	failures map[string]*loginFailures
	pruned   time.Time
	// failures from everywhere in the current minute
	globalCount  int
	globalMinute time.Time
}

type loginFailures struct {
	count      int
	last       time.Time
	blockUntil time.Time
}

// sleepFor applies the login delay; the tests replace it so they do not wait.
var sleepFor = time.Sleep

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{failures: map[string]*loginFailures{}}
}

// loginKeys are the limiter keys of an attempt: the client address, and the account when a
// name is given. A failed login counts against both, so a single address trying many accounts
// and many addresses trying one account are both stopped.
func loginKeys(ip, name string) []string {
	keys := []string{"ip:" + ip}
	if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
		keys = append(keys, "user:"+name)
	}
	return keys
}

// block is how long a key stays blocked after n failures: the window, doubled for each failure
// beyond maxLoginFailures, up to cap. An account is capped lower than an address, so flooding
// someone's user name cannot lock them out for as long as it blocks an attacking address.
func block(n int, cap time.Duration) time.Duration {
	wait := loginWindow
	for i := maxLoginFailures; i < n && wait < cap; i++ {
		wait *= 2
	}
	if wait > cap {
		wait = cap
	}
	return wait
}

// check returns how long the keys stay blocked (0 if none is) and the delay to add to this
// attempt. The longest of the keys wins, plus an extra delay while a global flood is going on.
func (l *loginLimiter) check(keys ...string) (retryAfter time.Duration, delay time.Duration) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	now := time.Now()
	for _, key := range keys {
		entry, ok := l.failures[key]
		if !ok {
			continue
		}
		if wait := entry.blockUntil.Sub(now); wait > retryAfter {
			retryAfter = wait
		}
		if entry.count >= tarpitAfter {
			if d := time.Duration(entry.count-tarpitAfter+1) * tarpitStep; d > delay {
				delay = d
			}
		}
	}
	if delay > maxTarpit {
		delay = maxTarpit
	}
	if l.globalMinute.Equal(now.Truncate(time.Minute)) && l.globalCount >= globalFailureCap {
		delay += globalFloodDelay
	}
	return retryAfter, delay
}

func (l *loginLimiter) fail(keys ...string) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	now := time.Now()
	minute := now.Truncate(time.Minute)
	if !l.globalMinute.Equal(minute) {
		l.globalMinute = minute
		l.globalCount = 0
	}
	l.globalCount++

	// forget old entries now and then, so the map does not grow forever
	if now.Sub(l.pruned) > time.Minute || len(l.failures) >= maxLimiterEntries {
		l.pruned = now
		for key, entry := range l.failures {
			if now.After(entry.blockUntil) && now.Sub(entry.last) > loginWindow {
				delete(l.failures, key)
			}
		}
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := l.failures[key]; !ok && len(l.failures) >= maxLimiterEntries {
			l.evictOldest()
		}
		entry, ok := l.failures[key]
		if !ok {
			entry = &loginFailures{}
			l.failures[key] = entry
		}
		// a fresh window after a long quiet time
		if now.After(entry.blockUntil) && now.Sub(entry.last) > loginWindow {
			entry.count = 0
		}
		entry.count++
		entry.last = now
		if entry.count >= maxLoginFailures {
			cap := maxLoginBlock
			if strings.HasPrefix(key, "user:") {
				cap = maxAccountBlock
			}
			entry.blockUntil = now.Add(block(entry.count, cap))
		}
	}
}

// evictOldest drops the entry that has been quiet the longest, to make room.
func (l *loginLimiter) evictOldest() {
	oldest := ""
	for key, entry := range l.failures {
		if oldest == "" || entry.last.Before(l.failures[oldest].last) {
			oldest = key
		}
	}
	delete(l.failures, oldest)
}

func (l *loginLimiter) succeed(keys ...string) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	for _, key := range keys {
		delete(l.failures, key)
	}
}

var errTooManyLogins = errors.New("Too many failed logins. Try again in %v minutes.")
