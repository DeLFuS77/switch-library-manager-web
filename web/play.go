package web

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Each user keeps how their games go: pending, playing, finished, completed or abandoned, with
// a rating of 1 to 5 stars and a note of their own. Without login there is one of them for
// everyone. The Library shows it on the cards and filters by it, and the game page sets it.

const (
	PLAY_FILENAME = "play.json"
	maxPlayNote   = 1000
)

// the states of a game for a user, in the order they are offered
const (
	PLAY_BACKLOG   = "backlog"
	PLAY_PLAYING   = "playing"
	PLAY_FINISHED  = "finished"
	PLAY_COMPLETED = "completed"
	PLAY_ABANDONED = "abandoned"
)

var playStates = []string{PLAY_BACKLOG, PLAY_PLAYING, PLAY_FINISHED, PLAY_COMPLETED, PLAY_ABANDONED}

// PlayState is a state with its name and icon, for the pages.
type PlayState struct {
	Value string
	Name  string
	Icon  string
}

var playStateLooks = map[string]PlayState{
	PLAY_BACKLOG:   {PLAY_BACKLOG, "Pending", "bi-bookmark"},
	PLAY_PLAYING:   {PLAY_PLAYING, "Playing", "bi-controller"},
	PLAY_FINISHED:  {PLAY_FINISHED, "Finished", "bi-flag"},
	PLAY_COMPLETED: {PLAY_COMPLETED, "Completed 100%", "bi-trophy"},
	PLAY_ABANDONED: {PLAY_ABANDONED, "Abandoned", "bi-x-octagon"},
}

// PlayStates are the states, in order, for the pages.
func PlayStates() []PlayState {
	states := make([]PlayState, len(playStates))
	for i, state := range playStates {
		states[i] = playStateLooks[state]
	}
	return states
}

// PlayEntry is how a game goes for a user.
type PlayEntry struct {
	Status  string    `json:"status,omitempty"`
	Rating  int       `json:"rating,omitempty"`
	Note    string    `json:"note,omitempty"`
	Updated time.Time `json:"updated"`
}

func (e PlayEntry) empty() bool {
	return e.Status == "" && e.Rating == 0 && e.Note == ""
}

// Look is the name and icon of the state.
func (e PlayEntry) Look() PlayState {
	return playStateLooks[e.Status]
}

type playStore struct {
	mutex sync.Mutex
	path  string
	// by user, then by upper case title ID
	users map[string]map[string]PlayEntry
}

func (web *Web) play() *playStore {
	web.playOnce.Do(func() {
		store := &playStore{path: filepath.Join(web.dataFolder, PLAY_FILENAME)}
		store.reload()
		web.playStore = store
	})
	return web.playStore
}

// reload reads the states again, also after a backup was restored.
func (s *playStore) reload() {
	users := map[string]map[string]PlayEntry{}
	if data, ok := readStore(s.path); ok {
		parseStore(s.path, data, &users)
	}
	if users == nil {
		users = map[string]map[string]PlayEntry{}
	}
	s.mutex.Lock()
	s.users = users
	s.mutex.Unlock()
}

// of is how a game goes for a user.
func (s *playStore) of(user string, id string) PlayEntry {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.users[user][strings.ToUpper(id)]
}

// all are the games of a user, a copy.
func (s *playStore) all(user string) map[string]PlayEntry {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	result := make(map[string]PlayEntry, len(s.users[user]))
	for id, entry := range s.users[user] {
		result[id] = entry
	}
	return result
}

func (s *playStore) set(user string, id string, entry PlayEntry) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	id = strings.ToUpper(id)
	games := s.users[user]
	if games == nil {
		games = map[string]PlayEntry{}
		s.users[user] = games
	}
	if entry.empty() {
		delete(games, id)
	} else {
		entry.Updated = time.Now()
		games[id] = entry
	}
	data, err := json.Marshal(s.users)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data)
}

// playUser is the user the states of a request belong to: "" without login.
func playUser(r *http.Request) string {
	if principal := principalFrom(r); principal != nil {
		return principal.Name
	}
	return ""
}

// cleanPlayEntry checks what a form sends; ok is false when it is not valid.
func cleanPlayEntry(status string, rating string, note string) (PlayEntry, bool) {
	entry := PlayEntry{}
	if status != "" {
		if _, ok := playStateLooks[status]; !ok {
			return entry, false
		}
		entry.Status = status
	}
	if rating != "" {
		value, err := strconv.Atoi(rating)
		if err != nil || value < 0 || value > 5 {
			return entry, false
		}
		entry.Rating = value
	}
	note = strings.TrimSpace(strings.ReplaceAll(note, "\r\n", "\n"))
	if !utf8.ValidString(note) || utf8.RuneCountInString(note) > maxPlayNote {
		return entry, false
	}
	entry.Note = note
	return entry, true
}

func (web *Web) HandlePlay() {
	web.router.HandleFunc("/play", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		if !titleIdPattern.MatchString(id) {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid Title ID (16 hexadecimal characters)")
			return
		}
		entry, ok := cleanPlayEntry(r.FormValue("status"), r.FormValue("rating"), r.FormValue("note"))
		if !ok {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		if err := web.play().set(playUser(r), id, entry); err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": entry.Status, "rating": entry.Rating, "note": entry.Note})
	}).Methods("POST")
}
