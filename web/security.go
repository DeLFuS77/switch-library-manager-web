package web

import (
	"net/http"
	"net/url"
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
