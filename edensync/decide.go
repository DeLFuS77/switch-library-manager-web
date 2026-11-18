package edensync

import "slices"

// Action is what a pass does with the save of a game of a profile.
type Action int

const (
	// DoNothing: the emulator and the library are in step, or nothing changed.
	DoNothing Action = iota
	// DoUpload: the save of the emulator changed, the library keeps it.
	DoUpload
	// DoBring: a newer save of the library goes into the emulator.
	DoBring
	// DoAsk: both changed since the last sync: the user chooses which one stays.
	DoAsk
)

func (a Action) String() string {
	switch a {
	case DoUpload:
		return "upload"
	case DoBring:
		return "bring"
	case DoAsk:
		return "conflict"
	}
	return "nothing"
}

// Synced is the last time the save of a game of a profile was in step with the library.
type Synced struct {
	// the save of the library the folder held then, and the hash of the folder
	SaveId string `json:"save"`
	Hash   string `json:"hash"`
	// the time of the newest save of the library known then, and the saves of that time: a save
	// of the library is new when it is newer, or of that time and not one of them
	Seen    int64    `json:"seen"`
	SeenIds []string `json:"seenIds,omitempty"`
}

// Decision is what Decide tells to do.
type Decision struct {
	Action Action
	// DoBring: the save to bring; DoAsk: the save of the library that changed
	Save *LibrarySave
	// DoNothing: the folder already holds this save of the library, whose state may be noted
	InStep *LibrarySave
}

// newer is the newer of two saves of the library, the one of the console when both are of the
// same time; nil when there is none.
func newer(console, emulator *LibrarySave) *LibrarySave {
	switch {
	case console == nil:
		return emulator
	case emulator == nil:
		return console
	case emulator.When > console.When:
		return emulator
	}
	return console
}

// remoteChanged tells a save of the library that is not the one the folder was last in step
// with, nor one known then, nor one with the same files.
func remoteChanged(last *Synced, newest *LibrarySave) bool {
	if newest.Id == last.SaveId || (newest.Hash != "" && newest.Hash == last.Hash) {
		return false
	}
	if newest.When > last.Seen {
		return true
	}
	return newest.When == last.Seen && !slices.Contains(last.SeenIds, newest.Id)
}

// Decide tells what to do with the save of a game of a profile, from the last time it was in step
// (nil: never), the hash of its folder now ("" when there is no save), and the newest save of the
// library of the console and of an emulator (nil when there is none). A pass first takes for the
// last time in step a save of the library with the hash of the folder, when it never synced.
//
//   - nothing anywhere, or the same files on both sides: DoNothing;
//   - only the emulator has a save, or only it changed: DoUpload;
//   - the emulator has none, or only the library changed: DoBring the newest;
//   - both changed, or never in step and different: DoAsk, the user chooses.
//
// A save deleted in the emulator after a sync is not brought back unless the library changes.
func Decide(last *Synced, local string, console, emulator *LibrarySave) Decision {
	newest := newer(console, emulator)
	switch {
	case newest == nil && local == "":
		return Decision{Action: DoNothing}
	case newest == nil:
		return Decision{Action: DoUpload}
	case local == "":
		if last != nil && !remoteChanged(last, newest) {
			return Decision{Action: DoNothing}
		}
		return Decision{Action: DoBring, Save: newest}
	case newest.Hash != "" && newest.Hash == local:
		return Decision{Action: DoNothing, InStep: newest}
	case last == nil:
		return Decision{Action: DoAsk, Save: newest}
	}
	localChanged := local != last.Hash
	changed := remoteChanged(last, newest)
	switch {
	case localChanged && changed:
		return Decision{Action: DoAsk, Save: newest}
	case localChanged:
		return Decision{Action: DoUpload}
	case changed:
		return Decision{Action: DoBring, Save: newest}
	}
	return Decision{Action: DoNothing}
}
