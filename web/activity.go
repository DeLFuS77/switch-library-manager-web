package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
		log.reload()
		web.activity = log
	})
	return web.activity
}

// reload reads the log again, also after a backup was restored.
func (l *activityLog) reload() {
	entries := []Activity{}
	if data, err := os.ReadFile(l.path); err == nil {
		json.Unmarshal(data, &entries)
	}
	l.mutex.Lock()
	l.entries = entries
	l.mutex.Unlock()
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
		writeFileAtomic(l.path, data)
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

var (
	activityFilesPattern = regexp.MustCompile(`^(\d+) files`)
	// values of the forms, shown with the name of what they do
	activityValues = map[string]string{
		ORGANIZE_ACTION_ORGANIZE: "Organize files",
		ORGANIZE_ACTION_CLEANUP:  "Delete old updates",
		SPACE_OLD_UPDATES:        "Old updates",
		SPACE_DUPLICATES:         "Duplicates",
		SPACE_COMPRESSED:         "Compressed originals",
		"admin":                  "Administrator",
		"viewer":                 "Read only",
	}
	activityPhrases = []string{", originals deleted after the check", ", NSZ deleted after the check"}
)

// translateActivity returns the activity with its details in a language. The details are
// recorded in English: counts of files, values of forms and names of users.
func translateActivity(lang string, entries []Activity) []Activity {
	result := make([]Activity, len(entries))
	for i, entry := range entries {
		detail := entry.Detail
		if label, ok := activityValues[detail]; ok {
			detail = translate(lang, label)
		}
		if match := activityFilesPattern.FindStringSubmatch(detail); match != nil {
			detail = translatef(lang, "%v files", match[1]) + detail[len(match[0]):]
		}
		parts := strings.Split(detail, " + ")
		for j, part := range parts {
			if label, ok := activityValues[part]; ok {
				parts[j] = translate(lang, label)
			}
		}
		detail = strings.Join(parts, " + ")
		for _, phrase := range activityPhrases {
			detail = strings.Replace(detail, phrase, ", "+translate(lang, strings.TrimPrefix(phrase, ", ")), 1)
		}
		if name, role, found := strings.Cut(detail, ": "); found {
			if label, ok := activityValues[role]; ok {
				detail = name + ": " + translate(lang, label)
			}
		}
		entry.Detail = detail
		result[i] = entry
	}
	return result
}

func containsError(location string) bool {
	return strings.Contains(location, "?error=") || strings.Contains(location, "&error=")
}
