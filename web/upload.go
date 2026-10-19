package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Games are sent from the browser to the library folder in pieces: a file of several GB
// survives a cut connection, and goes on where it stopped, also after the page is opened
// again (the same name and size continue the same upload). The pieces are written to a
// hidden file next to the games, which the scans leave alone, and that takes the name of the
// game only once it is whole. Only administrators upload; never in the demo.

const (
	// the biggest piece accepted in one request
	uploadChunkMax = 64 << 20
	// an upload nobody sent anything to for this long is given up, and its file removed
	uploadIdle   = 24 * time.Hour
	uploadPrefix = ".slm-upload-"
	uploadSuffix = ".part"
	// the uploads count as files being written for this long after their last piece
	uploadWriting = time.Minute
)

// the extensions of the games that can be uploaded
var uploadExtensions = map[string]bool{".nsp": true, ".nsz": true, ".xci": true, ".xcz": true}

const (
	UPLOAD_BAD_NAME = "Only NSP, NSZ, XCI and XCZ files can be uploaded."
	UPLOAD_EXISTS   = "A file with this name is already in the library folder."
	UPLOAD_SPACE    = "Not enough free space in the library folder for this file."
	UPLOAD_NO_DIR   = "The library folder is not set or cannot be found. Set it in Settings."
	UPLOAD_UNKNOWN  = "This upload is no longer known: send the file again."
	UPLOAD_FAILED   = "The file could not be written in the library folder."
	UPLOAD_PARTIAL  = "The file is not whole yet."
)

// gameUpload is a file being sent.
type gameUpload struct {
	mutex    sync.Mutex
	Id       string
	Name     string
	Size     int64
	Received int64
	part     string
	final    string
	touched  time.Time
	release  func()
}

type uploadStore struct {
	mutex     sync.Mutex
	items     map[string]*gameUpload
	lastWrite atomic.Int64
}

// writing tells whether a game is being uploaded: the folders change all the time meanwhile.
func (s *uploadStore) writing() bool {
	last := s.lastWrite.Load()
	return last != 0 && time.Since(time.Unix(0, last)) < uploadWriting
}

func (s *uploadStore) get(id string) *gameUpload {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.items[id]
}

func (s *uploadStore) remove(upload *gameUpload) {
	s.mutex.Lock()
	delete(s.items, upload.Id)
	s.mutex.Unlock()
	if upload.release != nil {
		upload.release()
		upload.release = nil
	}
}

