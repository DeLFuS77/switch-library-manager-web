package web

import (
	"net"
	"net/http"
	"strings"

	"golang.org/x/net/netutil"
)

// maxConnections is how many connections the app keeps open at once: a flood of connections
// waits for room instead of using up the memory and the files the system lets the app open.
// Each open page holds one or two (the live updates of the tasks).
const maxConnections = 1024

// listenLimited listens on an address, with at most maxConnections connections at once.
func listenLimited(address string) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	return netutil.LimitListener(listener, maxConnections), nil
}

// heavyPrefixes are the requests that read the whole library or every file of it: the exports
// and the preview of Organize. A few at a time, so clicking again and again, or a script, does
// not leave a NAS without disk or processor for the rest.
var heavyPrefixes = []string{"/export/", "/organize/preview"}

// maxHeavyRequests is how many heavy requests run at the same time.
const maxHeavyRequests = 2

var heavySlots = make(chan struct{}, maxHeavyRequests)

// limitHeavy answers "busy, try again" to a heavy request while maxHeavyRequests run.
func limitHeavy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		heavy := false
		for _, prefix := range heavyPrefixes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				heavy = true
				break
			}
		}
		if !heavy {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case heavySlots <- struct{}{}:
			defer func() { <-heavySlots }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "5")
			http.Error(w, "the server is busy with the same kind of request, try again in a moment", http.StatusTooManyRequests)
		}
	})
}
