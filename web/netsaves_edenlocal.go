package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/edensync"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
)

// Eden on this computer: when the app runs on the computer where Eden (or Citron) is, never in a
// container, it does what NXPress does for a computer paired, for the user who turned it on in
// the page Saves: every minute and when it starts, only while the emulator is closed, the new
// saves of the emulator go to the library and the newer ones of the library go into the
// emulator, a copy of the save replaced always staying in the library, and the conflicts are
// asked on the page. It uses the storage of the saves itself, as the device "local".

const (
	// in the folder of the saves: the user Eden of this computer is kept in step with
	edenLocalFile = "eden-local.json"
	// in the folder of the saves of that user: what the passes remember
	edenLocalStateFile = "eden-local-state.json"
	edenLocalEvery     = time.Minute
)

var (
	errEdenLocalMissing = errors.New("no emulator was found on this computer")
	errEdenLocalTaken   = errors.New("the emulator of this computer is kept in step with the saves of another user")
)

// edenLocalSetting is who Eden of this computer is kept in step with: "" for nobody.
type edenLocalSetting struct {
	User string `json:"user"`
}

// edenLocalRunner runs the passes of the emulator of this computer.
type edenLocalRunner struct {
	// one pass at a time
	mutex    sync.Mutex
	wakeOnce sync.Once
	wake     chan struct{}
	// where the emulator is, and whether it is open: replaced in the tests
	find    func() (edensync.Folder, bool)
	running func() (bool, error)
}

func (r *edenLocalRunner) wakeChannel() chan struct{} {
	r.wakeOnce.Do(func() { r.wake = make(chan struct{}, 1) })
	return r.wake
}

// edenLocalFolder is the folder of the emulator of this computer, when the app runs on one that
// has it and not in a container.
func (web *Web) edenLocalFolder() (edensync.Folder, bool) {
	if web.edenLocal.find != nil {
		return web.edenLocal.find()
	}
	if runningInContainer() || isDemoMode() {
		return edensync.Folder{}, false
	}
	found := edensync.FindAll()
	if len(found) == 0 {
		return edensync.Folder{}, false
	}
	return found[0], true
}

func (web *Web) edenLocalSettingPath() string {
	return filepath.Join(web.dataFolder, netSavesFolder, edenLocalFile)
}

func (web *Web) readEdenLocal() edenLocalSetting {
	setting := edenLocalSetting{}
	path := web.edenLocalSettingPath()
	if data, ok := durable.ReadStore(path); ok {
		durable.ParseStore(path, data, &setting)
	}
	return setting
}

func (web *Web) writeEdenLocal(setting edenLocalSetting) error {
	if err := os.MkdirAll(filepath.Dir(web.edenLocalSettingPath()), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(setting)
	if err != nil {
		return err
	}
	return durable.WriteFile(web.edenLocalSettingPath(), data)
}

// EdenLocalRow is the emulator of this computer, as the page Saves shows it.
type EdenLocalRow struct {
	Emulator string
	Folder   string
	// kept in step with the saves of the user looking, or of another user
	On, Other  bool
	Seen, Sync time.Time
}

// edenLocalRow is the emulator of this computer for a user, nil when the app does not run on a
// computer with one.
func (web *Web) edenLocalRow(user string, file edenFile) *EdenLocalRow {
	folder, ok := web.edenLocalFolder()
	if !ok {
		return nil
	}
	setting := web.readEdenLocal()
	row := &EdenLocalRow{Emulator: folder.Emulator.Name(), Folder: folder.Root}
	row.On = setting.User != "" && strings.EqualFold(setting.User, user)
	row.Other = setting.User != "" && !row.On
	if computer := file.Computers[edenLocalDevice]; row.On {
		if computer.Seen > 0 {
			row.Seen = time.Unix(computer.Seen, 0)
		}
		if computer.Sync > 0 {
			row.Sync = time.Unix(computer.Sync, 0)
		}
	}
	return row
}

// handleEdenLocalSwitch turns on or off the passes of the emulator of this computer, for the user
// signed in: {"on": true}. Another user's are turned off only by them or an administrator.
func (web *Web) handleEdenLocalSwitch(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := web.netSaver(w, r)
	if !ok {
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
		return
	}
	if _, found := web.edenLocalFolder(); !found {
		writeGlobalError(w, http.StatusNotFound, lang, errEdenLocalMissing.Error())
		return
	}
	setting := web.readEdenLocal()
	mine := strings.EqualFold(setting.User, principal.Name)
	switch {
	case body.On && setting.User != "" && !mine:
		writeGlobalError(w, http.StatusConflict, lang, errEdenLocalTaken.Error())
		return
	case !body.On && setting.User != "" && !mine && !principal.IsAdmin():
		writeGlobalError(w, http.StatusConflict, lang, errEdenLocalTaken.Error())
		return
	}
	setting.User = ""
	if body.On {
		setting.User = principal.Name
	}
	if err := web.writeEdenLocal(setting); err != nil {
		writeGlobalError(w, http.StatusInternalServerError, lang, "Error!")
		return
	}
	web.sugarLogger.Infof("Eden saves: %s turned %s the saves of Eden on this computer", principal.Name, map[bool]string{true: "on", false: "off"}[body.On])
	if body.On {
		web.edenLocalWake()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"on": body.On})
}

