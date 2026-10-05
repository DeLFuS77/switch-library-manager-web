package web

import (
	"crypto/sha256"
	"crypto/subtle"
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

// basicAuth protects every request with HTTP basic authentication.
func basicAuth(username string, password string, next http.Handler) http.Handler {
	expectedUser := sha256.Sum256([]byte(username))
	expectedPassword := sha256.Sum256([]byte(password))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		// compare hashes in constant time, so timing does not reveal the credentials
		givenUser := sha256.Sum256([]byte(user))
		givenPassword := sha256.Sum256([]byte(pass))
		userMatch := subtle.ConstantTimeCompare(givenUser[:], expectedUser[:]) == 1
		passwordMatch := subtle.ConstantTimeCompare(givenPassword[:], expectedPassword[:]) == 1

		if !ok || !userMatch || !passwordMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="Switch Library Manager", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
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
