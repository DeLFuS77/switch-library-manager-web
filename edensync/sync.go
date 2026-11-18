package edensync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Syncer runs the passes that keep the saves of an emulator of this computer in step with the
// library: only while the emulator is closed, both ways, and never replacing a save of the
// emulator that the library does not hold (it is uploaded first), and only while no other program
// keeps them in step on this computer (see lock.go).
type Syncer struct {
	Folder  Folder
	Library Library
	// the program that runs the passes, written in the lock of the folder: OwnerLibrary or
	// OwnerNXPress; the name of the program when ""
	Owner string
	// what the passes remember; NewState when nil
	State *State
	// where the state is written after every change; "" keeps it in memory
	StatePath string
	// where the ZIPs wait for a moment; "" for the temporary folder of the system
	TempDir string
	// whether the emulator is open: Running when nil (replaced in the tests)
	IsRunning func() (bool, error)
	Now       func() time.Time

	// when the pass going on last wrote the lock
	locked time.Time
}

// Item is a save of a game of a profile a pass did something with.
type Item struct {
	TitleId string `json:"titleId"`
	Profile string `json:"profile"`
	// the save of the library uploaded, brought or in conflict
	SaveId string `json:"saveId,omitempty"`
}

// Report is what a pass did.
type Report struct {
	// another program keeps the saves of the emulator in step on this computer (OwnerLibrary or
	// OwnerNXPress): nothing was looked at, or nothing more once it took them
	HeldBy string
	// the emulator was open: nothing was looked at
	Running   bool
	Uploaded  []Item
	Brought   []Item
	Conflicts []Item
	// games waiting: for the choice of the user, or to be opened once in the emulator with that
	// profile before a save can go in
	Waiting []Item
	// what failed with one game, the others went on
	Errors []error
}

func (s *Syncer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Syncer) running() (bool, error) {
	if s.IsRunning != nil {
		return s.IsRunning()
	}
	return Running(s.Folder.Emulator)
}

func (s *Syncer) save() error {
	if s.StatePath == "" {
		return nil
	}
	return s.State.Save(s.StatePath)
}

func (s *Syncer) owner() string {
	if s.Owner != "" {
		return s.Owner
	}
	return strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
}

// lock takes or renews the lock of the folder of the emulator, and gives the program that holds
// it instead, "" when it is ours. A folder without its data folder is not locked: Find and Open
// always tell it.
func (s *Syncer) lock() (string, error) {
	if s.Folder.Root == "" {
		return "", nil
	}
	now := s.now()
	held, err := takeLock(s.Folder, s.owner(), now)
	if err == nil && held == "" {
		s.locked = now
	}
	return held, err
}

// renew renews the lock as a long pass goes, and tells the program that took it meanwhile.
func (s *Syncer) renew() (string, error) {
	if s.now().Sub(s.locked) < lockRenew {
		return "", nil
	}
	return s.lock()
}

// Release removes the lock of the folder of the emulator when it is ours, so the other program
// may keep the saves in step at once: as the passes are turned off, and as the program stops.
func (s *Syncer) Release() error {
	return Release(s.Folder, s.owner())
}

// Pass looks at every save of every profile of the emulator once: uploads the ones that changed,
// brings the newer ones of the library, tells the conflicts and applies the choices of the user.
// With the emulator open it does nothing, and with the saves kept in step by another program on
// this computer either (Report.HeldBy). It takes or renews the lock of the folder first, also while
// the emulator is open.
func (s *Syncer) Pass(ctx context.Context) (Report, error) {
	report := Report{}
	if s.State == nil {
		s.State = NewState()
	}
	held, err := s.lock()
	if err != nil {
		return report, err
	}
	if held != "" {
		report.HeldBy = held
		return report, nil
	}
	running, err := s.running()
	if err != nil {
		return report, err
	}
	if running {
		report.Running = true
		return report, nil
	}
	profiles, err := s.Folder.Profiles()
	if err != nil {
		return report, err
	}
	pairs, err := s.pairs(ctx, profiles)
	if err != nil {
		return report, err
	}
	saves, err := s.Library.Saves(ctx)
	if err != nil {
		return report, err
	}
	conflicts, err := s.Library.Conflicts(ctx)
	if err != nil {
		return report, err
	}
	// a long pass renews the lock before each game it may write, and stops when another program
	// took it meanwhile
	stop := func() (bool, error) {
		held, err := s.renew()
		if err == nil && held == "" {
			return false, nil
		}
		report.HeldBy = held
		if saveErr := s.save(); err == nil {
			err = saveErr
		}
		return true, err
	}
	open := map[string]bool{}
	for _, conflict := range conflicts {
		if conflict.Keep == "" {
			open[conflict.Profile+"/"+strings.ToUpper(conflict.TitleId)] = true
			report.Waiting = append(report.Waiting, Item{TitleId: conflict.TitleId, Profile: conflict.Profile, SaveId: conflict.ConsoleSave})
			continue
		}
		if stopped, err := stop(); stopped {
			return report, err
		}
		if err := s.apply(ctx, conflict, profiles, pairs, &saves, &report); err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("%s: %w", conflict.TitleId, err))
		}
	}
	for _, profile := range profiles {
		account := accountOf(pairs, profile.ID.String())
		titles, err := s.titles(profile, account, pairs, saves)
		if err != nil {
			report.Errors = append(report.Errors, err)
		}
		for _, title := range titles {
			if open[profile.ID.String()+"/"+title] {
				continue
			}
			if stopped, err := stop(); stopped {
				return report, err
			}
			if err := s.syncGame(ctx, profile, account, title, pairs, &saves, &report); err != nil {
				report.Errors = append(report.Errors, fmt.Errorf("%s: %w", title, err))
			}
		}
	}
	s.State.LastPass = s.now().Unix()
	return report, s.save()
}

