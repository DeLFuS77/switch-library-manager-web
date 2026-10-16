package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// While the app starts, pages say so in the language of the browser with what it is doing,
// the health check is not ready, and the script of the page is served.
func TestStartingScreen(t *testing.T) {
	setStartingStep("Preparing the pages...")
	handler := withSecurityHeaders(startingHandler([]byte("<svg/>")))
	get := func(path string, accept string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Accept", accept)
		request.Header.Set("Accept-Language", "es-ES,es;q=0.9,en;q=0.8")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	page := get("/index.html", "text/html,application/xhtml+xml")
	if page.Code != http.StatusServiceUnavailable || !strings.Contains(page.Body.String(), "Iniciando la aplicación") || !strings.Contains(page.Body.String(), "Preparando las páginas") || !strings.Contains(page.Body.String(), `src="/starting.js"`) {
		t.Errorf("page: %d %s", page.Code, page.Body.String())
	}
	if health := get("/healthz", "*/*"); health.Code != http.StatusServiceUnavailable || health.Body.String() != "Preparando las páginas…" {
		t.Errorf("health: %d %q", health.Code, health.Body.String())
	}
	if detail := get("/healthz?detail=1", "*/*"); detail.Code != http.StatusServiceUnavailable || !strings.Contains(detail.Body.String(), `"step":3`) {
		t.Errorf("detail: %d %s", detail.Code, detail.Body.String())
	}
	if icon := get("/resources/static/icon.svg", "image/*"); icon.Code != http.StatusOK {
		t.Errorf("icon: %d", icon.Code)
	}
	if script := get("/starting.js", "*/*"); script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "/healthz") {
		t.Errorf("script: %d", script.Code)
	}
	if api := get("/api/titles", "application/json"); api.Code != http.StatusServiceUnavailable {
		t.Errorf("api: %d", api.Code)
	}
}