// edenLocalWake asks for a pass now, as when a choice of a conflict was made.
func (web *Web) edenLocalWake() {
	select {
	case web.edenLocal.wakeChannel() <- struct{}{}:
	default:
	}
}

// StartEdenLocal runs the passes of the emulator of this computer: when the app starts, every
// minute, and when asked. Never in a container.
func (web *Web) StartEdenLocal() {
	if runningInContainer() || isDemoMode() {
		return
	}
	wake := web.edenLocal.wakeChannel()
	go func() {
		ticker := time.NewTicker(edenLocalEvery)
		defer ticker.Stop()
		for {
			web.edenLocalPass()
			select {
			case <-ticker.C:
			case <-wake:
			}
		}
	}()
}

// edenLocalPass is one pass of the emulator of this computer, for the user who turned it on.
func (web *Web) edenLocalPass() (edensync.Report, error) {
	setting := web.readEdenLocal()
	if setting.User == "" {
		return edensync.Report{}, nil
	}
	user, ok := web.auth.users.Get(setting.User)
	if !ok || roleRank(user.Role) < roleRank(ROLE_VIEWER) {
		return edensync.Report{}, nil
	}
	folder, ok := web.edenLocalFolder()
	if !ok {
		return edensync.Report{}, nil
	}
	runner := &web.edenLocal
	runner.mutex.Lock()
	defer runner.mutex.Unlock()
	statePath := filepath.Join(web.netSavesUserFolder(user.Name), edenLocalStateFile)
	state, err := edensync.LoadState(statePath)
	if err != nil {
		web.sugarLogger.Warnf("Eden saves: what the passes remember could not be read, they start again: %v", err)
		state = edensync.NewState()
	}
	syncer := &edensync.Syncer{Folder: folder, Library: &edenLocalLibrary{web: web, user: user.Name}, State: state, StatePath: statePath, IsRunning: runner.running}
	report, err := syncer.Pass(context.Background())
	if err != nil {
		web.sugarLogger.Warnf("Eden saves: the saves of %s on this computer could not be looked at: %v", folder.Emulator.Name(), err)
		return report, err
	}
	if report.Running {
		return report, nil
	}
	for _, failed := range report.Errors {
		web.sugarLogger.Warnf("Eden saves: %v", failed)
	}
	moved := len(report.Uploaded) + len(report.Brought)
	if moved > 0 || len(report.Conflicts) > 0 {
		web.sugarLogger.Infof("Eden saves: %d uploaded, %d brought into %s, %d to choose", len(report.Uploaded), len(report.Brought), folder.Emulator.Name(), len(report.Conflicts))
	}
	web.edenComputerSeen(user.Name, edenLocalDevice, moved > 0)
	return report, nil
}

