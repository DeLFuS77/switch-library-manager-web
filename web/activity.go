package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The activity log records who changed what: deleted, organized or compressed files,
// restored backups, saved settings and managed users. Administrators see it on the Users
// page. Only actions that succeeded are recorded.

const (
	ACTIVITY_FILENAME = "activity.json"
	maxActivities     = 500
)

// actions, translated by the interface
const (
	ACTION_SETTINGS   = "Saved the settings"
	ACTION_ORGANIZE   = "Organized the files"
	ACTION_SPACE      = "Deleted files on the Space page"
	ACTION_COMPRESS   = "Started a compression"
	ACTION_DECOMPRESS = "Started a decompression"
	ACTION_RESTORE    = "Restored a backup"
	ACTION_USER_NEW   = "Created a user"
	ACTION_USER_ROLE  = "Changed the role of a user"
	ACTION_USER_PASS  = "Changed the password of a user"
	ACTION_USER_DEL   = "Deleted a user"
)

var activityActions = []string{ACTION_SETTINGS, ACTION_ORGANIZE, ACTION_SPACE, ACTION_COMPRESS, ACTION_DECOMPRESS, ACTION_RESTORE, ACTION_USER_NEW, ACTION_USER_ROLE, ACTION_USER_PASS, ACTION_USER_DEL}

// Activity is an action of a user.
type Activity struct {
	Time   time.Time `json:"time"`
	User   string    `json:"user,omitempty"`
	Action string    `json:"action"`
	Detail string    `json:"detail,omitempty"`
}

type activityLog struct {
	mutex   sync.Mutex
	path    string
	entries []Activity
}

func (web *Web) activities() *activityLog {
	web.activityOnce.Do(func() {
		log := &activityLog{path: filepath.Join(web.dataFolder, ACTIVITY_FILENAME)}
		if data, err := os.ReadFile(log.path); err == nil {
			json.Unmarshal(data, &log.entries)
		}
		web.activity = log
	})
	return web.activity
}

// add records an action, newest first.
func (l *activityLog) add(entry Activity) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.entries = append([]Activity{entry}, l.entries...)
	if len(l.entries) > maxActivities {
		l.entries = l.entries[:maxActivities]
	}
	if data, err := json.MarshalIndent(l.entries, "", " "); err == nil {
		os.WriteFile(l.path, data, 0644)
	}
}

func (l *activityLog) list() []Activity {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return append([]Activity(nil), l.entries...)
}

// recordedActions are the requests that change files, users or settings, and how to
// describe them.
var recordedActions = map[string]func(r *http.Request) (string, string){
	"/settings.html": func(r *http.Request) (string, string) { return ACTION_SETTINGS, "" },
	"/organize/run":  func(r *http.Request) (string, string) { return ACTION_ORGANIZE, r.FormValue("action") },
	"/space/clean": func(r *http.Request) (string, string) {
		detail := strconv.Itoa(len(r.Form["path"])) + " files"
		for _, group := range r.Form["rest"] {
			detail += " + " + group
		}
		return ACTION_SPACE, detail
	},
	"/compress/start": func(r *http.Request) (string, string) {
		detail := strconv.Itoa(len(r.Form["path"])) + " files"
		if r.FormValue("delete_originals") == "true" {
			detail += ", originals deleted after the check"
		}
		return ACTION_COMPRESS, detail
	},
	"/decompress/start": func(r *http.Request) (string, string) {
		detail := strconv.Itoa(len(r.Form["path"])) + " files"
		if r.FormValue("delete_compressed") == "true" {
			detail += ", NSZ deleted after the check"
		}
		return ACTION_DECOMPRESS, detail
	},
	"/backup/restore": func(r *http.Request) (string, string) { return ACTION_RESTORE, "" },
	"/users/create":   func(r *http.Request) (string, string) { return ACTION_USER_NEW, r.FormValue("name") },
	"/users/role": func(r *http.Request) (string, string) {
		return ACTION_USER_ROLE, r.FormValue("name") + ": " + r.FormValue("role")
	},
	"/users/password": func(r *http.Request) (string, string) { return ACTION_USER_PASS, r.FormValue("name") },
	"/users/delete":   func(r *http.Request) (string, string) { return ACTION_USER_DEL, r.FormValue("name") },
}

// statusRecorder remembers the status of a response.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Flush() {
	if flusher, ok := s.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// withActivityLog records the actions of recordedActions that succeeded.
func (web *Web) withActivityLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		describe, ok := recordedActions[r.URL.Path]
		if !ok || r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		// the form is read first: the handler gets a copy of the request and reads the body
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			r.ParseForm()
		}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		// redirects with an error in the query are failures too
		if recorder.status >= 400 || (recorder.status == http.StatusSeeOther && containsError(w.Header().Get("Location"))) {
			return
		}
		action, detail := describe(r)
		user := ""
		if principal := principalFrom(r); principal != nil {
			user = principal.Name
		}
		web.activities().add(Activity{Time: time.Now(), User: user, Action: action, Detail: detail})
	})
}

func containsError(location string) bool {
	return strings.Contains(location, "?error=") || strings.Contains(location, "&error=")
}
