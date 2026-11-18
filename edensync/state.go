package edensync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// stateVersion is the version of the state file, written in it.
const stateVersion = 1

// State is what a computer remembers between passes: when the save of each game of each profile
// was last in step with the library. It is kept in a file of its own (LoadState, Save).
type State struct {
	Version int `json:"version"`
	// by StateKey
	Games map[string]*Synced `json:"games"`
	// the profiles last told to the library, so they are told again only when they change
	Reported string `json:"reported,omitempty"`
	// the last pass that looked at the saves, and the last one that uploaded or brought one
	LastPass int64 `json:"lastPass,omitempty"`
	LastSync int64 `json:"lastSync,omitempty"`
	// what the passes did, counted
	Uploaded int `json:"uploaded,omitempty"`
	Brought  int `json:"brought,omitempty"`
}

// NewState is an empty state.
func NewState() *State {
	return &State{Version: stateVersion, Games: map[string]*Synced{}}
}

// StateKey names the save of a game of a profile of an emulator in the state.
func StateKey(emulator Emulator, profile ProfileID, titleId string) string {
	return string(emulator) + "/" + profile.String() + "/" + titleId
}

// LoadState reads a state file; a file that is missing is an empty state.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return nil, err
	}
	state := NewState()
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}
	if state.Games == nil {
		state.Games = map[string]*Synced{}
	}
	return state, nil
}

// Save writes the state through a temporary file, so a file cut halfway never takes its place.
func (s *State) Save(path string) error {
	s.Version = stateVersion
	data, err := json.MarshalIndent(s, "", "\t")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = temporary.Write(data)
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporary.Name(), path)
	}
	if err != nil {
		os.Remove(temporary.Name())
	}
	return err
}
