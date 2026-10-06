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
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/gift"

	"github.com/dtrunk90/switch-library-manager-web/db"
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

// thumbnails returns the thumbnail maker shared by requests and the background work.
func (web *Web) thumbnails() *thumbnails {
	web.thumbsOnce.Do(func() {
		web.thumbs = newThumbnails()
	})
	return web.thumbs
}

// pregenerateThumbnails makes the missing thumbnails of the library covers, one at a time,
// so pages do not wait for them the first time they are shown.
func (web *Web) pregenerateThumbnails() {
	_, localDB := web.state.get()
	if localDB == nil {
		return
	}
	missing := []string{}
	seen := map[string]bool{}
	for _, title := range localDB.TitlesMap {
		if title.Icon == "" || seen[title.Icon] {
			continue
		}
		seen[title.Icon] = true
		if _, err := os.Stat(filepath.Join(web.dataFolder, "img", "thumbs", title.Icon)); err != nil {
			missing = append(missing, title.Icon)
		}
	}
	if len(missing) == 0 {
		return
	}
	taskId := web.taskLog().Start(TASK_THUMBNAILS, TRIGGER_SCAN)
	made := 0
	for i, name := range missing {
		// a new scan brings its own list
		if web.state.IsSynchronizing() {
			web.taskLog().Warn(taskId, NOTE_PAUSED_FOR_SCAN, "")
			break
		}
		web.taskLog().Progress(taskId, i, len(missing), "Making thumbnails")
		original, ok := web.imagePath(name)
		if !ok {
			continue
		}
		if web.thumbnails().make(original, filepath.Join(web.dataFolder, "img", "thumbs", name)) == nil {
			made++
		}
	}
	web.taskLog().SetResult(taskId, 0, made)
	web.taskLog().Finish(taskId, nil)
	web.sugarLogger.Infof("[%d thumbnails made in the background]", made)
}

// HandleImages serves the cached covers: /i/ the original files, /t/ thumbnails.
func (web *Web) HandleImages() {
	thumbs := web.thumbnails()

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
		if !ok && remoteCoverName.MatchString(name) {
			path, err := web.fetchRemoteCover(name)
			if err != nil {
				// the browser can still try the cover server itself
				http.Redirect(w, r, remoteCoverBase+name, http.StatusFound)
				return
			}
			original, ok = path, true
		}
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
		// linear resampling is several times faster than Lanczos on small NAS processors,
		// and looks the same at this size
		filter := gift.New(gift.ResizeToFit(thumbnailSize, thumbnailSize, gift.LinearResampling))
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
	// covers of games that are not in the library are fetched once by the server and
	// shown as small thumbnails, instead of every browser loading the full images
	if name, ok := strings.CutPrefix(url, remoteCoverBase); ok && remoteCoverName.MatchString(name) {
		return "/t/" + name
	}
	return url
}

// the server of the covers in the titles database; covers are cached under the same name
var remoteCoverBase = "https://img-eshop.cdn.nintendo.net/i/"

// only names of that server are fetched, so the app cannot be used to reach other sites
var remoteCoverName = regexp.MustCompile(`^[0-9a-f]{64}\.(jpg|png)$`)

// remoteCovers downloads covers asked for by pages, a few at a time.
type remoteCovers struct {
	slots   chan struct{}
	mutex   sync.Mutex
	running map[string]*sync.WaitGroup
}

func (web *Web) remoteCoverFetcher() *remoteCovers {
	web.remoteOnce.Do(func() {
		web.remote = &remoteCovers{slots: make(chan struct{}, 3), running: map[string]*sync.WaitGroup{}}
	})
	return web.remote
}

// fetchRemoteCover stores the cover name of the cover server in the image cache.
func (web *Web) fetchRemoteCover(name string) (string, error) {
	if !remoteCoverName.MatchString(name) {
		return "", errors.New("not a cover of the titles database")
	}
	path := filepath.Join(web.dataFolder, "img", name)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, nil
	}
	fetcher := web.remoteCoverFetcher()
	fetcher.mutex.Lock()
	if wait, ok := fetcher.running[name]; ok {
		fetcher.mutex.Unlock()
		wait.Wait()
		if _, err := os.Stat(path); err != nil {
			return "", err
		}
		return path, nil
	}
	wait := &sync.WaitGroup{}
	wait.Add(1)
	fetcher.running[name] = wait
	fetcher.mutex.Unlock()
	defer func() {
		fetcher.mutex.Lock()
		delete(fetcher.running, name)
		fetcher.mutex.Unlock()
		wait.Done()
	}()

	fetcher.slots <- struct{}{}
	defer func() { <-fetcher.slots }()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if err := db.DownloadFile(remoteCoverBase+name, path); err != nil {
		return "", err
	}
	return path, nil
}