// pairs tells the library the profiles of the emulator when they changed, and pairs by itself a
// profile not chosen with the account of a console that is plain to tell: the one of its name, or
// the only one when the emulator has one profile.
func (s *Syncer) pairs(ctx context.Context, profiles []Profile) (Pairs, error) {
	reports := make([]ProfileReport, len(profiles))
	told := []string{string(s.Folder.Emulator)}
	for i, profile := range profiles {
		reports[i] = ProfileReport{Profile: profile.ID.String(), Name: profile.Name}
		told = append(told, profile.ID.String()+"="+profile.Name)
	}
	signature := strings.Join(told, "\n")
	var pairs Pairs
	var err error
	if signature != s.State.Reported {
		pairs, err = s.Library.ReportProfiles(ctx, s.Folder.Emulator, reports)
		if err == nil {
			s.State.Reported = signature
		}
	} else {
		pairs, err = s.Library.Pairs(ctx)
	}
	if err != nil {
		return Pairs{}, err
	}
	auto := false
	for i, profile := range profiles {
		paired, ok := pairedProfile(pairs, profile.ID.String())
		if ok && (paired.Account != "" || paired.Chosen) {
			continue
		}
		account := ""
		for _, known := range pairs.Accounts {
			if strings.EqualFold(strings.TrimSpace(known), strings.TrimSpace(profile.Name)) {
				account = known
			}
		}
		if account == "" && len(profiles) == 1 && len(pairs.Accounts) == 1 {
			account = pairs.Accounts[0]
		}
		if account != "" {
			reports[i].Account = &account
			auto = true
		}
	}
	if auto {
		return s.Library.ReportProfiles(ctx, s.Folder.Emulator, reports)
	}
	return pairs, nil
}

// pairedProfile is a profile as the pairs have it: the one of this computer first.
func pairedProfile(pairs Pairs, id string) (PairedProfile, bool) {
	found, ok := PairedProfile{}, false
	for _, paired := range pairs.Profiles {
		if paired.Profile == id && (!ok || paired.Mine) {
			found, ok = paired, true
		}
	}
	return found, ok
}

// accountOf is the account of the console a profile goes with, "" for none.
func accountOf(pairs Pairs, id string) string {
	paired, _ := pairedProfile(pairs, id)
	return paired.Account
}

// relevant are the saves of a game for a profile: of the consoles of its account, and of the
// emulators of the profiles that go with the same account, on any computer.
func relevant(saves []LibrarySave, profile, account, title string, pairs Pairs) []LibrarySave {
	list := []LibrarySave{}
	for _, save := range saves {
		if !strings.EqualFold(save.TitleId, title) {
			continue
		}
		switch {
		case !save.Emulator:
			if account != "" && strings.EqualFold(strings.TrimSpace(save.Account), account) {
				list = append(list, save)
			}
		case save.Profile == profile:
			list = append(list, save)
		case save.Profile != "" && account != "" && strings.EqualFold(accountOf(pairs, save.Profile), account):
			list = append(list, save)
		}
	}
	return list
}

// newestOf is the newest save of the consoles (emulator false) or of the emulators, nil for none.
func newestOf(saves []LibrarySave, emulator bool) *LibrarySave {
	var newest *LibrarySave
	for i := range saves {
		if saves[i].Emulator != emulator {
			continue
		}
		if newest == nil || saves[i].When > newest.When {
			newest = &saves[i]
		}
	}
	return newest
}

