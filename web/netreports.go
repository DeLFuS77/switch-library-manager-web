package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/diskfree"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/gorilla/mux"
)

// The reports a console sends of what went wrong on it, for the administrators.
//
//   - NXDock sends a report as a ZIP with POST /install/report, with the key of the console, behind
//     the gate of the network install. The ZIP holds only files (and folders) with relative names
//     inside it, 200 at most, and is 16 MiB at most. It answers {"stored": name}.
//   - The reports are kept in the data folder, in consoles/reports/<id of the key>/, named by the
//     time they came (UTC); the newest 10 of each console stay.
//   - The administrators list them with GET /api/install/reports, download one with GET
//     /api/install/reports/{keyId}/{name} and delete it with DELETE on the same path; the page
//     Diagnostics lists them, with Download and Delete. Not in the demo.

const (
	netInstallReport = NET_INSTALL_PREFIX + "report"
	netReportsApi    = "/api/install/reports"
	// the folder of the reports in the one of the consoles, and the file that names the console
	// and its owner in the folder of each console
	netReportsFolder = "reports"
	netReportsOwner  = "console.json"
	// the biggest report, the most files in it, and the reports kept of each console
	netReportMaxSize    = 16 << 20
	netReportMaxEntries = 200
	netReportsKeep      = 10
	// what is left free on the disk after a report at least
	netReportMinFree = 64 << 20
	// the time a report came, in its name
	netReportTimeLayout = "20060102T150405.000"
)

