package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// API keys let Home Assistant and scripts sign in without the password of their user. Each user
// makes and revokes their own in My account, and administrators can revoke anybody's in Users.
// A key is sent as "Authorization: Bearer slm_..." and does at most what its user does now: a key
// made by an administrator who becomes an editor works as an editor's, and the keys of a deleted
// user go with them. Only a SHA-256 hash of each key is kept, beside its user in users.json, so a
// key is shown once, when it is made, and never again: not in a page, an answer, the log, the
// activity or a backup.

const (
	apiKeyPrefix = "slm_"
	// letters and digits only, so a double click selects the whole key; 40 of them are 238 bits,
	// far too many to guess, which is why a fast hash is enough to keep them
	apiKeyAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	apiKeyLength   = 40
	// the end of a key shown to tell it from the others
	apiKeyHintLength = 4
	maxAPIKeys       = 20
	maxAPIKeyName    = 40
)

// errors shown to the user, translated by the interface
var (
	ErrAPIKeyName       = errors.New("Give the key a name of up to 40 characters.")
	ErrAPIKeyRole       = errors.New("A key cannot do more than its user.")
	ErrAPIKeyCount      = errors.New("A user can have at most 20 API keys. Revoke one that is no longer used.")
	ErrAPIKeyNotFound   = errors.New("The API key does not exist.")
	ErrAPIKeyAccount    = errors.New("Only the users created in Users can have API keys.")
	ErrAPIKeyFromAPIKey = errors.New("An API key cannot make or revoke API keys: sign in with the password.")
)

// details of the activity, translated by the interface
const (
	apiKeyFailDetail    = "unknown API key"
	apiKeyBlockedDetail = "API key"
)

// APIKey is a key an API client signs in with. The key itself is never kept, only its hash.
type APIKey struct {
	// names the key in the pages and the API; it is not secret
	ID   string `json:"id"`
	Name string `json:"name"`
	// the most the key may do; never more than its user may do at the time
	Role string `json:"role"`
	// SHA-256 of the key, in hexadecimal
	Hash string `json:"hash"`
	// the last characters of the key
	Hint    string    `json:"hint"`
	Created time.Time `json:"created"`
	// the last request signed in with it, written at most every loginNoteInterval; zero if never
	LastUsed time.Time `json:"last_used,omitzero"`
}

// roleRank orders the roles from the one that may do the least; an unknown role is a guest's.
func roleRank(role string) int {
	return max(slices.Index(roles, role), 0)
}

// lowerRole is the role of the two that may do less.
func lowerRole(a, b string) string {
	if roleRank(a) <= roleRank(b) {
		return a
	}
	return b
}

// apiKeyRoles are the roles a user may give a key: read only (a player's: looking and
// downloading), and their own role when it may do more.
func apiKeyRoles(userRole string) []string {
	choices := []string{ROLE_VIEWER}
	if roleRank(userRole) > roleRank(ROLE_VIEWER) {
		choices = append(choices, userRole)
	}
	return choices
}

// apiKeyRoleName is the name of the role of a key, translated by the interface.
func apiKeyRoleName(role string) string {
	if role == ROLE_VIEWER {
		return "Read only"
	}
	if name, ok := roleNames[role]; ok {
		return name
	}
	return roleNames[ROLE_GUEST]
}

// apiKeyRoleDetail is the role of a key in the activity log, translated by translateActivity.
func apiKeyRoleDetail(role string) string {
	if role == ROLE_VIEWER {
		return "read only"
	}
	return role
}

// newAPIKeySecret makes a key: the prefix and apiKeyLength letters and digits from crypto/rand.
func newAPIKeySecret() (string, error) {
	key := make([]byte, 0, len(apiKeyPrefix)+apiKeyLength)
	key = append(key, apiKeyPrefix...)
	random := make([]byte, 64)
	for len(key) < cap(key) {
		if _, err := rand.Read(random); err != nil {
			return "", err
		}
		for _, b := range random {
			// bytes from 248 up are left out, so each of the 62 letters is as likely as the others
			if b < 248 && len(key) < cap(key) {
				key = append(key, apiKeyAlphabet[b%62])
			}
		}
	}
	return string(key), nil
}

