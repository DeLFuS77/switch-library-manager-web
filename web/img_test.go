package web

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func writeTestCover(t *testing.T, folder string, name string, size int) {
	t.Helper()
	cover := image.NewRGBA(image.Rect(0, 0, size, size))
	for x := 0; x < size; x++ {
		cover.Set(x, x, color.RGBA{R: 200, A: 255})
	}
	var data bytes.Buffer
	if err := jpeg.Encode(&data, cover, nil); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(folder, "img"), 0755)
	if err := os.WriteFile(filepath.Join(folder, "img", name), data.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func imageServer(t *testing.T) (*Web, http.Handler) {
	t.Helper()
	web := newTestWeb(t)
	// the image handlers are registered on the default mux in production
	mux := http.NewServeMux()
	previous := http.DefaultServeMux
	http.DefaultServeMux = mux
	web.HandleImages()
	http.DefaultServeMux = previous
	return web, mux
}

func TestImagesAndThumbnails(t *testing.T) {
	web, handler := imageServer(t)
	writeTestCover(t, web.dataFolder, "abc.jpg", 1024)

	get := func(path string, headers ...string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", path, nil)
		for i := 0; i+1 < len(headers); i += 2 {
			request.Header.Set(headers[i], headers[i+1])
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	original := get("/i/abc.jpg")
	if original.Code != http.StatusOK || original.Header().Get("Content-Type") != "image/jpeg" || original.Header().Get("Cache-Control") != imageCacheControl {
		t.Fatalf("original: %v %v", original.Code, original.Header())
	}

	thumb := get("/t/abc.jpg")
	if thumb.Code != http.StatusOK || thumb.Body.Len() >= original.Body.Len() {
		t.Fatalf("thumbnail: %v, %v bytes (original %v)", thumb.Code, thumb.Body.Len(), original.Body.Len())
	}
	decoded, _, err := image.Decode(bytes.NewReader(thumb.Body.Bytes()))
	if err != nil || decoded.Bounds().Dx() != thumbnailSize {
		t.Fatalf("thumbnail size: %v %v", err, decoded.Bounds())
	}
	if _, err := os.Stat(filepath.Join(web.dataFolder, "img", "thumbs", "abc.jpg")); err != nil {
		t.Fatal("the thumbnail must be cached")
	}

	if cached := get("/t/abc.jpg", "If-Modified-Since", thumb.Header().Get("Last-Modified")); cached.Code != http.StatusNotModified {
		t.Fatalf("revalidation: %v", cached.Code)
	}

	for _, path := range []string{"/i/missing.jpg", "/i/..%2Fsettings.json", "/t/.failed.json", "/i/"} {
		if code := get(path).Code; code != http.StatusNotFound {
			t.Errorf("%s: %v", path, code)
		}
	}
}

func TestThumbnailOfAnUndecodableImageServesTheOriginal(t *testing.T) {
	web, handler := imageServer(t)
	os.MkdirAll(filepath.Join(web.dataFolder, "img"), 0755)
	// a GIF header without data: recognized as an image, but it cannot be decoded
	os.WriteFile(filepath.Join(web.dataFolder, "img", "broken.gif"), []byte("GIF89a broken"), 0644)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/t/broken.gif", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "GIF89a broken" {
		t.Fatalf("got %v %q", recorder.Code, recorder.Body.String())
	}
}

func TestThumbnailsAreMadeOnce(t *testing.T) {
	web, handler := imageServer(t)
	writeTestCover(t, web.dataFolder, "same.jpg", 800)

	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/t/same.jpg", nil))
			if recorder.Code != http.StatusOK {
				t.Errorf("concurrent request: %v", recorder.Code)
			}
		}()
	}
	wait.Wait()
	entries, _ := os.ReadDir(filepath.Join(web.dataFolder, "img", "thumbs"))
	if len(entries) != 1 {
		t.Fatalf("expected one thumbnail and no temporary files, got %v", len(entries))
	}
}

func TestThumbUrl(t *testing.T) {
	for url, want := range map[string]string{
		"/i/abc.jpg":                         "/t/abc.jpg",
		"https://img-eshop.cdn.nintendo.net": "https://img-eshop.cdn.nintendo.net",
		"/resources/static/noimage.png":      "/resources/static/noimage.png",
	} {
		if got := thumbUrl(url); got != want {
			t.Errorf("thumbUrl(%q) = %q", url, got)
		}
	}
}
