package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/diskfree"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
)

// The games a console dumps, uploaded by NXDock to the library: an NSP or an XCI of several GB
// over the Wi-Fi of the console, which is cut now and then, so it goes on where it stopped.
//
//   - GET /install/upload/{name} answers {"have": n}: the bytes the app has of that file, 0 when
//     it has none.
//   - PUT /install/upload/{name}?offset=n sends the file from byte n on, the end of what the app
//     has: another offset answers 409 with {"have": n}, where to go on. Any length is taken, the
//     whole file in one request or in pieces.
//   - The last request adds &done=1 (with the last piece, or with an empty body): the file takes
//     its name, " (2)" added when one is already there, and the library is scanned.
//
// What is received is written to "<name>.part" in the folder of the dumps, which the scans leave
// alone; the folder is the one of the settings, or "NXDock dumps" in the first folder of the
// library. Only for the key of a console, behind the gate of the network install, and only while
// the uploads are allowed in Settings.

const (
	netInstallUpload = NET_INSTALL_PREFIX + "upload/"
	// the folder of the dumps in the folder of the library, when the settings name none
	netDumpsFolderName = "NXDock dumps"
	netDumpPartSuffix  = ".part"
	// what is left free on the disk after a dump at least
	netDumpMinFree = 256 << 20
)

var netDumpUploadPattern = regexp.MustCompile(`^` + netInstallUpload + `[^/]+$`)

var (
	errNetDumpOff    = errors.New("the consoles may not upload what they dump: it is turned off in Settings")
	errNetDumpName   = errors.New("the name of an NSP, NSZ, XCI or XCZ file, without folders")
	errNetDumpFolder = errors.New("the library has no folder to upload to")
	errNetDumpSpace  = errors.New("not enough free space in the folder of the dumps")
	errNetDumpBusy   = errors.New("this file is already being uploaded")
	errNetDumpEmpty  = errors.New("nothing was uploaded of this file")
)

// netDumpUploadPath tells the path of a dump being uploaded, which a console may send.
func netDumpUploadPath(path string) bool {
	return netDumpUploadPattern.MatchString(path)
}

// netDumpUploads are the names being written, so two requests never write the same file at once.
type netDumpUploads struct {
	mutex sync.Mutex
	busy  map[string]bool
}

func (u *netDumpUploads) take(path string) bool {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.busy == nil {
		u.busy = map[string]bool{}
	}
	if u.busy[path] {
		return false
	}
	u.busy[path] = true
	return true
}

func (u *netDumpUploads) give(path string) {
	u.mutex.Lock()
	delete(u.busy, path)
	u.mutex.Unlock()
}

// netDumpRescan scans the library after a dump came; a variable, so the tests count it.
var netDumpRescan = func(web *Web) {
	web.invalidateDerived()
	web.Rescan(tasklog.TRIGGER_UPLOAD)
}