// edenLocalLibrary is the library of a user as the passes of this computer see it: the storage of
// the saves itself.
type edenLocalLibrary struct {
	web  *Web
	user string
}

// netSaveShared is a save as the package edensync lists it.
func netSaveShared(save netSave) edensync.LibrarySave {
	return edensync.LibrarySave{TitleId: save.TitleId, Game: save.Game, Id: save.Id, When: save.When, Note: save.Note, Version: save.Version, Account: save.Account,
		Console: save.Console, Size: save.Size, Pinned: save.Pinned, Emulator: save.Emulator, Profile: save.Profile, Device: save.Device, Auto: save.Auto, Hash: save.Hash}
}

func (l *edenLocalLibrary) Saves(ctx context.Context) ([]edensync.LibrarySave, error) {
	saves, err := l.web.netSavesOf(l.user, "")
	if err != nil {
		return nil, err
	}
	l.web.netSaveHashes(l.user, saves)
	list := make([]edensync.LibrarySave, len(saves))
	for i, save := range saves {
		list[i] = netSaveShared(save)
	}
	return list, nil
}

func (l *edenLocalLibrary) Download(ctx context.Context, titleId, id string, out io.Writer) error {
	if _, ok := l.web.netSaveFind(l.user, titleId, id); !ok {
		return errNetSaveGone
	}
	file, err := os.Open(l.web.netSavePath(l.user, titleId, id))
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(out, file)
	return err
}

func (l *edenLocalLibrary) Upload(ctx context.Context, upload edensync.Upload) (edensync.LibrarySave, error) {
	titleId, ok := edensync.NormalTitleID(upload.TitleId)
	if !ok {
		return edensync.LibrarySave{}, errors.New("the title ID is 16 hexadecimal digits")
	}
	folder := filepath.Join(l.web.netSavesUserFolder(l.user), titleId)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return edensync.LibrarySave{}, err
	}
	temporary, err := os.CreateTemp(folder, ".upload-*.tmp")
	if err != nil {
		return edensync.LibrarySave{}, err
	}
	defer os.Remove(temporary.Name())
	size, err := io.Copy(temporary, io.LimitReader(upload.Body, netSaveMaxSize+1))
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil && size > netSaveMaxSize {
		err = errors.New("a save is 1 GiB at most")
	}
	if err == nil {
		err = checkNetSaveZip(temporary.Name())
	}
	if err != nil {
		return edensync.LibrarySave{}, err
	}
	save := netSave{TitleId: titleId, Size: size, Emulator: true, Console: upload.Emulator.Name(), Account: netSaveText(upload.Profile.Name, maxNetSaveAccount),
		Profile: upload.Profile.ID.String(), Device: edenLocalName(), Auto: upload.Auto}
	save, err = l.web.keepNetSave(l.user, temporary.Name(), save)
	if err != nil {
		return edensync.LibrarySave{}, err
	}
	return netSaveShared(save), nil
}

func (l *edenLocalLibrary) Pairs(ctx context.Context) (edensync.Pairs, error) {
	return l.web.edenPairs(l.user, edenLocalDevice)
}

func (l *edenLocalLibrary) ReportProfiles(ctx context.Context, emulator edensync.Emulator, profiles []edensync.ProfileReport) (edensync.Pairs, error) {
	if err := l.web.edenReportProfiles(l.user, edenLocalDevice, emulator, profiles); err != nil {
		return edensync.Pairs{}, err
	}
	return l.web.edenPairs(l.user, edenLocalDevice)
}

func (l *edenLocalLibrary) ReportConflict(ctx context.Context, conflict edensync.Conflict) (string, error) {
	kept, err := l.web.edenReportConflict(l.user, edenLocalDevice, conflict)
	return kept.Id, err
}

func (l *edenLocalLibrary) Conflicts(ctx context.Context) ([]edensync.Conflict, error) {
	return l.web.edenConflictsOf(l.user, edenLocalDevice)
}

func (l *edenLocalLibrary) ConflictDone(ctx context.Context, id string) error {
	return l.web.edenConflictDone(l.user, edenLocalDevice, id)
}
