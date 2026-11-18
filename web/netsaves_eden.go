package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/edensync"
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/gorilla/mux"
)

// Eden on the computers of a user: a computer is paired with a code as a console is (NXPress
// asks with the kind "pc") and gets a key of the role PC, which only keeps the saves of the
// emulator of the computer in step with the saves of its user (see the package edensync):
//
//   - GET and POST /install/saves, as a console, the saves listed with the hash of their files;
//     an upload tells ?emulator=eden or citron, ?profile= (the UUID of the profile of the
//     emulator), ?account= (its name) and ?auto=1 when the computer uploads it by itself;
//   - GET /install/saves/profiles: {"accounts": [the accounts of the consoles], "profiles":
//     [{"profile", "name", "emulator", "account", "chosen", "computer", "mine"}]};
//   - PUT /install/saves/profiles {"emulator", "profiles": [{"profile", "name", "account"?}]}:
//     the profiles of the emulator of the computer, the ones not told forgotten; an account
//     given pairs the profile with it ("" with none), one not given keeps the one it has;
//   - POST /install/saves/conflicts {"titleId", "profile", "consoleSave", "emulatorSave"}: both
//     saves of a game changed, both are in the library; answers {"id"};
//   - GET /install/saves/conflicts: {"conflicts": [{"id", "titleId", "profile", "consoleSave",
//     "emulatorSave", "when", "keep", "decided"}]} of the computer, "keep" being "console" or
//     "emulator" once the user chose on the web;
//   - POST /install/saves/conflicts/{id}/done: the computer applied the choice.
//
// On the web, the page Saves shows the computers, pairs the profiles with the accounts of the
// consoles (POST /api/saves/profiles {"device", "profile", "account"}) and asks which save stays
// (POST /api/saves/conflicts/{id} {"keep"}). The app does the same as a computer for the
// emulator of its own computer when it runs on it (see netsaves_edenlocal.go), as the device
// "local". All of it is kept per user, in eden.json of the folder of their saves.

const (
	netInstallSavesProfiles  = netInstallSaves + "/profiles"
	netInstallSavesConflicts = netInstallSaves + "/conflicts"
	edenSavesFile            = "eden.json"
	// the device of the app itself, for the emulator of its own computer
	edenLocalDevice = "local"
	// the last pass of a computer is written at most so often
	edenSeenEvery = 5 * time.Minute
	// the most profiles and conflicts kept for a user
	maxEdenProfiles  = 64
	maxEdenConflicts = 200
)

var (
	errEdenConflictGone = errors.New("this choice is no longer asked")
	errEdenProfile      = errors.New("this profile is no longer known")
	errEdenAccount      = errors.New("no save of a console was uploaded with this account")
)

// pcAllowed is what a key of the role PC may ask: the saves of /install/saves, and nothing else
// of /install/, nor any page.
func pcAllowed(r *http.Request) bool {
	p := r.URL.Path
	if path.Clean(p) != p || r.URL.EscapedPath() != p {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return p == netInstallSaves || strings.HasPrefix(p, netInstallSaves+"/")
	case http.MethodPost:
		return netSaveUploadPath(p) || p == netInstallSavesConflicts ||
			(strings.HasPrefix(p, netInstallSavesConflicts+"/") && strings.HasSuffix(p, "/done"))
	case http.MethodPut:
		return p == netInstallSavesProfiles
	}
	return false
}

// netPcKey is the key of the computer of a request: a key of the role PC.
func netPcKey(w http.ResponseWriter, r *http.Request) (*Principal, bool) {
	principal := principalFrom(r)
	if principal.Source != "key" || principal.Role != ROLE_PC || !netKeyIdPattern.MatchString(principal.KeyID) {
		http.Error(w, "only the key of a computer", http.StatusForbidden)
		return nil, false
	}
	return principal, true
}

// edenProfile is a profile of an emulator of a computer, and the account of the console it goes
// with.
type edenProfile struct {
	// the id of the key of the computer, or edenLocalDevice
	Device   string `json:"device"`
	Emulator string `json:"emulator"`
	Profile  string `json:"profile"`
	Name     string `json:"name"`
	Account  string `json:"account"`
	Chosen   bool   `json:"chosen,omitempty"`
}

