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

	"github.com/dtrunk90/switch-library-manager-web/process"
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
	// an upload nothing came to for this long ends its task: the page was closed, or lost the
	// connection. Its file is kept, and the same file chosen again goes on (see uploadIdle). The
	// room kept on the disk for it is given back meanwhile, so it does not hold up a compression
	uploadStalled = 3 * time.Minute
	// how often the uploads given up and their files are looked for, besides at the start
	uploadSweepEvery = time.Hour
)

const TASK_UPLOAD = "upload"

// notes of the task of an upload, translated by the interface
const (
	NOTE_UPLOAD_CANCELED = "The upload was cancelled."
	NOTE_UPLOAD_STOPPED  = "The upload stopped: the page was closed or lost the connection. Choose the same file again to go on where it stopped."
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
	// the task that shows it in Tasks, 0 while nothing comes
	taskId int64
}

type uploadStore struct {
	mutex     sync.Mutex
	items     map[string]*gameUpload
	lastWrite atomic.Int64
	watching  sync.Once
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
	// the room is given back once, also while the watch gives it back after a pause
	upload.mutex.Lock()
	defer upload.mutex.Unlock()
	if upload.release != nil {
		upload.release()
		upload.release = nil
	}
}

// uploadNameBytes is the longest name a game is saved under, as the disk keeps it: a name too
// long is shortened as Organize does (see process.FitFileName), or a disk that keeps names
// decomposed (ZFS on Unraid) refuses it
const uploadNameBytes = 240

// uploadName is the name a file is saved under, or "" when it cannot be uploaded.
func uploadName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	// a game whose name starts with a dot is saved without it, as Organize names it: with it, the
	// file would be hidden on Linux and on many NAS
	name = strings.TrimSpace(strings.TrimLeft(name, "."))
	if name == "" || !uploadExtensions[strings.ToLower(filepath.Ext(name))] {
		return ""
	}
	name = process.FitFileName(name, uploadNameBytes)
	if process.NameBytes(name) > uploadNameBytes {
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

// uploadFolders are the folders games can be uploaded to: those of the library that exist, the
// main one first.
func (web *Web) uploadFolders() []string {
	folders := []string{}
	for _, folder := range scanFolders(settings.ReadSettings(web.dataFolder)) {
		if info, err := os.Stat(folder); err == nil && info.IsDir() {
			folders = append(folders, folder)
		}
	}
	return folders
}

// uploadFolder is the folder asked for, when it is one of the library, else the main one; ""
// when there is none.
func (web *Web) uploadFolder(asked string) string {
	folders := web.uploadFolders()
	for _, folder := range folders {
		if asked != "" && folder == asked {
			return folder
		}
	}
	if len(folders) == 0 {
		return ""
	}
	return folders[0]
}

// uploadTask starts the task that shows an upload in Tasks, from where it is.
func (web *Web) uploadTask(upload *gameUpload) {
	if upload.taskId != 0 {
		return
	}
	upload.taskId = web.taskLog().Start(TASK_UPLOAD, TRIGGER_MANUAL)
	web.taskLog().SetSubject(upload.taskId, upload.Name)
	web.taskLog().Progress(upload.taskId, int(upload.Received>>20), int(upload.Size>>20), upload.Name)
}

// endUploadTask ends the task of an upload, with what happened.
func (web *Web) endUploadTask(upload *gameUpload, failure *TaskNote) {
	if upload.taskId == 0 {
		return
	}
	if failure == nil {
		web.taskLog().Progress(upload.taskId, int(upload.Size>>20), int(upload.Size>>20), upload.Name)
		web.taskLog().SetCompressResult(upload.taskId, 1, upload.Size)
	}
	web.taskLog().Finish(upload.taskId, failure)
	upload.taskId = 0
}

// watchUploads ends the tasks of the uploads nothing comes to any more and gives back the room
// they kept; at the start and every uploadSweepEvery it also gives up the ones idle for a day
// and removes the files of uploads cut before the start, also when no other upload starts.
func (web *Web) watchUploads() {
	store := &web.uploads
	store.watching.Do(func() {
		go func() {
			web.forgetIdleUploadsEverywhere()
			swept := time.Now()
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				web.endStalledUploads()
				if time.Since(swept) >= uploadSweepEvery {
					web.forgetIdleUploadsEverywhere()
					swept = time.Now()
				}
			}
		}()
	})
}

// forgetIdleUploadsEverywhere gives up the idle uploads, and removes the files of the uploads
// cut before the start in every folder games are uploaded to.
func (web *Web) forgetIdleUploadsEverywhere() {
	web.dropIdleUploads()
	for _, folder := range web.uploadFolders() {
		web.removeCutUploads(folder)
	}
}

