package web

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPagesAreCompressed(t *testing.T) {
	page := strings.Repeat("<p>library</p>", 500)
	handler := withCompression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, page)
		case "/image":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write([]byte("jpeg data"))
		}
	}))

	request := httptest.NewRequest(http.MethodGet, "/page", nil)
	request.Header.Set("Accept-Encoding", "gzip, deflate")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get("Content-Encoding") != "gzip" || recorder.Body.Len() >= len(page)/4 {
		t.Fatalf("page not compressed: %q, %d bytes", recorder.Header().Get("Content-Encoding"), recorder.Body.Len())
	}
	reader, err := gzip.NewReader(recorder.Body)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := io.ReadAll(reader); string(body) != page {
		t.Fatal("the page must be the same after decompression")
	}

	request = httptest.NewRequest(http.MethodGet, "/image", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get("Content-Encoding") != "" || recorder.Body.String() != "jpeg data" {
		t.Fatal("images are sent unchanged")
	}

	request = httptest.NewRequest(http.MethodGet, "/page", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get("Content-Encoding") != "" || recorder.Body.String() != page {
		t.Fatal("browsers that do not accept gzip get the page unchanged")
	}
}
