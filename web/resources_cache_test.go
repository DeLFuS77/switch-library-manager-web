package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// Browsers keep the files of the build: the styles and scripts of this version for good, the
// rest asked again with its tag and answered "not modified", without the file.
func TestStaticFilesAreKept(t *testing.T) {
	files := cached(http.FileServer(http.FS(fstest.MapFS{
		"web.css":  {Data: []byte("body{}")},
		"icon.svg": {Data: []byte("<svg/>")},
	})))
	get := func(path string, tag string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", path, nil)
		if tag != "" {
			request.Header.Set("If-None-Match", tag)
		}
		response := httptest.NewRecorder()
		files.ServeHTTP(response, request)
		return response
	}

	versioned := get("/web.css?v="+assetVersion, "")
	if versioned.Code != http.StatusOK || !strings.Contains(versioned.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("the styles of this version: %d %q", versioned.Code, versioned.Header().Get("Cache-Control"))
	}
	// an address of another version is not kept for good
	if other := get("/web.css?v=old", ""); strings.Contains(other.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("another version: %q", other.Header().Get("Cache-Control"))
	}
	icon := get("/icon.svg", "")
	tag := icon.Header().Get("ETag")
	if icon.Code != http.StatusOK || tag == "" || !strings.Contains(icon.Header().Get("Cache-Control"), "max-age=86400") {
		t.Fatalf("an icon: %d %q %q", icon.Code, icon.Header().Get("Cache-Control"), tag)
	}
	if again := get("/icon.svg", tag); again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("asked again with its tag: %d, %d bytes", again.Code, again.Body.Len())
	}
	if stale := get("/icon.svg", `"another build"`); stale.Code != http.StatusOK {
		t.Fatalf("the tag of another build: %d", stale.Code)
	}
}
