package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The session cookies are signed, not stored, so Log out also writes down the signature of the
// session until it would have expired: a copied cookie stops working too.

const REVOKED_SESSIONS_FILENAME = "sessions-ended.json"

type revokedSessions struct {
	mutex sync.Mutex
	path  string
	// signature of the session -> when it expires (Unix seconds)
	ended map[string]int64
}

func loadRevokedSessions(dataFolder string) *revokedSessions {
	r := &revokedSessions{path: filepath.Join(dataFolder, REVOKED_SESSIONS_FILENAME), ended: map[string]int64{}}
	if data, err := os.ReadFile(r.path); err == nil {
		json.Unmarshal(data, &r.ended)
	}
	return r
}

func (r *revokedSessions) has(signature string) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	_, ok := r.ended[signature]
	return ok
}

// A session also ends when nobody used it for sessionIdle: when each one was last used is
// written down, at most once an hour, under a hash of its signature, so the file holds nothing a
// session could be made from.

const SEEN_SESSIONS_FILENAME = "sessions-seen.json"

const (
	// a session nobody used for this long ends; every use moves it on
	sessionIdle = 14 * 24 * time.Hour
	// how often the last use of a session is written down: not with every request
	sessionSeenWriteEvery = time.Hour
)

type seenSessions struct {
	mutex sync.Mutex
	path  string
	// hash of the signature of the session -> when it was last used and when it expires
	seen map[string]seenSession
}

type seenSession struct {
	Last   int64 `json:"last"`
	Expiry int64 `json:"expiry"`
}

func loadSeenSessions(dataFolder string) *seenSessions {
	s := &seenSessions{path: filepath.Join(dataFolder, SEEN_SESSIONS_FILENAME), seen: map[string]seenSession{}}
	if data, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(data, &s.seen)
	}
	return s
}

// use notes a use of a session that expires at expiry, and reports false when nobody used it for
// sessionIdle: it has ended then. A session not seen before, made by a version of before or
// lost with the file, is used now.
func (s *seenSessions) use(signature string, expiry int64, now time.Time) bool {
	if s == nil {
		return true
	}
	hash := sha256.Sum256([]byte(signature))
	key := hex.EncodeToString(hash[:16])
	s.mutex.Lock()
	entry, known := s.seen[key]
	if known && now.Sub(time.Unix(entry.Last, 0)) > sessionIdle {
		s.mutex.Unlock()
		return false
	}
	if known && now.Sub(time.Unix(entry.Last, 0)) < sessionSeenWriteEvery {
		s.mutex.Unlock()
		return true
	}
	// the ended ones are kept until they expire, or they would start again
	for other, each := range s.seen {
		if each.Expiry < now.Unix() {
			delete(s.seen, other)
		}
	}
	s.seen[key] = seenSession{Last: now.Unix(), Expiry: expiry}
	data, err := json.Marshal(s.seen)
	s.mutex.Unlock()
	if err == nil {
		writeFileAtomic(s.path, data)
	}
	return true
}

func (r *revokedSessions) add(signature string, expiry int64) {
	r.mutex.Lock()
	now := time.Now().Unix()
	for key, until := range r.ended {
		if until < now {
			delete(r.ended, key)
		}
	}
	r.ended[signature] = expiry
	data, err := json.Marshal(r.ended)
	r.mutex.Unlock()
	if err == nil {
		os.WriteFile(r.path, data, 0600)
	}
}
