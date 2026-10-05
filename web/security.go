package web

import (
	"errors"
	"net/http"
	"net/url"
	"os"
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
