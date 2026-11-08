package web

import (
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// loginResults are the actions of a sign-in attempt, by result.
var loginResults = map[string]string{"ok": activity.ACTION_LOGIN_OK, "fail": activity.ACTION_LOGIN_FAIL, "blocked": activity.ACTION_LOGIN_LOCK}

// recordLogin notes a sign-in attempt: who, from where, and how it went. The password tried is
// never kept; only whether it had the length a real one needs, to tell a typo from a probe.
func (web *Web) recordLogin(user, ip, result string, longEnough bool) {
	if isDemoMode() {
		return
	}
	action, ok := loginResults[result]
	if !ok {
		return
	}
	detail := ""
	if result != "ok" {
		if longEnough {
			detail = "password of the usual length"
		} else {
			detail = "password too short to be real"
		}
	}
	web.activities().Add(activity.Entry{Time: time.Now(), User: user, Action: action, Detail: detail, IP: ip})
}

func (web *Web) activities() *activity.Log {
	web.activityOnce.Do(func() {
		web.activity = activity.Load(filepath.Join(web.dataFolder, activity.ACTIVITY_FILENAME))
	})
	return web.activity
}

// recordedActions are the requests that change files, users or settings, and how to
// describe them.
var recordedActions = map[string]func(r *http.Request) (string, string){
	"/settings.html": func(r *http.Request) (string, string) { return activity.ACTION_SETTINGS, "" },
	"/organize/run":  func(r *http.Request) (string, string) { return activity.ACTION_ORGANIZE, r.FormValue("action") },
	"/space/clean": func(r *http.Request) (string, string) {
		detail := strconv.Itoa(len(r.Form["path"])) + " files"
		for _, group := range r.Form["rest"] {
			detail += " + " + group
		}
		return activity.ACTION_SPACE, detail
	},
	"/compress/start": func(r *http.Request) (string, string) {
		detail := strconv.Itoa(len(r.Form["path"])) + " files"
		if r.FormValue("delete_originals") == "true" {
			detail += ", originals deleted after the check"
		}
		return activity.ACTION_COMPRESS, detail
	},
	"/decompress/start": func(r *http.Request) (string, string) {
		detail := strconv.Itoa(len(r.Form["path"])) + " files"
		if r.FormValue("delete_compressed") == "true" {
			detail += ", NSZ deleted after the check"
		}
		return activity.ACTION_DECOMPRESS, detail
	},
	"/backup/restore": func(r *http.Request) (string, string) { return activity.ACTION_RESTORE, "" },
	"/users/create":   func(r *http.Request) (string, string) { return activity.ACTION_USER_NEW, r.FormValue("name") },
	"/users/role": func(r *http.Request) (string, string) {
		return activity.ACTION_USER_ROLE, r.FormValue("name") + ": " + r.FormValue("role")
	},
	"/users/password":  func(r *http.Request) (string, string) { return activity.ACTION_USER_PASS, r.FormValue("name") },
	"/users/delete":    func(r *http.Request) (string, string) { return activity.ACTION_USER_DEL, r.FormValue("name") },
	"/users/email":     func(r *http.Request) (string, string) { return activity.ACTION_USER_EMAIL, r.FormValue("name") },
	"/users/unlink":    func(r *http.Request) (string, string) { return activity.ACTION_OIDC_UNLINK, r.FormValue("name") },
	"/settings/portal": func(r *http.Request) (string, string) { return activity.ACTION_PORTAL_SETTINGS, "" },
	"/uploads/finish":  func(r *http.Request) (string, string) { return activity.ACTION_UPLOAD, r.FormValue("name") },
	"/sync":            func(r *http.Request) (string, string) { return activity.ACTION_SYNC, "" },
	"/verify/start":    func(r *http.Request) (string, string) { return activity.ACTION_VERIFY, verifyActivity(r) },
}

// verifyActivity tells the level of a check, and whether it reads every file again.
func verifyActivity(r *http.Request) string {
	level, _ := verifyLevel(r.FormValue("level"))
	if r.FormValue("scope") == "signatures" {
		level = tasklog.VERIFY_LEVEL_SIGNATURE
	}
	if r.FormValue("all") == "true" {
		return level + " + " + VERIFY_ACTIVITY_ALL
	}
	return level
}

// VERIFY_ACTIVITY_ALL is a check of every file again, in the activity.
const VERIFY_ACTIVITY_ALL = "every file again"

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
		web.activities().Add(activity.Entry{Time: time.Now(), User: user, Action: action, Detail: detail, IP: clientIp(r)})
	})
}

