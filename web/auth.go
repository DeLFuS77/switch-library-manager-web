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

	// failed logins allowed per address within the window, before it is blocked for the window
	maxLoginFailures = 10
	loginWindow      = 15 * time.Minute

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
	return &Auth{users: users, envUser: envUser, envPassword: envPassword, secret: secret, limiter: newLoginLimiter()}, nil
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
	return path == "/login.html" || path == "/logout" ||
		strings.HasPrefix(path, "/resources/static/") || strings.HasPrefix(path, "/resources/vendor/")
}

// pages and actions of administrators; read-only users can only look and download
var adminOnlyPages = map[string]struct{}{"/settings.html": {}, "/organize.html": {}, "/users.html": {}, "/compress.html": {}, "/backup/download": {}}

func viewerAllowed(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		_, adminOnly := adminOnlyPages[r.URL.Path]
		return !adminOnly
	case http.MethodPost:
		return r.URL.Path == "/account/password" || r.URL.Path == "/logout"
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
			principal = &Principal{Role: ROLE_ADMIN}
		default:
			principal = a.fromSession(r)
			if principal == nil {
				ip := clientIp(r)
				if !a.limiter.allowed(ip) {
					http.Error(w, "too many failed logins, try again later", http.StatusTooManyRequests)
					return
				}
				var attempted bool
				principal, attempted = a.fromBasicAuth(r)
				if principal == nil {
					if attempted {
						a.limiter.fail(ip)
					}
					a.unauthorized(w, r)
					return
				}
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

// safeNext keeps redirects after login on this site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || strings.HasPrefix(next, "/login.html") {
		return "/index.html"
	}
	return next
}

func clientIp(r *http.Request) string {
	// the remote address only: forwarded headers can be forged
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (web *Web) authInfo(r *http.Request) AuthInfo {
	principal := principalFrom(r)
	enabled := web.auth != nil && web.auth.Enabled()
	return AuthInfo{Enabled: enabled, User: principal.Name, IsAdmin: principal.IsAdmin(), FromEnv: principal.Source == "env"}
}

// loginLimiter blocks addresses with too many failed logins.
type loginLimiter struct {
	mutex    sync.Mutex
	failures map[string]*loginFailures
}

type loginFailures struct {
	count int
	first time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{failures: map[string]*loginFailures{}}
}

func (l *loginLimiter) allowed(ip string) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	entry, ok := l.failures[ip]
	if !ok {
		return true
	}
	if time.Since(entry.first) > loginWindow {
		delete(l.failures, ip)
		return true
	}
	return entry.count < maxLoginFailures
}

// retryAfter is how long an address stays blocked.
func (l *loginLimiter) retryAfter(ip string) time.Duration {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if entry, ok := l.failures[ip]; ok {
		if wait := loginWindow - time.Since(entry.first); wait > 0 {
			return wait
		}
	}
	return 0
}

func (l *loginLimiter) fail(ip string) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	now := time.Now()
	// forget old entries, so the map does not grow forever
	for key, entry := range l.failures {
		if now.Sub(entry.first) > loginWindow {
			delete(l.failures, key)
		}
	}
	entry, ok := l.failures[ip]
	if !ok {
		entry = &loginFailures{first: now}
		l.failures[ip] = entry
	}
	entry.count++
}

func (l *loginLimiter) succeed(ip string) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	delete(l.failures, ip)
}

var errTooManyLogins = errors.New("Too many failed logins. Try again in %v minutes.")
