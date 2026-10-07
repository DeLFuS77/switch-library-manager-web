package web

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	ROLE_ADMIN  = "admin"
	ROLE_VIEWER = "viewer"

	USERS_FILENAME = "users.json"
)

// errors shown to the user, translated by the interface
var (
	ErrUserName        = errors.New("The user name may only contain letters, numbers, dots, dashes and underscores (up to 32).")
	ErrUserExists      = errors.New("A user with this name already exists.")
	ErrUserNotFound    = errors.New("The user does not exist.")
	ErrRole            = errors.New("Unknown role.")
	ErrLastAdmin       = errors.New("At least one administrator is needed.")
	ErrDeleteSelf      = errors.New("You cannot delete your own account.")
	ErrWrongPassword   = errors.New("The current password is wrong.")
	ErrEnvironmentUser = errors.New("This user is set with environment variables and cannot be changed here.")
)

var userErrors = []error{ErrUserName, ErrUserExists, ErrUserNotFound, ErrPasswordLength, ErrPasswordCommon, ErrPasswordName, ErrRole, ErrLastAdmin, ErrDeleteSelf, ErrWrongPassword, ErrEnvironmentUser}

var validUserName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// User is an account of the web interface.
type User struct {
	Name         string `json:"name"`
	Role         string `json:"role"`
	PasswordHash string `json:"password_hash"`
	// interface language of the user; empty for the language of the app
	Language string    `json:"language,omitempty"`
	Created  time.Time `json:"created"`
}

func (u User) IsAdmin() bool {
	return u.Role == ROLE_ADMIN
}

// UserStore keeps the accounts in users.json in the data folder.
type UserStore struct {
	mutex sync.RWMutex
	path  string
	users map[string]*User // by lower case name
	// a user configured with environment variables, which is always an administrator
	reservedName string
}

// a hash compared when the user does not exist, so the response time does not tell
// whether a name exists
// the cost of the password hashes: a guess takes a quarter of a second
const passwordCost = 12

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("not a real password"), passwordCost)

func loadUserStore(dataFolder string, reservedName string) (*UserStore, error) {
	store := &UserStore{path: filepath.Join(dataFolder, USERS_FILENAME), users: map[string]*User{}, reservedName: reservedName}
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	users := []*User{}
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, err
	}
	for _, user := range users {
		store.users[strings.ToLower(user.Name)] = user
	}
	return store, nil
}

// reload reads the users file again, after it was replaced.
func (s *UserStore) reload() error {
	loaded, err := loadUserStore(filepath.Dir(s.path), s.reservedName)
	if err != nil {
		return err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.users = loaded.users
	return nil
}

// List returns the users sorted by name.
func (s *UserStore) List() []User {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	users := make([]User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, *user)
	}
	sort.Slice(users, func(i, j int) bool { return strings.ToLower(users[i].Name) < strings.ToLower(users[j].Name) })
	return users
}

func (s *UserStore) Count() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return len(s.users)
}

func (s *UserStore) Get(name string) (User, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	user, ok := s.users[strings.ToLower(name)]
	if !ok {
		return User{}, false
	}
	return *user, true
}

// Verify checks a password; it takes the same time whether the user exists or not.
func (s *UserStore) Verify(name string, password string) (User, bool) {
	user, ok := s.Get(name)
	hash := dummyHash
	if ok {
		hash = []byte(user.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil || !ok {
		return User{}, false
	}
	return user, true
}

func (s *UserStore) Add(name string, password string, role string) error {
	return s.add(name, password, role, false)
}

// add creates a user; onlyFirst refuses it when a user exists already.
func (s *UserStore) add(name string, password string, role string, onlyFirst bool) error {
	if !validUserName.MatchString(name) {
		return ErrUserName
	}
	if role != ROLE_ADMIN && role != ROLE_VIEWER {
		return ErrRole
	}
	if err := checkPassword(name, password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := strings.ToLower(name)
	if _, exists := s.users[key]; exists || (s.reservedName != "" && strings.EqualFold(name, s.reservedName)) {
		return ErrUserExists
	}
	if onlyFirst && len(s.users) > 0 {
		return ErrUserExists
	}
	s.users[key] = &User{Name: name, Role: role, PasswordHash: hash, Created: time.Now()}
	return s.save()
}

// SetLanguage changes the interface language of a user ("" for the language of the app).
func (s *UserStore) SetLanguage(name string, lang string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	user, ok := s.users[strings.ToLower(name)]
	if !ok {
		return ErrUserNotFound
	}
	user.Language = lang
	return s.save()
}

func (s *UserStore) SetRole(name string, role string) error {
	if role != ROLE_ADMIN && role != ROLE_VIEWER {
		return ErrRole
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	user, ok := s.users[strings.ToLower(name)]
	if !ok {
		return ErrUserNotFound
	}
	if user.Role == ROLE_ADMIN && role != ROLE_ADMIN && s.adminCountLocked() == 1 && s.reservedName == "" {
		return ErrLastAdmin
	}
	user.Role = role
	return s.save()
}

func (s *UserStore) SetPassword(name string, password string) error {
	if err := checkPassword(name, password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	user, ok := s.users[strings.ToLower(name)]
	if !ok {
		return ErrUserNotFound
	}
	// a new hash also ends the sessions of the user, see sessionTag
	user.PasswordHash = hash
	return s.save()
}

func (s *UserStore) Delete(name string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := strings.ToLower(name)
	user, ok := s.users[key]
	if !ok {
		return ErrUserNotFound
	}
	if user.Role == ROLE_ADMIN && s.adminCountLocked() == 1 && s.reservedName == "" && len(s.users) > 1 {
		// the remaining users could not manage the accounts any more
		return ErrLastAdmin
	}
	delete(s.users, key)
	return s.save()
}

func (s *UserStore) adminCountLocked() int {
	count := 0
	for _, user := range s.users {
		if user.Role == ROLE_ADMIN {
			count++
		}
	}
	return count
}

// save writes the users with permissions for the owner only. The mutex must be held.
func (s *UserStore) save() error {
	users := make([]*User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return strings.ToLower(users[i].Name) < strings.ToLower(users[j].Name) })
	data, err := json.MarshalIndent(users, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func hashPassword(password string) (string, error) {
	// the length is also checked by checkPassword before this, this is the last guard
	if len(password) < minPasswordLength || len(password) > maxPasswordLength {
		return "", ErrPasswordLength
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}