// edenConflict is a game whose save changed both in the emulator of a computer and in the
// library; Keep is "" until the user chooses.
type edenConflict struct {
	Id           string `json:"id"`
	Device       string `json:"device"`
	TitleId      string `json:"titleId"`
	Profile      string `json:"profile"`
	ConsoleSave  string `json:"consoleSave"`
	EmulatorSave string `json:"emulatorSave"`
	When         int64  `json:"when"`
	Keep         string `json:"keep,omitempty"`
	Decided      int64  `json:"decided,omitempty"`
}

func (c edenConflict) shared() edensync.Conflict {
	return edensync.Conflict{Id: c.Id, TitleId: c.TitleId, Profile: c.Profile, ConsoleSave: c.ConsoleSave, EmulatorSave: c.EmulatorSave,
		When: c.When, Keep: c.Keep, Decided: c.Decided}
}

// edenComputer is when a computer last looked at the saves, and last uploaded or took one.
type edenComputer struct {
	Seen int64 `json:"seen,omitempty"`
	Sync int64 `json:"sync,omitempty"`
}

// edenFile is what eden.json of a user holds.
type edenFile struct {
	Profiles  []edenProfile           `json:"profiles"`
	Conflicts []edenConflict          `json:"conflicts"`
	Computers map[string]edenComputer `json:"computers"`
}

func (web *Web) edenPath(user string) string {
	return filepath.Join(web.netSavesUserFolder(user), edenSavesFile)
}

// readEden reads eden.json of a user. With web.netSaves held.
func (web *Web) readEden(user string) (edenFile, error) {
	file := edenFile{}
	path := web.edenPath(user)
	data, ok := durable.ReadStore(path)
	if !ok {
		if _, err := os.Stat(path); err == nil || !os.IsNotExist(err) {
			return file, errors.New("eden.json cannot be read")
		}
	} else if err := durable.ParseStore(path, data, &file); err != nil {
		return file, err
	}
	if file.Computers == nil {
		file.Computers = map[string]edenComputer{}
	}
	return file, nil
}