// netDumpName is the name a dump is kept under, or "" when it is not the name of a file an
// installer takes.
func netDumpName(name string) string {
	if strings.ContainsAny(name, `/\`) {
		return ""
	}
	name = uploadName(name)
	if !netInstallExtensions[strings.ToLower(filepath.Ext(name))] {
		return ""
	}
	return name
}

// netDumpsFolder is the folder the dumps go to, "" when the library has no folder.
func (web *Web) netDumpsFolder(options settings.NetInstallOptions) string {
	if folder := strings.TrimSpace(options.UploadFolder); folder != "" {
		return filepath.Clean(folder)
	}
	folders := web.uploadFolders()
	if len(folders) == 0 {
		return ""
	}
	return filepath.Join(folders[0], netDumpsFolderName)
}

// netDumpFreeName is the name a whole dump takes: its own, or with " (2)", " (3)"… when a file
// already has it.
func netDumpFreeName(folder string, name string) (string, error) {
	extension := filepath.Ext(name)
	base := strings.TrimSuffix(name, extension)
	for i := 1; i < 1000; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", base, i, extension)
		}
		if _, err := os.Lstat(filepath.Join(folder, candidate)); os.IsNotExist(err) {
			return filepath.Join(folder, candidate), nil
		}
	}
	return "", errors.New("too many files of that name")
}

// netDumpWriter tells the watch of the folders that a dump is being written, so it does not scan
// the library over and over meanwhile.
type netDumpWriter struct {
	web  *Web
	file *os.File
}

func (w netDumpWriter) Write(data []byte) (int, error) {
	w.web.uploads.lastWrite.Store(time.Now().UnixNano())
	return w.file.Write(data)
}

// netDumpHave is the size of what was received of a dump, 0 when nothing was.
func netDumpHave(part string) int64 {
	if stat, err := os.Stat(part); err == nil && !stat.IsDir() {
		return stat.Size()
	}
	return 0
}

// netDumpTarget is the folder of the dumps and the name of a request, or an answer that refuses it.
func (web *Web) netDumpTarget(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	options, ok := web.netInstallGate(w, r)
	if !ok {
		return "", "", false
	}
	if _, ok := netConsoleKey(w, r); !ok {
		return "", "", false
	}
	if !options.UploadsAllowed() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": errNetDumpOff.Error()})
		return "", "", false
	}
	name := netDumpName(mux.Vars(r)["name"])
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errNetDumpName.Error()})
		return "", "", false
	}
	folder := web.netDumpsFolder(options)
	if folder == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": errNetDumpFolder.Error()})
		return "", "", false
	}
	return folder, name, true
}

// handleNetDumpHave tells how much of a dump the app has.
func (web *Web) handleNetDumpHave(w http.ResponseWriter, r *http.Request) {
	folder, name, ok := web.netDumpTarget(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]int64{"have": netDumpHave(filepath.Join(folder, name+netDumpPartSuffix))})
}

// handleNetDumpUpload writes a piece of a dump at the end of what the app has, and with done=1
// gives the whole file its name and scans the library.
func (web *Web) handleNetDumpUpload(w http.ResponseWriter, r *http.Request) {
	folder, name, ok := web.netDumpTarget(w, r)
	if !ok {
		return
	}
	key := principalFrom(r)
	offset := int64(0)
	if value := r.URL.Query().Get("offset"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the offset is the number of bytes sent before"})
			return
		}
		offset = parsed
	}
	done := r.URL.Query().Get("done") == "1"
	if err := os.MkdirAll(folder, 0o755); err != nil {
		web.sugarLogger.Warnf("NXDock dumps: %s cannot be made: %v", folder, err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": errNetDumpFolder.Error()})
		return
	}
	part := filepath.Join(folder, name+netDumpPartSuffix)
	if !web.netDumps.take(part) {
		writeJSON(w, http.StatusConflict, map[string]any{"have": netDumpHave(part), "error": errNetDumpBusy.Error()})
		return
	}
	defer web.netDumps.give(part)
	have := netDumpHave(part)
	if offset != have {
		// the console goes on from where the app is
		writeJSON(w, http.StatusConflict, map[string]int64{"have": have})
		return
	}
	// the room of what it says it sends, or a body of unknown length stopped where the disk would
	// be nearly full
	body := io.Reader(r.Body)
	if free, err := diskfree.Free(folder); err == nil {
		if r.ContentLength > 0 && int64(free) < r.ContentLength+netDumpMinFree {
			writeJSON(w, http.StatusInsufficientStorage, map[string]any{"have": have, "free": free, "error": errNetDumpSpace.Error()})
			return
		}
		if r.ContentLength < 0 {
			body = http.MaxBytesReader(w, r.Body, max(int64(free)-netDumpMinFree, 0))
		}
	}
	if r.ContentLength != 0 {
		// several GB over the Wi-Fi of the console take long
		controller := http.NewResponseController(w)
		controller.SetReadDeadline(time.Time{})
		controller.SetWriteDeadline(time.Time{})
		file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			web.sugarLogger.Warnf("NXDock dumps: %s cannot be written: %v", part, err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"have": have, "error": "the file cannot be written"})
			return
		}
		if _, err = file.Seek(have, io.SeekStart); err == nil {
			var written int64
			written, err = io.Copy(netDumpWriter{web: web, file: file}, body)
			have += written
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			// what came is kept: the next request goes on after it
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				writeJSON(w, http.StatusInsufficientStorage, map[string]any{"have": have, "error": errNetDumpSpace.Error()})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]any{"have": have, "error": "the upload was cut short"})
			return
		}
	}
	if !done {
		writeJSON(w, http.StatusOK, map[string]int64{"have": have})
		return
	}
	if have == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"have": have, "error": errNetDumpEmpty.Error()})
		return
	}
	final, err := netDumpFreeName(folder, name)
	if err == nil {
		err = durable.CommitFile(part, final)
	}
	if err != nil {
		web.sugarLogger.Warnf("NXDock dumps: %s could not take its name: %v", part, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"have": have, "error": "the file could not take its name"})
		return
	}
	web.sugarLogger.Infof("NXDock dumps: the console %s (%s) uploaded %s (%d bytes) to %s", key.KeyName, key.Name, filepath.Base(final), have, folder)
	netDumpRescan(web)
	writeJSON(w, http.StatusOK, map[string]any{"have": have, "done": true, "name": filepath.Base(final)})
}

// handleNetDumps adds the paths of the uploads of the consoles.
func (web *Web) handleNetDumps() {
	web.router.HandleFunc(netInstallUpload+"{name}", web.handleNetDumpHave).Methods(http.MethodGet)
	web.router.HandleFunc(netInstallUpload+"{name}", web.handleNetDumpUpload).Methods(http.MethodPut)
}
