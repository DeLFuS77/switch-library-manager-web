package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// While the heavy requests run, another one is told to try again; the light ones go on.
func TestHeavyRequestsAreLimited(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, maxHeavyRequests)
	handler := limitHeavy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.html" {
			started <- struct{}{}
			<-release
		}
	}))
	for i := 0; i < maxHeavyRequests; i++ {
		go handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/export/library.csv", nil))
		<-started
	}
	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, httptest.NewRequest("POST", "/organize/preview", nil))
	if busy.Code != http.StatusTooManyRequests {
		t.Fatalf("heavy request while full: %d", busy.Code)
	}
	light := httptest.NewRecorder()
	handler.ServeHTTP(light, httptest.NewRequest("GET", "/index.html", nil))
	if light.Code != http.StatusOK {
		t.Fatalf("light request: %d", light.Code)
	}
	close(release)
}