// uploadName is the name a file is saved under, or "" when it cannot be uploaded.
func uploadName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	if name == "" || name == "." || strings.HasPrefix(name, ".") || len(name) > 255 || !uploadExtensions[strings.ToLower(filepath.Ext(name))] {
		return ""
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	// a name alone, never a way out of the library folder
	if !filepath.IsLocal(name) || strings.ContainsAny(name, `/\`) {
		return ""
	}
	return name
}

// uploadFolder is the folder the games are uploaded to: the library folder of the settings.
func (web *Web) uploadFolder() string {
	folder := settings.ReadSettings(web.dataFolder).Folder
	if folder == "" {
		return ""
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return ""
	}
	return folder
}

// forgetIdleUploads gives up the uploads nobody sent anything to for a day, and removes the
// files of uploads cut before the app was started.
func (web *Web) forgetIdleUploads(folder string) {
	store := &web.uploads
	store.mutex.Lock()
	known := map[string]bool{}
	for id, upload := range store.items {
		upload.mutex.Lock()
		idle := time.Since(upload.touched) >= uploadIdle
		upload.mutex.Unlock()
		if idle {
			delete(store.items, id)
			os.Remove(upload.part)
			if upload.release != nil {
				upload.release()
			}
			continue
		}
		known[upload.part] = true
	}
	store.mutex.Unlock()
	entries, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, uploadPrefix) || !strings.HasSuffix(name, uploadSuffix) {
			continue
		}
		path := filepath.Join(folder, name)
		if info, err := entry.Info(); err == nil && !known[path] && time.Since(info.ModTime()) >= uploadIdle {
			os.Remove(path)
		}
	}
}

func uploadId() string {
	buffer := make([]byte, 12)
	rand.Read(buffer)
	return hex.EncodeToString(buffer)
}

// uploadStatus is what the browser is told of an upload.
func uploadStatus(upload *gameUpload) map[string]any {
	return map[string]any{"id": upload.Id, "name": upload.Name, "size": upload.Size, "received": upload.Received}
}

func (web *Web) HandleUploads() {
	// start an upload, or go on with the one of the same file
	web.router.HandleFunc("/uploads/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		name := uploadName(r.FormValue("name"))
		size, err := strconv.ParseInt(r.FormValue("size"), 10, 64)
		if name == "" || err != nil || size <= 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, UPLOAD_BAD_NAME)
			return
		}
		folder := web.uploadFolder()
		if folder == "" {
			writeGlobalError(w, http.StatusConflict, lang, UPLOAD_NO_DIR)
			return
		}
		web.forgetIdleUploads(folder)
		final := filepath.Join(folder, name)
		if filepath.Dir(final) != filepath.Clean(folder) {
			writeGlobalError(w, http.StatusBadRequest, lang, UPLOAD_BAD_NAME)
			return
		}

		store := &web.uploads
		store.mutex.Lock()
		defer store.mutex.Unlock()
		if store.items == nil {
			store.items = map[string]*gameUpload{}
		}
		for _, upload := range store.items {
			if upload.final == final && upload.Size == size {
				upload.mutex.Lock()
				upload.touched = time.Now()
				status := uploadStatus(upload)
				upload.mutex.Unlock()
				writeJSON(w, http.StatusOK, status)
				return
			}
		}
		if _, err := os.Lstat(final); err == nil {
			writeGlobalError(w, http.StatusConflict, lang, UPLOAD_EXISTS)
			return
		}
		release, err := web.reserveSpace(folder, size)
		if err != nil {
			writeGlobalError(w, http.StatusInsufficientStorage, lang, UPLOAD_SPACE)
			return
		}
		id := uploadId()
		part := filepath.Join(folder, uploadPrefix+id+uploadSuffix)
		file, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			release()
			web.sugarLogger.Errorf("[Upload] %s could not be created: %v", part, err)
			writeGlobalError(w, http.StatusInternalServerError, lang, UPLOAD_FAILED)
			return
		}
		file.Close()
		upload := &gameUpload{Id: id, Name: name, Size: size, part: part, final: final, touched: time.Now(), release: release}
		store.items[id] = upload
		web.sugarLogger.Infof("[Upload] %s started (%d bytes)", name, size)
		writeJSON(w, http.StatusOK, uploadStatus(upload))
	}).Methods("POST")

	// where an upload is, to go on after a cut connection
	web.router.HandleFunc("/uploads/status", func(w http.ResponseWriter, r *http.Request) {
		upload := web.uploads.get(r.FormValue("id"))
		if upload == nil {
			writeGlobalError(w, http.StatusNotFound, web.requestLanguage(r), UPLOAD_UNKNOWN)
			return
		}
		upload.mutex.Lock()
		defer upload.mutex.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, uploadStatus(upload))
	}).Methods("GET")

	// a piece of the file, sent from where the server is
	web.router.HandleFunc("/uploads/chunk", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		upload := web.uploads.get(r.FormValue("id"))
		if upload == nil {
			writeGlobalError(w, http.StatusNotFound, lang, UPLOAD_UNKNOWN)
			return
		}
		upload.mutex.Lock()
		defer upload.mutex.Unlock()
		offset, err := strconv.ParseInt(r.FormValue("offset"), 10, 64)
		if err != nil || offset != upload.Received {
			// the browser goes on from where the server is
			writeJSON(w, http.StatusConflict, uploadStatus(upload))
			return
		}
		file, err := os.OpenFile(upload.part, os.O_WRONLY, 0)
		if err != nil {
			writeGlobalError(w, http.StatusNotFound, lang, UPLOAD_UNKNOWN)
			return
		}
		defer file.Close()
		if _, err := file.Seek(upload.Received, io.SeekStart); err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, UPLOAD_FAILED)
			return
		}
		left := upload.Size - upload.Received
		body := http.MaxBytesReader(w, r.Body, min(uploadChunkMax, left))
		written, err := io.Copy(file, body)
		upload.Received += written
		upload.touched = time.Now()
		web.uploads.lastWrite.Store(time.Now().UnixNano())
		// what was written of a piece cut short is kept: the next one starts after it
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				file.Truncate(upload.Received)
				writeJSON(w, http.StatusRequestEntityTooLarge, uploadStatus(upload))
				return
			}
			writeJSON(w, http.StatusConflict, uploadStatus(upload))
			return
		}
		writeJSON(w, http.StatusOK, uploadStatus(upload))
	}).Methods("PUT")

	// the whole file takes its name, and the library is scanned
	web.router.HandleFunc("/uploads/finish", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		upload := web.uploads.get(r.FormValue("id"))
		if upload == nil {
			writeGlobalError(w, http.StatusNotFound, lang, UPLOAD_UNKNOWN)
			return
		}
		upload.mutex.Lock()
		if upload.Received != upload.Size {
			upload.mutex.Unlock()
			writeGlobalError(w, http.StatusConflict, lang, UPLOAD_PARTIAL)
			return
		}
		if _, err := os.Lstat(upload.final); err == nil {
			upload.mutex.Unlock()
			writeGlobalError(w, http.StatusConflict, lang, UPLOAD_EXISTS)
			return
		}
		err := os.Rename(upload.part, upload.final)
		upload.mutex.Unlock()
		if err != nil {
			web.sugarLogger.Errorf("[Upload] %s could not take its name: %v", upload.final, err)
			writeGlobalError(w, http.StatusInternalServerError, lang, UPLOAD_FAILED)
			return
		}
		// the store is always locked before an upload, never while one is
		web.uploads.remove(upload)
		web.sugarLogger.Infof("[Upload] %s saved in %s", upload.Name, filepath.Dir(upload.final))
		web.invalidateDerived()
		web.Rescan(TRIGGER_UPLOAD)
		writeJSON(w, http.StatusOK, map[string]any{"name": upload.Name, "path": upload.final})
	}).Methods("POST")

	// stop an upload and remove what was sent
	web.router.HandleFunc("/uploads/cancel", func(w http.ResponseWriter, r *http.Request) {
		upload := web.uploads.get(r.FormValue("id"))
		if upload == nil {
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		upload.mutex.Lock()
		os.Remove(upload.part)
		upload.mutex.Unlock()
		web.uploads.remove(upload)
		web.sugarLogger.Infof("[Upload] %s cancelled", upload.Name)
		writeJSON(w, http.StatusOK, map[string]any{})
	}).Methods("POST")
}