// holds tells a save of the library with these files.
func holds(saves []LibrarySave, hash string) bool {
	for _, save := range saves {
		if save.Hash != "" && save.Hash == hash {
			return true
		}
	}
	return false
}

// titles are the games of a profile: the ones with a folder in the emulator, and the ones with a
// save of the library for it.
func (s *Syncer) titles(profile Profile, account string, pairs Pairs, saves []LibrarySave) ([]string, error) {
	found := map[string]bool{}
	games, err := s.Folder.Games(profile.ID)
	for _, game := range games {
		found[game] = true
	}
	for _, save := range saves {
		title, ok := NormalTitleID(save.TitleId)
		if ok && !found[title] && len(relevant([]LibrarySave{save}, profile.ID.String(), account, title, pairs)) > 0 {
			found[title] = true
		}
	}
	list := make([]string, 0, len(found))
	for title := range found {
		list = append(list, title)
	}
	sort.Strings(list)
	return list, err
}

// note writes that the save of a game of a profile is in step with a save of the library, the
// newest of the ones known being seen.
func (s *Syncer) note(key, saveId, hash string, known []LibrarySave) {
	synced := &Synced{SaveId: saveId, Hash: hash}
	for _, save := range known {
		if save.When > synced.Seen {
			synced.Seen, synced.SeenIds = save.When, nil
		}
		if save.When == synced.Seen {
			synced.SeenIds = append(synced.SeenIds, save.Id)
		}
	}
	s.State.Games[key] = synced
}

// syncGame does what Decide tells with the save of a game of a profile.
func (s *Syncer) syncGame(ctx context.Context, profile Profile, account, title string, pairs Pairs, saves *[]LibrarySave, report *Report) error {
	dir := s.Folder.SaveFolder(profile.ID, title)
	if err := Recover(dir); err != nil {
		return err
	}
	local, err := HashFolder(dir)
	if err != nil {
		return err
	}
	known := relevant(*saves, profile.ID.String(), account, title, pairs)
	key := StateKey(s.Folder.Emulator, profile.ID, title)
	last := s.State.Games[key]
	adopted := false
	if last == nil && local != "" {
		// never synced, but the library holds these files: in step with that save
		for _, save := range known {
			if save.Hash == local && (last == nil || save.When > last.Seen) {
				last, adopted = &Synced{SaveId: save.Id, Hash: local, Seen: save.When, SeenIds: []string{save.Id}}, true
			}
		}
	}
	decision := Decide(last, local, newestOf(known, false), newestOf(known, true))
	item := Item{TitleId: title, Profile: profile.ID.String()}
	switch decision.Action {
	case DoNothing:
		switch {
		case decision.InStep != nil:
			if old := s.State.Games[key]; old == nil || old.SaveId != decision.InStep.Id || old.Hash != local {
				s.note(key, decision.InStep.Id, local, known)
				return s.save()
			}
		case adopted:
			s.State.Games[key] = last
			return s.save()
		}
		return nil
	case DoUpload:
		uploaded, err := s.upload(ctx, profile, title, dir, local)
		if err != nil {
			return err
		}
		*saves = append(*saves, uploaded)
		s.note(key, uploaded.Id, local, append(known, uploaded))
		item.SaveId = uploaded.Id
		report.Uploaded = append(report.Uploaded, item)
		return s.save()
	case DoBring:
		if !isDir(dir) {
			// the game was never opened with this profile: the emulator makes its folder first
			item.SaveId = decision.Save.Id
			report.Waiting = append(report.Waiting, item)
			return nil
		}
		if local != "" && !holds(known, local) {
			// the save replaced stays in the library
			copied, err := s.upload(ctx, profile, title, dir, local)
			if err != nil {
				return err
			}
			*saves = append(*saves, copied)
			known = append(known, copied)
		}
		hash, err := s.bring(ctx, title, decision.Save.Id, dir)
		if err != nil {
			return err
		}
		s.note(key, decision.Save.Id, hash, known)
		item.SaveId = decision.Save.Id
		report.Brought = append(report.Brought, item)
		return s.save()
	case DoAsk:
		uploaded, err := s.upload(ctx, profile, title, dir, local)
		if err != nil {
			return err
		}
		*saves = append(*saves, uploaded)
		if _, err := s.Library.ReportConflict(ctx, Conflict{TitleId: title, Profile: profile.ID.String(), ConsoleSave: decision.Save.Id, EmulatorSave: uploaded.Id}); err != nil {
			return err
		}
		item.SaveId = decision.Save.Id
		report.Conflicts = append(report.Conflicts, item)
		return s.save()
	}
	return nil
}