var (
	netReportNamePattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{3}Z\.zip$`)

	errNetReportNotZip  = errors.New("a report is a ZIP of files with relative names inside it")
	errNetReportEntries = errors.New("a report holds 200 files at most")
	errNetReportTooBig  = errors.New("a report is 16 MiB at most")
	errNetReportSpace   = errors.New("not enough free space to keep the report")
	errNetReportAdmin   = errors.New("only an administrator reads the reports of the consoles")
)

// netReportStore keeps two reports from being written at once, and the time of the last one, so
// two reports in the same millisecond take two names.
type netReportStore struct {
	mutex sync.Mutex
	last  time.Time
}

// netReportOwner is the console a folder of reports is of, and its owner.
type netReportOwner struct {
	KeyId   string `json:"keyId"`
	Console string `json:"console"`
	Owner   string `json:"owner"`
}

// netReportRow is a report, for the administrators.
type netReportRow struct {
	KeyId   string    `json:"keyId"`
	Console string    `json:"console"`
	Owner   string    `json:"owner"`
	Name    string    `json:"name"`
	Date    time.Time `json:"date"`
	Size    int64     `json:"size"`
}

func (web *Web) netReportsRoot() string {
	return filepath.Join(web.dataFolder, netInventoryFolder, netReportsFolder)
}

// netReportPath is the path of a report, or "" when the id of the key or the name is not one of a
// report.
func (web *Web) netReportPath(keyId string, name string) string {
	if !netKeyIdPattern.MatchString(keyId) || !netReportNamePattern.MatchString(name) {
		return ""
	}
	return filepath.Join(web.netReportsRoot(), keyId, name)
}

// netReportEntryName tells whether a name in a ZIP is relative and stays inside it: no "..", no
// root, no drive, no back slash and no control character.
func netReportEntryName(name string) bool {
	if name == "" || len(name) > 255 || strings.ContainsAny(name, `\:`) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return fs.ValidPath(strings.TrimSuffix(name, "/"))
}

// checkNetReportZip tells whether data is a ZIP of plain files (and folders) with relative names,
// netReportMaxEntries at most.
func checkNetReportZip(data []byte) error {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) == 0 {
		return errNetReportNotZip
	}
	if len(archive.File) > netReportMaxEntries {
		return errNetReportEntries
	}
	for _, file := range archive.File {
		mode := file.Mode()
		if !netReportEntryName(file.Name) || (!mode.IsRegular() && !mode.IsDir()) {
			return errNetReportNotZip
		}
	}
	return nil
}

// netReportTime is when a report came, from its name.
func netReportTime(name string) time.Time {
	when, err := time.Parse(netReportTimeLayout, strings.TrimSuffix(name, "Z.zip"))
	if err != nil {
		return time.Time{}
	}
	return when.UTC()
}

// netReportNames are the reports of a console, oldest first.
func (web *Web) netReportNames(keyId string) []string {
	entries, err := os.ReadDir(filepath.Join(web.netReportsRoot(), keyId))
	if err != nil {
		return nil
	}
	names := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() && netReportNamePattern.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// handleNetReportUpload keeps a report sent by a console.
func (web *Web) handleNetReportUpload(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netConsoleKey(w, r)
	if !ok {
		return
	}
	if kind := r.Header.Get("Content-Type"); kind != "" {
		if media, _, err := mime.ParseMediaType(kind); err != nil || (media != "application/zip" && media != "application/octet-stream") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": errNetReportNotZip.Error()})
			return
		}
	}
	if r.ContentLength > netReportMaxSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": errNetReportTooBig.Error()})
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, netReportMaxSize))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": errNetReportTooBig.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the report was cut short"})
		return
	}
	if err := checkNetReportZip(data); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if free, err := diskfree.Free(web.dataFolder); err == nil && int64(free) < int64(len(data))+netReportMinFree {
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": errNetReportSpace.Error()})
		return
	}
	name, err := web.keepNetReport(key, data)
	if err != nil {
		web.sugarLogger.Warnf("Network install: the report of %s could not be kept: %v", key.KeyName, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the report could not be kept"})
		return
	}
	web.sugarLogger.Infof("Network install: the console %s (%s) sent a report (%d bytes)", key.KeyName, key.Name, len(data))
	if !isDemoMode() {
		web.activities().Add(activity.Entry{Time: time.Now(), User: key.Name, Action: activity.ACTION_NET_REPORT,
			Detail: quotedKeyName(key.KeyName) + ": " + name, IP: clientIp(r)})
	}
	writeJSON(w, http.StatusOK, map[string]string{"stored": name})
}

// keepNetReport writes a report of a console under a name of its own, notes the console and its
// owner, and deletes the oldest reports past netReportsKeep.
func (web *Web) keepNetReport(key *Principal, data []byte) (string, error) {
	web.netReports.mutex.Lock()
	defer web.netReports.mutex.Unlock()
	when := time.Now().UTC().Truncate(time.Millisecond)
	if !when.After(web.netReports.last) {
		when = web.netReports.last.Add(time.Millisecond)
	}
	web.netReports.last = when
	name := when.Format(netReportTimeLayout) + "Z.zip"
	// made here, as deleting the last report of a console deletes its folder
	folder := filepath.Join(web.netReportsRoot(), key.KeyID)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	if err := durable.WriteFile(filepath.Join(folder, name), data); err != nil {
		return "", err
	}
	owner, _ := json.Marshal(netReportOwner{KeyId: key.KeyID, Console: key.KeyName, Owner: key.Name})
	if err := durable.WriteFile(filepath.Join(folder, netReportsOwner), owner); err != nil {
		return "", err
	}
	names := web.netReportNames(key.KeyID)
	for len(names) > netReportsKeep {
		if names[0] != name {
			os.Remove(filepath.Join(folder, names[0]))
		}
		names = names[1:]
	}
	return name, nil
}

// netReportsAdmin answers a request about the reports that is not of an administrator, or made in
// the demo, and tells whether it is one.
func (web *Web) netReportsAdmin(w http.ResponseWriter, r *http.Request) bool {
	if isDemoMode() {
		http.NotFound(w, r)
		return false
	}
	if !principalFrom(r).IsAdmin() {
		writeGlobalError(w, http.StatusForbidden, web.requestLanguage(r), errNetReportAdmin.Error())
		return false
	}
	return true
}

// netReportOwnerOf is the console a folder of reports is of and its owner: as noted with the
// reports, else as its key tells now.
func (web *Web) netReportOwnerOf(keyId string, keys map[string]KeyRow) netReportOwner {
	owner := netReportOwner{KeyId: keyId}
	if data, err := os.ReadFile(filepath.Join(web.netReportsRoot(), keyId, netReportsOwner)); err == nil {
		json.Unmarshal(data, &owner)
		owner.KeyId = keyId
	}
	if key, ok := keys[keyId]; ok {
		owner.Console, owner.Owner = key.Name, key.User
	}
	return owner
}

// handleNetReportList lists the reports of every console, newest first, for the administrators.
func (web *Web) handleNetReportList(w http.ResponseWriter, r *http.Request) {
	if !web.netReportsAdmin(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"reports": web.netReportRows()})
}

// netReportRows are the reports of every console, newest first.
func (web *Web) netReportRows() []netReportRow {
	keys := map[string]KeyRow{}
	if web.auth != nil {
		for _, key := range web.auth.users.KeyRows("") {
			keys[key.ID] = key
		}
	}
	rows := []netReportRow{}
	folders, _ := os.ReadDir(web.netReportsRoot())
	for _, folder := range folders {
		if !folder.IsDir() || !netKeyIdPattern.MatchString(folder.Name()) {
			continue
		}
		owner := web.netReportOwnerOf(folder.Name(), keys)
		for _, name := range web.netReportNames(folder.Name()) {
			info, err := os.Stat(filepath.Join(web.netReportsRoot(), folder.Name(), name))
			if err != nil {
				continue
			}
			rows = append(rows, netReportRow{KeyId: folder.Name(), Console: owner.Console, Owner: owner.Owner, Name: name,
				Date: netReportTime(name), Size: info.Size()})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].Date.Equal(rows[j].Date) {
			return rows[i].Date.After(rows[j].Date)
		}
		return rows[i].KeyId < rows[j].KeyId
	})
	return rows
}

// handleNetReportDownload gives a report, for the administrators.
func (web *Web) handleNetReportDownload(w http.ResponseWriter, r *http.Request) {
	if !web.netReportsAdmin(w, r) {
		return
	}
	keyId, name := mux.Vars(r)["keyId"], mux.Vars(r)["name"]
	path := web.netReportPath(keyId, name)
	if path == "" {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	download := "NXDock report " + name
	if console := netSafeName(web.netReportOwnerOf(keyId, nil).Console); console != "" {
		download = "NXDock report " + console + " " + name
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(download))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", stat.ModTime(), file)
}

// handleNetReportDelete deletes a report, for the administrators; the folder of a console goes
// with its last report.
func (web *Web) handleNetReportDelete(w http.ResponseWriter, r *http.Request) {
	if !web.netReportsAdmin(w, r) {
		return
	}
	keyId, name := mux.Vars(r)["keyId"], mux.Vars(r)["name"]
	path := web.netReportPath(keyId, name)
	if path == "" {
		http.NotFound(w, r)
		return
	}
	web.netReports.mutex.Lock()
	defer web.netReports.mutex.Unlock()
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		writeGlobalError(w, http.StatusInternalServerError, web.requestLanguage(r), "Error!")
		return
	}
	if len(web.netReportNames(keyId)) == 0 {
		os.RemoveAll(filepath.Join(web.netReportsRoot(), keyId))
	}
	web.sugarLogger.Infof("Network install: %s deleted the report %s of the console %s", principalFrom(r).Name, name, keyId)
	writeJSON(w, http.StatusOK, map[string]string{"state": "deleted"})
}

// handleNetReportsPaths adds the paths of the reports of the consoles.
func (web *Web) handleNetReportsPaths() {
	web.router.HandleFunc(netInstallReport, web.handleNetReportUpload).Methods(http.MethodPost)
	web.router.HandleFunc(netReportsApi, web.handleNetReportList).Methods(http.MethodGet)
	report := netReportsApi + `/{keyId:[0-9a-f]{16}}/{name:[0-9]{8}T[0-9]{6}\.[0-9]{3}Z\.zip}`
	web.router.HandleFunc(report, web.handleNetReportDownload).Methods(http.MethodGet, http.MethodHead)
	web.router.HandleFunc(report, web.handleNetReportDelete).Methods(http.MethodDelete)
}