func (web *Web) endStalledUploads() {
	store := &web.uploads
	store.mutex.Lock()
	uploads := make([]*gameUpload, 0, len(store.items))
	for _, upload := range store.items {
		uploads = append(uploads, upload)
	}
	store.mutex.Unlock()
	for _, upload := range uploads {
		upload.mutex.Lock()
		if time.Since(upload.touched) >= uploadStalled {
			if upload.taskId != 0 {
				web.endUploadTask(upload, &TaskNote{Text: NOTE_UPLOAD_STOPPED})
			}
			// the room kept for the rest of the file is given back; the upload asks for it again
			// when it goes on
			if upload.release != nil {
				upload.release()
				upload.release = nil
			}
		}
		upload.mutex.Unlock()
	}
}

// keepRoom keeps again the room an upload needs for the rest of its file, after it was given
// back while nothing came. The upload is locked.
func (web *Web) keepRoom(upload *gameUpload) error {
	if upload.release != nil {
		return nil
	}
	release, err := web.reserveSpace(filepath.Dir(upload.part), upload.Size-upload.Received)
	if err != nil {
		return err
	}
	upload.release = release
	return nil
}

// forgetIdleUploads gives up the uploads nobody sent anything to for a day, and removes the
// files of uploads cut before the app was started.
func (web *Web) forgetIdleUploads(folder string) {
	web.dropIdleUploads()
	web.removeCutUploads(folder)
}

// dropIdleUploads gives up the uploads nobody sent anything to for a day, with their files.
func (web *Web) dropIdleUploads() {
	store := &web.uploads
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for id, upload := range store.items {
		upload.mutex.Lock()
		if time.Since(upload.touched) >= uploadIdle {
			web.endUploadTask(upload, &TaskNote{Text: NOTE_UPLOAD_STOPPED})
			delete(store.items, id)
			os.Remove(upload.part)
			if upload.release != nil {
				upload.release()
				upload.release = nil
			}
		}
		upload.mutex.Unlock()
	}
}

// removeCutUploads removes the files of uploads of a folder this app does not know, cut before
// it was started, once they are a day old.
func (web *Web) removeCutUploads(folder string) {
	store := &web.uploads
	store.mutex.Lock()
	known := map[string]bool{}
	for _, upload := range store.items {
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
	web.watchUploads()
	// start an upload, or go on with the one of the same file
	web.router.HandleFunc("/uploads/start", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		name := uploadName(r.FormValue("name"))
		size, err := strconv.ParseInt(r.FormValue("size"), 10, 64)
		if name == "" || err != nil || size <= 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, UPLOAD_BAD_NAME)
			return
		}
		folder := web.uploadFolder(r.FormValue("folder"))
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
				if err := web.keepRoom(upload); err != nil {
					upload.mutex.Unlock()
					writeGlobalError(w, http.StatusInsufficientStorage, lang, UPLOAD_SPACE)
					return
				}
				upload.touched = time.Now()
				// gone on after it stopped: in Tasks again
				web.uploadTask(upload)
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
		web.uploadTask(upload)
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
		// pieces again after a pause: the room given back meanwhile is kept again
		if err := web.keepRoom(upload); err != nil {
			writeGlobalError(w, http.StatusInsufficientStorage, lang, UPLOAD_SPACE)
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
		web.uploadTask(upload)
		web.taskLog().Progress(upload.taskId, int(upload.Received>>20), int(upload.Size>>20), upload.Name)
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
		err := commitFile(upload.part, upload.final)
		if err != nil {
			// the task ends with the failure instead of staying at 100%, and the file sent is
			// removed: sending it again starts over
			web.sugarLogger.Errorf("[Upload] %s could not take its name: %v", upload.final, err)
			os.Remove(upload.part)
			web.endUploadTask(upload, &TaskNote{Text: UPLOAD_FAILED})
			upload.mutex.Unlock()
			web.uploads.remove(upload)
			writeGlobalError(w, http.StatusInternalServerError, lang, UPLOAD_FAILED)
			return
		}
		web.endUploadTask(upload, nil)
		upload.mutex.Unlock()
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
		web.endUploadTask(upload, &TaskNote{Text: NOTE_UPLOAD_CANCELED})
		upload.mutex.Unlock()
		web.uploads.remove(upload)
		web.sugarLogger.Infof("[Upload] %s cancelled", upload.Name)
		writeJSON(w, http.StatusOK, map[string]any{})
	}).Methods("POST")
}