var (
	activityFilesPattern = regexp.MustCompile(`^(\d+) files`)
	// values of the forms, shown with the name of what they do
	activityValues = map[string]string{
		ORGANIZE_ACTION_ORGANIZE:        "Organize files",
		ORGANIZE_ACTION_CLEANUP:         "Delete old updates",
		SPACE_OLD_UPDATES:               "Old updates",
		SPACE_DUPLICATES:                "Duplicates",
		SPACE_COMPRESSED:                "Compressed originals",
		tasklog.VERIFY_LEVEL_SIGNATURE:  "Signature",
		tasklog.VERIFY_LEVEL_HASH:       "Hash",
		tasklog.VERIFY_LEVEL_BOTH:       "Both",
		VERIFY_ACTIVITY_ALL:             "Check everything again",
		"admin":                         "Administrator",
		"viewer":                        "Player",
		"guest":                         "Guest",
		"editor":                        "Editor",
		"password of the usual length":  "password of the usual length",
		"password too short to be real": "password too short to be real",
		"read only":                     "Read only",
		apiKeyFailDetail:                "unknown API key",
		apiKeyBlockedDetail:             "API key",
		portalUnknownDetail:             portalUnknownDetail,
		portalUntrustedDetail:           portalUntrustedDetail,
		portalTokenDetail:               portalTokenDetail,
		oidcFailedDetail:                oidcFailedDetail,
		recoveryActivityDetail:          recoveryActivityDetail,
	}
	activityPhrases = []string{", originals deleted after the check", ", NSZ deleted after the check"}
)

// translateActivity returns the activity with its details in a language. The details are
// recorded in English: counts of files, values of forms and names of users.
func translateActivity(lang string, entries []activity.Entry) []activity.Entry {
	result := make([]activity.Entry, len(entries))
	for i, entry := range entries {
		detail := entry.Detail
		if label, ok := activityValues[detail]; ok {
			detail = i18n.Translate(lang, label)
		}
		if match := activityFilesPattern.FindStringSubmatch(detail); match != nil {
			detail = i18n.Translatef(lang, "%v files", match[1]) + detail[len(match[0]):]
		}
		parts := strings.Split(detail, " + ")
		for j, part := range parts {
			if label, ok := activityValues[part]; ok {
				parts[j] = i18n.Translate(lang, label)
			}
		}
		detail = strings.Join(parts, " + ")
		for _, phrase := range activityPhrases {
			detail = strings.Replace(detail, phrase, ", "+i18n.Translate(lang, strings.TrimPrefix(phrase, ", ")), 1)
		}
		// the role after the last colon: the name of an API key before it may have one too
		if i := strings.LastIndex(detail, ": "); i >= 0 {
			if label, ok := activityValues[detail[i+2:]]; ok {
				detail = detail[:i] + ": " + i18n.Translate(lang, label)
			}
		}
		// a key that does not exist names no user
		if entry.User == "" && (entry.Detail == apiKeyFailDetail || entry.Detail == apiKeyBlockedDetail) {
			entry.User = i18n.Translate(lang, "API key")
		}
		// a token that is not valid names nobody
		if entry.User == "" && entry.Detail == portalTokenDetail {
			entry.User = "Cloudflare Access"
		}
		if entry.User == "" && entry.Detail == oidcFailedDetail {
			entry.User = "OpenID Connect"
		}
		entry.Detail = detail
		result[i] = entry
	}
	return result
}

func containsError(location string) bool {
	return strings.Contains(location, "?error=") || strings.Contains(location, "&error=")
}