// writeEden writes eden.json of a user. With web.netSaves held.
func (web *Web) writeEden(user string, file edenFile) error {
	if err := os.MkdirAll(web.netSavesUserFolder(user), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(file)
	if err != nil {
		return err
	}
	return durable.WriteFile(web.edenPath(user), data)
}

// changeEden reads eden.json of a user, changes it and writes it, under web.netSaves.
func (web *Web) changeEden(user string, change func(*edenFile) error) error {
	web.netSaves.Lock()
	defer web.netSaves.Unlock()
	file, err := web.readEden(user)
	if err != nil {
		return err
	}
	if err := change(&file); err != nil {
		return err
	}
	return web.writeEden(user, file)
}

// edenComputerSeen notes that a computer looked at the saves (synced: that it uploaded or took
// one), written at most every edenSeenEvery when nothing moved.
func (web *Web) edenComputerSeen(user, device string, synced bool) {
	now := time.Now().Unix()
	err := web.changeEden(user, func(file *edenFile) error {
		computer := file.Computers[device]
		if !synced && now-computer.Seen < int64(edenSeenEvery.Seconds()) {
			return errEdenUnchanged
		}
		computer.Seen = now
		if synced {
			computer.Sync = now
		}
		file.Computers[device] = computer
		return nil
	})
	if err != nil && !errors.Is(err, errEdenUnchanged) {
		web.sugarLogger.Warnf("Eden saves: what %s did could not be noted: %v", device, err)
	}
}

// errEdenUnchanged tells changeEden that nothing is to be written.
var errEdenUnchanged = errors.New("unchanged")

// edenDevices are the computers of a user, by the id of their keys, with the app itself.
func (web *Web) edenDevices(user string) map[string]KeyRow {
	devices := map[string]KeyRow{edenLocalDevice: {ID: edenLocalDevice, Name: edenLocalName()}}
	for _, key := range web.auth.users.KeyRows(user) {
		if key.Role == ROLE_PC && strings.EqualFold(key.User, user) {
			devices[key.ID] = key
		}
	}
	return devices
}

// edenLocalName is how this computer is named, for the saves the app uploads of its own emulator.
func edenLocalName() string {
	if name, err := os.Hostname(); err == nil {
		if cleaned, ok := cleanAPIKeyName(name); ok {
			return cleaned
		}
	}
	return "Switch Library Manager"
}

// netSaveAccounts are the accounts of the consoles of the saves of a user, in alphabetical order.
func netSaveAccounts(saves []netSave) []string {
	seen := map[string]bool{}
	accounts := []string{}
	for _, save := range saves {
		account := strings.TrimSpace(save.Account)
		if save.Emulator || account == "" || seen[strings.ToLower(account)] {
			continue
		}
		seen[strings.ToLower(account)] = true
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool { return strings.ToLower(accounts[i]) < strings.ToLower(accounts[j]) })
	return accounts
}

// edenPairs are the pairs of a user as the device asking sees them.
func (web *Web) edenPairs(user, device string) (edensync.Pairs, error) {
	saves, err := web.netSavesOf(user, "")
	if err != nil {
		return edensync.Pairs{}, err
	}
	web.netSaves.Lock()
	file, err := web.readEden(user)
	web.netSaves.Unlock()
	if err != nil {
		return edensync.Pairs{}, err
	}
	devices := web.edenDevices(user)
	pairs := edensync.Pairs{Accounts: netSaveAccounts(saves), Profiles: []edensync.PairedProfile{}}
	for _, profile := range file.Profiles {
		computer, ok := devices[profile.Device]
		if !ok {
			continue
		}
		pairs.Profiles = append(pairs.Profiles, edensync.PairedProfile{Profile: profile.Profile, Name: profile.Name, Emulator: profile.Emulator,
			Account: profile.Account, Chosen: profile.Chosen, Computer: computer.Name, Mine: profile.Device == device})
	}
	return pairs, nil
}

// edenReportProfiles keeps the profiles of an emulator of a device, as edensync.Library has it.
func (web *Web) edenReportProfiles(user, device string, emulator edensync.Emulator, reports []edensync.ProfileReport) error {
	return web.changeEden(user, func(file *edenFile) error {
		old := map[string]edenProfile{}
		kept := []edenProfile{}
		for _, profile := range file.Profiles {
			if profile.Device == device && profile.Emulator == string(emulator) {
				old[profile.Profile] = profile
			} else {
				kept = append(kept, profile)
			}
		}
		for _, report := range reports {
			id, err := edensync.ParseProfileID(report.Profile)
			if err != nil {
				return err
			}
			profile := old[id.String()]
			profile.Device, profile.Emulator, profile.Profile, profile.Name = device, string(emulator), id.String(), netSaveText(report.Name, maxNetSaveAccount)
			if report.Account != nil {
				profile.Account, profile.Chosen = netSaveText(*report.Account, maxNetSaveAccount), true
			}
			kept = append(kept, profile)
		}
		if len(kept) > maxEdenProfiles {
			kept = kept[len(kept)-maxEdenProfiles:]
		}
		file.Profiles = kept
		return nil
	})
}

// edenReportConflict keeps a conflict told by a device: its saves are of the user, and one still
// open of the same game and profile takes them.
func (web *Web) edenReportConflict(user, device string, conflict edensync.Conflict) (edenConflict, error) {
	titleId, ok := edensync.NormalTitleID(conflict.TitleId)
	if !ok {
		return edenConflict{}, errors.New("the title ID is 16 hexadecimal digits")
	}
	profile, err := edensync.ParseProfileID(conflict.Profile)
	if err != nil {
		return edenConflict{}, err
	}
	for _, id := range []string{conflict.ConsoleSave, conflict.EmulatorSave} {
		if _, ok := web.netSaveFind(user, titleId, id); !ok {
			return edenConflict{}, errNetSaveGone
		}
	}
	kept := edenConflict{Device: device, TitleId: titleId, Profile: profile.String(), ConsoleSave: conflict.ConsoleSave, EmulatorSave: conflict.EmulatorSave, When: time.Now().Unix()}
	err = web.changeEden(user, func(file *edenFile) error {
		for i, other := range file.Conflicts {
			if other.Device == device && other.TitleId == titleId && other.Profile == kept.Profile && other.Keep == "" {
				kept.Id = other.Id
				file.Conflicts[i] = kept
				return nil
			}
		}
		kept.Id = newNetSaveId()
		file.Conflicts = append(file.Conflicts, kept)
		if len(file.Conflicts) > maxEdenConflicts {
			file.Conflicts = file.Conflicts[len(file.Conflicts)-maxEdenConflicts:]
		}
		return nil
	})
	if err != nil {
		return edenConflict{}, err
	}
	web.activities().Add(activity.Entry{Time: time.Now(), User: user, Action: activity.ACTION_SAVES_CONFLICT, Detail: web.netSaveGameName(user, titleId)})
	return kept, nil
}

// edenConflictsOf are the conflicts of a device not yet done.
func (web *Web) edenConflictsOf(user, device string) ([]edensync.Conflict, error) {
	web.netSaves.Lock()
	file, err := web.readEden(user)
	web.netSaves.Unlock()
	if err != nil {
		return nil, err
	}
	list := []edensync.Conflict{}
	for _, conflict := range file.Conflicts {
		if conflict.Device == device {
			list = append(list, conflict.shared())
		}
	}
	return list, nil
}

// edenConflictDone closes a conflict of a device, whose choice it applied.
func (web *Web) edenConflictDone(user, device, id string) error {
	return web.changeEden(user, func(file *edenFile) error {
		for i, conflict := range file.Conflicts {
			if conflict.Id == id && conflict.Device == device {
				file.Conflicts = append(file.Conflicts[:i], file.Conflicts[i+1:]...)
				return nil
			}
		}
		return errEdenConflictGone
	})
}

// newNetSaveId is a new id of 16 hexadecimal digits.
func newNetSaveId() string {
	id, _ := newAPIKeyId()
	return id
}

// netSaveGameName is the name of a game of a user as the library has it, or its title ID.
func (web *Web) netSaveGameName(user, titleId string) string {
	named := []netSave{{TitleId: titleId}}
	web.netSaveGames(named)
	if named[0].Game != "" {
		return named[0].Game
	}
	return titleId
}

// netSaveHashCache keeps the hash of the files of the saves, by their path, size and time.
var netSaveHashCache sync.Map

type netSaveHashEntry struct {
	size int64
	mod  time.Time
	hash string
}

// netSaveHashes writes the hash of the files of each save, read once.
func (web *Web) netSaveHashes(user string, saves []netSave) {
	for i := range saves {
		path := web.netSavePath(user, saves[i].TitleId, saves[i].Id)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if cached, ok := netSaveHashCache.Load(path); ok {
			if entry := cached.(netSaveHashEntry); entry.size == info.Size() && entry.mod.Equal(info.ModTime()) {
				saves[i].Hash = entry.hash
				continue
			}
		}
		hash, err := edensync.HashZipFile(path)
		if err != nil {
			continue
		}
		netSaveHashCache.Store(path, netSaveHashEntry{size: info.Size(), mod: info.ModTime(), hash: hash})
		saves[i].Hash = hash
	}
}

// the paths of the computers

func (web *Web) handlePcProfiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netPcKey(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPut {
		var body struct {
			Emulator string                   `json:"emulator"`
			Profiles []edensync.ProfileReport `json:"profiles"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the body is {\"emulator\", \"profiles\"}"})
			return
		}
		emulator, ok := edensync.ParseEmulator(body.Emulator)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the emulator is eden or citron"})
			return
		}
		if len(body.Profiles) > maxEdenProfiles {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many profiles"})
			return
		}
		if err := web.edenReportProfiles(key.Name, key.KeyID, emulator, body.Profiles); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	pairs, err := web.edenPairs(key.Name, key.KeyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the profiles cannot be read"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, pairs)
}

func (web *Web) handlePcConflictReport(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netPcKey(w, r)
	if !ok {
		return
	}
	var body edensync.Conflict
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the body is {\"titleId\", \"profile\", \"consoleSave\", \"emulatorSave\"}"})
		return
	}
	conflict, err := web.edenReportConflict(key.Name, key.KeyID, body)
	switch {
	case errors.Is(err, errNetSaveGone):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	web.sugarLogger.Infof("Eden saves: %s tells that both saves of %s changed", key.KeyName, conflict.TitleId)
	writeJSON(w, http.StatusOK, map[string]string{"id": conflict.Id})
}

func (web *Web) handlePcConflicts(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netPcKey(w, r)
	if !ok {
		return
	}
	conflicts, err := web.edenConflictsOf(key.Name, key.KeyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the conflicts cannot be read"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": conflicts})
}

func (web *Web) handlePcConflictDone(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netPcKey(w, r)
	if !ok {
		return
	}
	if err := web.edenConflictDone(key.Name, key.KeyID, mux.Vars(r)["id"]); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"done": true})
}

// the paths of the web

// handleEdenProfileChange pairs a profile of an emulator of a computer of the user signed in with
// an account of their consoles ("" for none).
func (web *Web) handleEdenProfileChange(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := web.netSaver(w, r)
	if !ok {
		return
	}
	var body struct {
		Device  string `json:"device"`
		Profile string `json:"profile"`
		Account string `json:"account"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
		return
	}
	account := netSaveText(body.Account, maxNetSaveAccount)
	if account != "" {
		saves, err := web.netSavesOf(principal.Name, "")
		if err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, "Error!")
			return
		}
		found := false
		for _, known := range netSaveAccounts(saves) {
			if known == account {
				found = true
			}
		}
		if !found {
			writeGlobalError(w, http.StatusBadRequest, lang, errEdenAccount.Error())
			return
		}
	}
	err := web.changeEden(principal.Name, func(file *edenFile) error {
		for i := range file.Profiles {
			if file.Profiles[i].Device == body.Device && file.Profiles[i].Profile == body.Profile {
				file.Profiles[i].Account, file.Profiles[i].Chosen = account, true
				return nil
			}
		}
		return errEdenProfile
	})
	if errors.Is(err, errEdenProfile) {
		writeGlobalError(w, http.StatusNotFound, lang, errEdenProfile.Error())
		return
	}
	if err != nil {
		writeGlobalError(w, http.StatusInternalServerError, lang, "Error!")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"device": body.Device, "profile": body.Profile, "account": account})
}

