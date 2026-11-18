package edensync

import (
	"context"
	"io"
)

// LibrarySave is a save the library keeps, as GET /install/saves lists it.
type LibrarySave struct {
	TitleId string `json:"title_id"`
	Game    string `json:"game,omitempty"`
	Id      string `json:"id"`
	// the time it was kept, by the clock of the library (seconds since 1970)
	When    int64  `json:"when"`
	Note    string `json:"note,omitempty"`
	Version int    `json:"version,omitempty"`
	// the account of the console it came from, or the name of the profile of the emulator
	Account string `json:"account,omitempty"`
	// the console it came from, or the emulator (Eden, Citron)
	Console string `json:"console,omitempty"`
	Size    int64  `json:"size"`
	Pinned  bool   `json:"pinned,omitempty"`
	// a save of an emulator; Profile is the UUID of its profile (ProfileID.String) when a
	// computer uploaded it, and Device the computer
	Emulator bool   `json:"emulator,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Device   string `json:"device,omitempty"`
	// uploaded by itself, by a pass
	Auto bool `json:"auto,omitempty"`
	// the hash of its files, as HashZip; "" when the library does not tell it
	Hash string `json:"hash,omitempty"`
}

// PairedProfile is a profile of an emulator of a computer of the user, and the account of the
// console it goes with.
type PairedProfile struct {
	Profile  string `json:"profile"`
	Name     string `json:"name"`
	Emulator string `json:"emulator"`
	// the account of the console, "" for none
	Account string `json:"account"`
	// the account was chosen, by the user or by the computer: a pass does not pair it by itself
	Chosen bool `json:"chosen,omitempty"`
	// the computer, and whether it is the one asking
	Computer string `json:"computer,omitempty"`
	Mine     bool   `json:"mine,omitempty"`
}

// Pairs is how the profiles of the emulators of the user go with the accounts of their consoles.
type Pairs struct {
	// the accounts of the consoles, from the saves NXDock uploaded
	Accounts []string        `json:"accounts"`
	Profiles []PairedProfile `json:"profiles"`
}

// ProfileReport is a profile of the emulator told to the library. Account nil keeps the account
// the profile goes with; "" pairs it with none.
type ProfileReport struct {
	Profile string  `json:"profile"`
	Name    string  `json:"name"`
	Account *string `json:"account,omitempty"`
}

// what the user keeps of a conflict
const (
	KeepConsole  = "console"
	KeepEmulator = "emulator"
)

// Conflict is a game whose save changed both in the emulator and in the library since they were
// last in step. Both saves are in the library; the user chooses which one the emulator keeps.
type Conflict struct {
	Id      string `json:"id,omitempty"`
	TitleId string `json:"titleId"`
	Profile string `json:"profile"`
	// the save of the library that changed (of the console, or of the emulator of another
	// computer), and the one of this emulator, uploaded before the conflict was told
	ConsoleSave  string `json:"consoleSave"`
	EmulatorSave string `json:"emulatorSave"`
	When         int64  `json:"when,omitempty"`
	// "" while the user has not chosen; else KeepConsole or KeepEmulator
	Keep    string `json:"keep,omitempty"`
	Decided int64  `json:"decided,omitempty"`
}

// Upload is a save a pass keeps in the library: the ZIP of the folder of a game of a profile.
type Upload struct {
	TitleId  string
	Emulator Emulator
	Profile  Profile
	// uploaded by itself
	Auto bool
	Body io.Reader
	Size int64
}

// Library is the library a pass keeps the emulator in step with: Switch Library Manager Web over
// HTTP (HTTPLibrary), or its own storage when it runs on the same computer. Every call is of the
// user the computer was paired for.
type Library interface {
	// Saves lists every save of the user, of every game.
	Saves(ctx context.Context) ([]LibrarySave, error)
	// Download writes the ZIP of a save.
	Download(ctx context.Context, titleId, id string, out io.Writer) error
	// Upload keeps a save and tells how it is listed.
	Upload(ctx context.Context, upload Upload) (LibrarySave, error)
	// Pairs tells how the profiles go with the accounts of the consoles.
	Pairs(ctx context.Context) (Pairs, error)
	// ReportProfiles tells the profiles of the emulator of this computer (the ones not told are
	// forgotten) and gives the pairs as they are then.
	ReportProfiles(ctx context.Context, emulator Emulator, profiles []ProfileReport) (Pairs, error)
	// ReportConflict tells a conflict and gives its id; a conflict of the same game and profile
	// still open takes the new saves.
	ReportConflict(ctx context.Context, conflict Conflict) (string, error)
	// Conflicts are the conflicts of this computer not yet done, with the choice of the user.
	Conflicts(ctx context.Context) ([]Conflict, error)
	// ConflictDone tells that the choice was applied: the conflict closes.
	ConflictDone(ctx context.Context, id string) error
}