func newAPIKeyId() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}

func apiKeyHash(secret string) [sha256.Size]byte {
	return sha256.Sum256([]byte(secret))
}

// cleanAPIKeyName returns the name of a key without the spaces around it, if it can be one: up to
// maxAPIKeyName letters, without control characters.
func cleanAPIKeyName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxAPIKeyName {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", false
		}
	}
	return name, true
}

// AddAPIKey makes a key for a user and returns it: the only time it is seen, as only its hash is
// kept. The role must be one of apiKeyRoles; read only given by a guest stays a guest's.
func (s *UserStore) AddAPIKey(owner string, name string, role string) (string, APIKey, error) {
	name, ok := cleanAPIKeyName(name)
	if !ok {
		return "", APIKey{}, ErrAPIKeyName
	}
	secret, err := newAPIKeySecret()
	if err != nil {
		return "", APIKey{}, err
	}
	id, err := newAPIKeyId()
	if err != nil {
		return "", APIKey{}, err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	user, ok := s.users[strings.ToLower(owner)]
	if !ok {
		return "", APIKey{}, ErrUserNotFound
	}
	if !slices.Contains(apiKeyRoles(user.Role), role) {
		return "", APIKey{}, ErrAPIKeyRole
	}
	if len(user.APIKeys) >= maxAPIKeys {
		return "", APIKey{}, ErrAPIKeyCount
	}
	hash := apiKeyHash(secret)
	key := APIKey{ID: id, Name: name, Role: lowerRole(role, user.Role), Hash: hex.EncodeToString(hash[:]), Hint: secret[len(secret)-apiKeyHintLength:], Created: time.Now()}
	user.APIKeys = append(user.APIKeys, key)
	if err := s.save(); err != nil {
		// a key that was not saved must not work until the next start either
		user.APIKeys = user.APIKeys[:len(user.APIKeys)-1]
		return "", APIKey{}, err
	}
	return secret, key, nil
}

// DeleteAPIKey revokes a key of a user, or of any user when owner is "" (an administrator). It
// returns the user the key was of and the key.
func (s *UserStore) DeleteAPIKey(owner string, id string) (string, APIKey, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, user := range s.users {
		if owner != "" && !strings.EqualFold(user.Name, owner) {
			continue
		}
		for i, key := range user.APIKeys {
			if key.ID != id {
				continue
			}
			user.APIKeys = slices.Delete(user.APIKeys, i, i+1)
			if err := s.save(); err != nil {
				user.APIKeys = slices.Insert(user.APIKeys, i, key)
				return "", APIKey{}, err
			}
			return user.Name, key, nil
		}
	}
	return "", APIKey{}, ErrAPIKeyNotFound
}

// FindAPIKey returns the user a key belongs to and the key. The hash of the key is compared with
// every stored one, in constant time and without stopping at the one that matches, so the time
// taken tells nothing of the keys.
func (s *UserStore) FindAPIKey(secret string) (User, APIKey, bool) {
	given := apiKeyHash(secret)
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	var owner *User
	var found APIKey
	for _, user := range s.users {
		for _, key := range user.APIKeys {
			stored, err := hex.DecodeString(key.Hash)
			if err == nil && subtle.ConstantTimeCompare(given[:], stored) == 1 {
				owner, found = user, key
			}
		}
	}
	if owner == nil {
		return User{}, APIKey{}, false
	}
	return owner.clone(), found, true
}

// NoteAPIKeyUse records the use of a key, which is also a sign-in of its user, at most once every
// loginNoteInterval: an API client signs in with every request.
func (s *UserStore) NoteAPIKeyUse(owner string, id string, when time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	user, ok := s.users[strings.ToLower(owner)]
	if !ok {
		return nil
	}
	for i := range user.APIKeys {
		if key := &user.APIKeys[i]; key.ID == id && when.Sub(key.LastUsed) >= loginNoteInterval {
			key.LastUsed = when
			if when.Sub(user.LastLogin) >= loginNoteInterval {
				user.LastLogin = when
			}
			return s.save()
		}
	}
	return nil
}

// KeyRow is an API key as the pages and the API show it: never the key, nor its hash.
type KeyRow struct {
	User string `json:"user"`
	ID   string `json:"id"`
	Name string `json:"name"`
	// what the key may do now: the lower of its role and the role of its user
	Role string `json:"role"`
	// the role it was made with, when its user may do less now
	MadeAs   string    `json:"madeAs,omitempty"`
	Hint     string    `json:"hint"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
}

// RoleName is the name of what the key may do, and MadeAsName the one it was made with,
// translated by the interface.
func (k KeyRow) RoleName() string   { return apiKeyRoleName(k.Role) }
func (k KeyRow) MadeAsName() string { return apiKeyRoleName(k.MadeAs) }

func keyRow(user User, key APIKey) KeyRow {
	row := KeyRow{User: user.Name, ID: key.ID, Name: key.Name, Role: lowerRole(key.Role, user.Role), Hint: apiKeyPrefix + "…" + key.Hint, Created: key.Created, LastUsed: key.LastUsed}
	if row.Role != key.Role {
		row.MadeAs = key.Role
	}
	return row
}

// KeyRows lists the keys of a user, or of every user when owner is "": by user, the newest first.
func (s *UserStore) KeyRows(owner string) []KeyRow {
	rows := []KeyRow{}
	for _, user := range s.List() {
		if owner != "" && !strings.EqualFold(user.Name, owner) {
			continue
		}
		keys := slices.Clone(user.APIKeys)
		sort.SliceStable(keys, func(i, j int) bool { return keys[i].Created.After(keys[j].Created) })
		for _, key := range keys {
			rows = append(rows, keyRow(user, key))
		}
	}
	return rows
}

// bearerKey returns an API key sent as "Authorization: Bearer slm_...". A bearer token of another
// kind, such as one a proxy in front adds for itself, is not taken for an attempt: it would count
// as a failed sign-in with every request and block the address of its users.
func bearerKey(r *http.Request) (string, bool) {
	scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, strings.HasPrefix(value, apiKeyPrefix)
}

// fromAPIKey returns who a key signs in: its user, with the lower of the roles of the key and of
// the user now.
func (a *Auth) fromAPIKey(secret string) (*Principal, APIKey, bool) {
	user, key, ok := a.users.FindAPIKey(secret)
	if !ok {
		return nil, APIKey{}, false
	}
	return &Principal{Name: user.Name, Role: lowerRole(key.Role, user.Role), Source: "key"}, key, true
}

// keyAttempt notes a sign-in with an API key and tells the hook of it: a use once a day by key and
// address, as an API client signs in with every request, and a failed or blocked one once an hour
// by address. The key itself is never passed.
func (a *Auth) keyAttempt(user string, key APIKey, ip string, result string) {
	if result == "ok" {
		a.users.NoteAPIKeyUse(user, key.ID, time.Now())
	}
	if a.onKey == nil {
		return
	}
	name, every := "", retryTellInterval
	if result == "ok" {
		name, every = "key:"+key.ID, apiLoginTellInterval
	}
	if !a.shouldTell(name, ip, result, every, time.Now()) {
		return
	}
	a.onKey(user, key.Name, ip, result)
}

// quotedKeyName is the name of a key in the activity log: in quotes, so it is never taken for a
// value of a form that the log translates.
func quotedKeyName(name string) string {
	return `"` + name + `"`
}

// recordKeyAttempt notes a sign-in with an API key in the activity log.
func (web *Web) recordKeyAttempt(user, keyName, ip, result string) {
	if isDemoMode() {
		return
	}
	entry := Activity{Time: time.Now(), IP: ip}
	switch result {
	case "ok":
		entry.User, entry.Action, entry.Detail = user, ACTION_KEY_USE, quotedKeyName(keyName)
	case "fail":
		entry.Action, entry.Detail = ACTION_LOGIN_FAIL, apiKeyFailDetail
	case "blocked":
		entry.Action, entry.Detail = ACTION_LOGIN_LOCK, apiKeyBlockedDetail
	default:
		return
	}
	web.activities().add(entry)
}
