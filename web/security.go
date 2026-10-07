package web

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// sameOriginOnly rejects state changing requests sent by other web sites (CSRF): without it
// any page open in the browser could start a synchronization or reorganize the library.
// Requests without browser headers (curl, scripts) are allowed.
func sameOriginOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if !isSameOrigin(r) {
			http.Error(w, "cross-site request rejected", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isSameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}

	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			return false
		}
	}

	return true
}

// authFromEnv returns the credentials set with SLM_AUTH_USERNAME and SLM_AUTH_PASSWORD.
// Authentication is disabled when neither is set; setting only one is an error.
func authFromEnv() (username string, password string, enabled bool, err error) {
	username, password = os.Getenv("SLM_AUTH_USERNAME"), os.Getenv("SLM_AUTH_PASSWORD")
	if username == "" && password == "" {
		return "", "", false, nil
	}
	if username == "" || password == "" {
		return "", "", false, errors.New("both SLM_AUTH_USERNAME and SLM_AUTH_PASSWORD must be set to enable authentication")
	}
	return username, password, true, nil
}

// weakEnvPassword reports why the password of SLM_AUTH_PASSWORD is weak, or nil. A weak one
// still works, so nobody is locked out of the app after an update: the log and the pages of the
// administrators ask to change it.
func weakEnvPassword(username, password string) error {
	if username == "" || password == "" {
		return nil
	}
	return checkPassword(username, password)
}

// contentSecurityPolicy allows only the app's own scripts, styles and fonts. Images may
// also come from the Nintendo servers (covers and screenshots not cached yet).
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https:; font-src 'self'; connect-src 'self'; worker-src 'self'; manifest-src 'self'; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'"

// withSecurityHeaders adds headers that make the browser refuse foreign scripts, framing
// by other sites and content type guessing.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		headers.Set("Content-Security-Policy", contentSecurityPolicy)
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		headers.Set("Referrer-Policy", "same-origin")
		headers.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		headers.Set("Cross-Origin-Opener-Policy", "same-origin")
		headers.Set("Cross-Origin-Resource-Policy", "same-origin")
		// served over https (directly or through a proxy): browsers keep using https
		if isHttps(r) {
			headers.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

const (
	// the largest request body: forms and lists of files; a backup has its own limit
	maxRequestBody = 8 << 20
	maxBackupBody  = 17 << 20
	// the login form only has a name and a password
	maxLoginBody = 64 << 10
)

// withBodyLimit refuses request bodies larger than the forms need, so nobody can fill the
// memory or the disk of the server by sending endless data, even without logging in.
func withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Body != nil {
			limit := int64(maxRequestBody)
			switch r.URL.Path {
			case "/backup/restore":
				limit = maxBackupBody
			case "/login.html":
				limit = maxLoginBody
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
					http.Error(w, "unsupported form", http.StatusUnsupportedMediaType)
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// withHealthCheck answers /healthz without authentication, so container health checks
// work when a password is set. It reveals nothing but the fact that the app is running.
func withHealthCheck(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte("ok"))
	})
	mux.Handle("/", next)
	return mux
}
