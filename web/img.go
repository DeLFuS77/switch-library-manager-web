package web

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/gift"
)

const (
	// longest side of the thumbnails shown on cards and lists
	thumbnailSize    = 360
	thumbnailQuality = 82
	// thumbnails made at the same time, so a page full of new covers does not take every CPU
	thumbnailWorkers = 2
	// image names are content hashes from the titles database: a name never changes content
	imageCacheControl = "public, max-age=31536000, immutable"
)

// thumbnails makes and caches small versions of the covers.
type thumbnails struct {
	slots   chan struct{}
	mutex   sync.Mutex
	running map[string]*sync.WaitGroup
}

func newThumbnails() *thumbnails {
	return &thumbnails{slots: make(chan struct{}, thumbnailWorkers), running: map[string]*sync.WaitGroup{}}
}

// HandleImages serves the cached covers: /i/ the original files, /t/ thumbnails.
func (web *Web) HandleImages() {
	thumbs := newThumbnails()

	http.HandleFunc("/i/", func(w http.ResponseWriter, r *http.Request) {
		path, ok := web.imagePath(strings.TrimPrefix(r.URL.Path, "/i/"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		serveImage(w, r, path)
	})

	http.HandleFunc("/t/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/t/")
		original, ok := web.imagePath(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		thumbnail := filepath.Join(web.dataFolder, "img", "thumbs", name)
		if err := thumbs.make(original, thumbnail); err != nil {
			// an image that cannot be decoded is served as it is
			web.sugarLogger.Debugf("No thumbnail for %s: %v", name, err)
			serveImage(w, r, original)
			return
		}
		serveImage(w, r, thumbnail)
	})
}

// imagePath returns the file of a cached image. Images are stored flat in the img folder,
// anything that could escape it is rejected.
func (web *Web) imagePath(name string) (string, bool) {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return "", false
	}
	path := filepath.Join(web.dataFolder, "img", name)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", false
	}
	return path, true
}

func serveImage(w http.ResponseWriter, r *http.Request, path string) {
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}

	head := make([]byte, 512)
	n, _ := file.Read(head)
	contentType := http.DetectContentType(head[:n])
	if !strings.HasPrefix(contentType, "image/") {
		http.Error(w, "not an image", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", imageCacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// ServeContent answers conditional and range requests from the modification time
	http.ServeContent(w, r, "", info.ModTime(), file)
}

// make writes the thumbnail of original unless it exists. Requests for the same image
// wait for one another instead of making it twice.
func (t *thumbnails) make(original string, thumbnail string) error {
	if info, err := os.Stat(thumbnail); err == nil && info.Size() > 0 {
		return nil
	}

	t.mutex.Lock()
	if wait, ok := t.running[thumbnail]; ok {
		t.mutex.Unlock()
		wait.Wait()
		if _, err := os.Stat(thumbnail); err != nil {
			return err
		}
		return nil
	}
	wait := &sync.WaitGroup{}
	wait.Add(1)
	t.running[thumbnail] = wait
	t.mutex.Unlock()

	defer func() {
		t.mutex.Lock()
		delete(t.running, thumbnail)
		t.mutex.Unlock()
		wait.Done()
	}()

	t.slots <- struct{}{}
	defer func() { <-t.slots }()
	return writeThumbnail(original, thumbnail)
}

func writeThumbnail(original string, thumbnail string) error {
	data, err := os.ReadFile(original)
	if err != nil {
		return err
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	bounds := source.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		return errors.New("empty image")
	}

	result := source
	if bounds.Dx() > thumbnailSize || bounds.Dy() > thumbnailSize {
		filter := gift.New(gift.ResizeToFit(thumbnailSize, thumbnailSize, gift.LanczosResampling))
		resized := image.NewRGBA(filter.Bounds(bounds))
		filter.Draw(resized, source)
		result = resized
	}

	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, result, &jpeg.Options{Quality: thumbnailQuality}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(thumbnail), 0755); err != nil {
		return err
	}
	// written aside first, so a half written thumbnail is never served
	tmp := thumbnail + ".tmp" + time.Now().Format("150405.000000000")
	if err := os.WriteFile(tmp, encoded.Bytes(), 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, thumbnail); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// thumbUrl turns the URL of a cached cover into the URL of its thumbnail; other URLs
// (covers on the Nintendo servers, the placeholder) are left as they are.
func thumbUrl(url string) string {
	if strings.HasPrefix(url, "/i/") {
		return "/t/" + strings.TrimPrefix(url, "/i/")
	}
	return url
}