// handleEdenConflictDecide notes which save the emulator keeps, for a conflict of the user signed
// in: both saves stay in the library whatever is chosen.
func (web *Web) handleEdenConflictDecide(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := web.netSaver(w, r)
	if !ok {
		return
	}
	var body struct {
		Keep string `json:"keep"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || (body.Keep != edensync.KeepConsole && body.Keep != edensync.KeepEmulator) {
		writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
		return
	}
	var decided edenConflict
	err := web.changeEden(principal.Name, func(file *edenFile) error {
		for i := range file.Conflicts {
			if file.Conflicts[i].Id == mux.Vars(r)["id"] {
				file.Conflicts[i].Keep, file.Conflicts[i].Decided = body.Keep, time.Now().Unix()
				decided = file.Conflicts[i]
				return nil
			}
		}
		return errEdenConflictGone
	})
	if errors.Is(err, errEdenConflictGone) {
		writeGlobalError(w, http.StatusNotFound, lang, errEdenConflictGone.Error())
		return
	}
	if err != nil {
		writeGlobalError(w, http.StatusInternalServerError, lang, "Error!")
		return
	}
	// the source of the save kept, as the activity translates it
	source := ROLE_CONSOLE
	keptId := decided.ConsoleSave
	if body.Keep == edensync.KeepEmulator {
		keptId = decided.EmulatorSave
	}
	if save, ok := web.netSaveFind(principal.Name, decided.TitleId, keptId); ok && save.Emulator {
		source = save.Console
	}
	web.activities().Add(activity.Entry{Time: time.Now(), User: principal.Name, Action: activity.ACTION_SAVES_CHOICE,
		Detail: web.netSaveGameName(principal.Name, decided.TitleId) + " + " + source, IP: clientIp(r)})
	if decided.Device == edenLocalDevice {
		web.edenLocalWake()
	}
	writeJSON(w, http.StatusOK, decided.shared())
}

// handleEdenSaves adds the paths of the computers and of the web.
func (web *Web) handleEdenSaves() {
	web.router.HandleFunc(netInstallSavesProfiles, web.handlePcProfiles).Methods(http.MethodGet, http.MethodPut)
	web.router.HandleFunc(netInstallSavesConflicts, web.handlePcConflicts).Methods(http.MethodGet)
	web.router.HandleFunc(netInstallSavesConflicts, web.handlePcConflictReport).Methods(http.MethodPost)
	web.router.HandleFunc(netInstallSavesConflicts+"/{id:[0-9a-f]{16}}/done", web.handlePcConflictDone).Methods(http.MethodPost)
	web.router.HandleFunc(netSavesApi+"/profiles", web.handleEdenProfileChange).Methods(http.MethodPost)
	web.router.HandleFunc(netSavesApi+"/conflicts/{id:[0-9a-f]{16}}", web.handleEdenConflictDecide).Methods(http.MethodPost)
	web.router.HandleFunc(netSavesApi+"/eden-local", web.handleEdenLocalSwitch).Methods(http.MethodPost)
}

// the page

// EdenSection is the part Eden on your computers of the page Saves.
type EdenSection struct {
	Computers []EdenComputerRow
	Profiles  []EdenProfileRow
	Accounts  []string
	Conflicts []EdenConflictRow
	// the emulator of this computer, when the app runs on it; nil otherwise
	Local *EdenLocalRow
}

// EdenComputerRow is a computer paired.
type EdenComputerRow struct {
	Name       string
	Seen, Sync time.Time
}

// EdenProfileRow is a profile of an emulator of a computer, and the accounts of the consoles it
// may go with: the ones known, and the one it has.
type EdenProfileRow struct {
	Device, Profile, Name, Emulator, Computer, Account string
	Choices                                            []string
}

// EdenConflictRow is a game whose two saves changed, and the two saves.
type EdenConflictRow struct {
	Id, TitleId, Game string
	Saves             []EdenConflictSave
}

// EdenConflictSave is one of the two saves of a conflict.
type EdenConflictSave struct {
	Keep     string
	Source   string
	Emulator bool
	Time     time.Time
	Size     int64
	Found    bool
}

// edenSection is the part Eden on your computers of the page of a user.
func (web *Web) edenSection(user string, saves []netSave) EdenSection {
	section := EdenSection{Accounts: netSaveAccounts(saves)}
	web.netSaves.Lock()
	file, err := web.readEden(user)
	web.netSaves.Unlock()
	if err != nil {
		return section
	}
	devices := web.edenDevices(user)
	for _, key := range web.auth.users.KeyRows(user) {
		if key.Role != ROLE_PC || !strings.EqualFold(key.User, user) {
			continue
		}
		computer := file.Computers[key.ID]
		row := EdenComputerRow{Name: key.Name, Seen: key.LastUsed}
		if seen := time.Unix(computer.Seen, 0); computer.Seen > 0 && seen.After(row.Seen) {
			row.Seen = seen
		}
		if computer.Sync > 0 {
			row.Sync = time.Unix(computer.Sync, 0)
		}
		section.Computers = append(section.Computers, row)
	}
	for _, profile := range file.Profiles {
		device, ok := devices[profile.Device]
		if !ok {
			continue
		}
		emulator, _ := edensync.ParseEmulator(profile.Emulator)
		choices := section.Accounts
		if profile.Account != "" && !slices.Contains(choices, profile.Account) {
			choices = append(slices.Clone(choices), profile.Account)
		}
		section.Profiles = append(section.Profiles, EdenProfileRow{Device: profile.Device, Profile: profile.Profile, Name: profile.Name,
			Emulator: emulator.Name(), Computer: device.Name, Account: profile.Account, Choices: choices})
	}
	byId := map[string]netSave{}
	for _, save := range saves {
		byId[save.TitleId+"/"+save.Id] = save
	}
	for _, conflict := range file.Conflicts {
		if _, ok := devices[conflict.Device]; !ok || conflict.Keep != "" {
			continue
		}
		row := EdenConflictRow{Id: conflict.Id, TitleId: conflict.TitleId, Game: web.netSaveGameName(user, conflict.TitleId)}
		for _, side := range []struct{ keep, id string }{{edensync.KeepEmulator, conflict.EmulatorSave}, {edensync.KeepConsole, conflict.ConsoleSave}} {
			save, found := byId[conflict.TitleId+"/"+side.id]
			row.Saves = append(row.Saves, EdenConflictSave{Keep: side.keep, Found: found, Emulator: save.Emulator, Size: save.Size, Time: time.Unix(save.When, 0), Source: save.Console})
		}
		section.Conflicts = append(section.Conflicts, row)
	}
	section.Local = web.edenLocalRow(user, file)
	return section
}
