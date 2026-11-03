package userdata

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
)

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

var PlayStateLooks = map[string]PlayState{
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
		states[i] = PlayStateLooks[state]
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
	return PlayStateLooks[e.Status]
}

// PlayStore is how the games go for each user, saved in the data folder.
type PlayStore struct {
	mutex sync.Mutex
	path  string
	// by user, then by upper case title ID
	users map[string]map[string]PlayEntry
}

// LoadPlay reads the states saved at path.
func LoadPlay(path string) *PlayStore {
	store := &PlayStore{path: path}
	store.Reload()
	return store
}

// Reload reads the states again, also after a backup was restored.
func (s *PlayStore) Reload() {
	users := map[string]map[string]PlayEntry{}
	if data, ok := durable.ReadStore(s.path); ok {
		durable.ParseStore(s.path, data, &users)
	}
	if users == nil {
		users = map[string]map[string]PlayEntry{}
	}
	s.mutex.Lock()
	s.users = users
	s.mutex.Unlock()
}

// Of is how a game goes for a user.
func (s *PlayStore) Of(user string, id string) PlayEntry {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.users[user][strings.ToUpper(id)]
}

// All are the games of a user, a copy.
func (s *PlayStore) All(user string) map[string]PlayEntry {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	result := make(map[string]PlayEntry, len(s.users[user]))
	for id, entry := range s.users[user] {
		result[id] = entry
	}
	return result
}

// Set saves how a game goes for a user; an empty entry forgets the game.
func (s *PlayStore) Set(user string, id string, entry PlayEntry) error {
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
	return durable.WriteFile(s.path, data)
}

// CleanPlayEntry checks what a form sends; ok is false when it is not valid.
func CleanPlayEntry(status string, rating string, note string) (PlayEntry, bool) {
	entry := PlayEntry{}
	if status != "" {
		if _, ok := PlayStateLooks[status]; !ok {
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