// apply does what the user chose for a conflict, and closes it.
func (s *Syncer) apply(ctx context.Context, conflict Conflict, profiles []Profile, pairs Pairs, saves *[]LibrarySave, report *Report) error {
	var profile *Profile
	for i := range profiles {
		if profiles[i].ID.String() == conflict.Profile {
			profile = &profiles[i]
		}
	}
	title, ok := NormalTitleID(conflict.TitleId)
	if profile == nil || !ok {
		// the profile is gone from the emulator: nothing to apply
		return s.Library.ConflictDone(ctx, conflict.Id)
	}
	dir := s.Folder.SaveFolder(profile.ID, title)
	if err := Recover(dir); err != nil {
		return err
	}
	local, err := HashFolder(dir)
	if err != nil {
		return err
	}
	account := accountOf(pairs, profile.ID.String())
	known := relevant(*saves, profile.ID.String(), account, title, pairs)
	key := StateKey(s.Folder.Emulator, profile.ID, title)
	find := func(id string) *LibrarySave {
		for i := range *saves {
			if (*saves)[i].Id == id {
				return &(*saves)[i]
			}
		}
		return nil
	}
	switch conflict.Keep {
	case KeepConsole:
		chosen := find(conflict.ConsoleSave)
		if chosen == nil {
			s.Library.ConflictDone(ctx, conflict.Id)
			return errors.New("the save chosen is no longer in the library")
		}
		if local != "" && !holds(known, local) {
			copied, err := s.upload(ctx, *profile, title, dir, local)
			if err != nil {
				return err
			}
			*saves = append(*saves, copied)
			known = append(known, copied)
		}
		hash, err := s.bring(ctx, title, chosen.Id, dir)
		if err != nil {
			return err
		}
		s.note(key, chosen.Id, hash, known)
		report.Brought = append(report.Brought, Item{TitleId: title, Profile: conflict.Profile, SaveId: chosen.Id})
	case KeepEmulator:
		// the emulator keeps its save; one played since is uploaded by the next pass
		hash := local
		if chosen := find(conflict.EmulatorSave); chosen != nil && chosen.Hash != "" {
			hash = chosen.Hash
		}
		s.note(key, conflict.EmulatorSave, hash, known)
	default:
		return fmt.Errorf("unknown choice %q", conflict.Keep)
	}
	if err := s.save(); err != nil {
		return err
	}
	return s.Library.ConflictDone(ctx, conflict.Id)
}

// upload keeps the save of a folder in the library, marked as uploaded by itself.
func (s *Syncer) upload(ctx context.Context, profile Profile, title, dir, hash string) (LibrarySave, error) {
	temporary, err := os.CreateTemp(s.TempDir, "edensync-*.zip")
	if err != nil {
		return LibrarySave{}, err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if err := ZipFolder(dir, temporary); err != nil {
		return LibrarySave{}, err
	}
	size, err := temporary.Seek(0, 1)
	if err != nil {
		return LibrarySave{}, err
	}
	if _, err := temporary.Seek(0, 0); err != nil {
		return LibrarySave{}, err
	}
	uploaded, err := s.Library.Upload(ctx, Upload{TitleId: title, Emulator: s.Folder.Emulator, Profile: profile, Auto: true, Body: temporary, Size: size})
	if err != nil {
		return LibrarySave{}, err
	}
	uploaded.TitleId, uploaded.Emulator, uploaded.Profile = title, true, profile.ID.String()
	if uploaded.Hash == "" {
		uploaded.Hash = hash
	}
	s.State.Uploaded++
	s.State.LastSync = s.now().Unix()
	return uploaded, nil
}

// bring puts a save of the library in a folder of the emulator, and gives the hash of the folder.
func (s *Syncer) bring(ctx context.Context, title, id, dir string) (string, error) {
	temporary, err := os.CreateTemp(s.TempDir, "edensync-*.zip")
	if err != nil {
		return "", err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if err := s.Library.Download(ctx, title, id, temporary); err != nil {
		return "", err
	}
	info, err := temporary.Stat()
	if err != nil {
		return "", err
	}
	if err := ReplaceFromZip(dir, temporary, info.Size()); err != nil {
		return "", err
	}
	s.State.Brought++
	s.State.LastSync = s.now().Unix()
	return HashFolder(dir)
}
