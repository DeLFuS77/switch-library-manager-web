// Package activity is the activity log: who changed what, deleted, organized or compressed
// files, restored backups, saved settings and managed users, and who signed in. Administrators
// see it on the Users page. Only actions that succeeded are recorded.
package activity

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
)

const (
	ACTIVITY_FILENAME = "activity.json"
	maxActivities     = 500
	// the failed and blocked sign-ins kept at most: a flood of them from many addresses cannot
	// push the other actions out of the log
	MaxFailedSignIns = 100
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
	ACTION_UPLOAD     = "Uploaded a game"
	ACTION_SYNC       = "Updated the titles database"
	ACTION_VERIFY     = "Started a file check"
	ACTION_LOGIN_OK   = "Signed in"
	ACTION_LOGIN_FAIL = "Failed sign-in"
	ACTION_LOGIN_LOCK = "Sign-in blocked (too many tries)"
	ACTION_KEY_NEW    = "Created an API key"
	ACTION_KEY_USE    = "Signed in with an API key"
	ACTION_KEY_DEL    = "Revoked an API key"
	// signed in by a portal in front of the app (see proxyauth.go)
	ACTION_PORTAL_PROXY = "Signed in through the proxy"
	ACTION_PORTAL_CF    = "Signed in with Cloudflare Access"
	// signed in with OpenID Connect (see oidc.go), and a user unlinked from the account of the
	// provider
	ACTION_PORTAL_OIDC = "Signed in with OpenID Connect"
	ACTION_OIDC_UNLINK = "Unlinked a user from OpenID Connect"
	// the email a portal names a user with, and the options of the portal
	ACTION_USER_EMAIL      = "Changed the email of a user"
	ACTION_PORTAL_SETTINGS = "Changed the sign-in portal"
	// the temporary files of a task cut halfway, removed (see leftovers.go)
	ACTION_LEFTOVERS = "Removed temporary files left halfway"
)

var Actions = []string{ACTION_SETTINGS, ACTION_ORGANIZE, ACTION_SPACE, ACTION_COMPRESS, ACTION_DECOMPRESS, ACTION_RESTORE, ACTION_USER_NEW, ACTION_USER_ROLE, ACTION_USER_PASS, ACTION_USER_DEL, ACTION_LEFTOVERS, ACTION_UPLOAD, ACTION_SYNC, ACTION_VERIFY, ACTION_LOGIN_OK, ACTION_LOGIN_FAIL, ACTION_LOGIN_LOCK,
	ACTION_KEY_NEW, ACTION_KEY_USE, ACTION_KEY_DEL, ACTION_PORTAL_PROXY, ACTION_PORTAL_CF, ACTION_USER_EMAIL, ACTION_PORTAL_SETTINGS,
	ACTION_PORTAL_OIDC, ACTION_OIDC_UNLINK}

// Entry is an action of a user.
type Entry struct {
	Time   time.Time `json:"time"`
	User   string    `json:"user,omitempty"`
	Action string    `json:"action"`
	Detail string    `json:"detail,omitempty"`
	// the address the action came from, when it is known
	IP string `json:"ip,omitempty"`
}

type Log struct {
	mutex   sync.Mutex
	path    string
	entries []Entry
}

// Load reads the log saved at path.
func Load(path string) *Log {
	log := &Log{path: path}
	log.Reload()
	return log
}

// Reload reads the log again, also after a backup was restored.
func (l *Log) Reload() {
	entries := []Entry{}
	if data, ok := durable.ReadStore(l.path); ok {
		durable.ParseStore(l.path, data, &entries)
	}
	l.mutex.Lock()
	l.entries = entries
	l.mutex.Unlock()
}

// Add records an action, newest first.
func (l *Log) Add(entry Entry) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.entries = append([]Entry{entry}, l.entries...)
	if failedSignIn(entry) {
		// only the newest failed sign-ins stay
		kept, failures := l.entries[:0], 0
		for _, each := range l.entries {
			if failedSignIn(each) {
				if failures++; failures > MaxFailedSignIns {
					continue
				}
			}
			kept = append(kept, each)
		}
		l.entries = kept
	}
	if len(l.entries) > maxActivities {
		l.entries = l.entries[:maxActivities]
	}
	if data, err := json.MarshalIndent(l.entries, "", " "); err == nil {
		durable.WriteFile(l.path, data)
	}
}

func failedSignIn(entry Entry) bool {
	return entry.Action == ACTION_LOGIN_FAIL || entry.Action == ACTION_LOGIN_LOCK
}

// Preview is the newest entries, at most n.
func Preview(entries []Entry, n int) []Entry {
	if len(entries) > n {
		return entries[:n]
	}
	return entries
}

func (l *Log) List() []Entry {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return append([]Entry(nil), l.entries...)
}
