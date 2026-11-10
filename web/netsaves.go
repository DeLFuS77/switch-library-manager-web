package web

import (
	"archive/zip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/diskfree"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/gorilla/mux"
)

// The saves NXDock uploads from the console, kept for the user who owns the key of the console.
//
//   - POST /install/saves/{titleId} takes a save as a ZIP (its files as they are, with relative
//     paths), with ?note=, ?version= (the version of the game installed) and ?account= (the
//     profile of the console). It answers {"id", "when", "size"}.
//   - GET /install/saves (?title= for one game) lists the saves of the user, newest first, and
//     GET /install/saves/{titleId}/{id} gives one back, as the ZIP it was.
//   - On the web, the page Saves (/saves) lists them for the user signed in, who writes a note,
//     pins, downloads and deletes them (/api/saves).
//
// They live in a folder of the data folder of their own, one per user: not in the one of the
// save vault of JKSV (saves/), whose WebDAV share reads every file under it with the user and
// password of JKSV, and whose page lists them all for the administrators.

const (
	netInstallSaves = NET_INSTALL_PREFIX + "saves"
	// the paths of the web
	netSavesPage = "/saves"
	netSavesApi  = "/api/saves"

	// the folder of the data folder with the saves, a folder per user, and the index of each
	netSavesFolder = "console-saves"
	netSavesIndex  = "index.json"
	// a save is rarely bigger than a few hundred megabytes
	netSaveMaxSize = 1 << 30
	// the saves kept per game: past that, the oldest that are not pinned go
	netSavesKeep = 10
	// the longest note and name of a profile of the console
	maxNetSaveNote    = 200
	maxNetSaveAccount = 64
	// what is left free on the disk after a save at least
	netSaveMinFree = 64 << 20
)

var (
	netSaveIdPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)
	// the path a console uploads a save to
	netSaveUploadPattern = regexp.MustCompile(`^` + netInstallSaves + `/[0-9A-Fa-f]{16}$`)
)

// netSaveUploadPath tells the path of the upload of a save, which a console may ask.
func netSaveUploadPath(path string) bool {
	return netSaveUploadPattern.MatchString(path)
}

var (
	errNetSaveNotZip = errors.New("the save is not a ZIP with relative paths")
	errNetSaveSpace  = errors.New("not enough free space for the save")
	// told on the web
	errNetSaveNotAllow = errors.New("only a user who may download has saves")
	errNetSaveGone     = errors.New("this save is no longer kept")
)

// netSave is a save kept, as the index has it and as it is listed.
type netSave struct {
	TitleId string `json:"title_id"`
	Game    string `json:"game"`
	Id      string `json:"id"`
	When    int64  `json:"when"`
	Note    string `json:"note"`
	Version int    `json:"version"`
	Account string `json:"account"`
	Console string `json:"console"`
	Size    int64  `json:"size"`
	Pinned  bool   `json:"pinned"`
}

// netSavesFile is the index of the saves of a user.
type netSavesFile struct {
	Saves []netSave `json:"saves"`
}

// netSavesUserFolder is the folder of the saves of a user: their name as a file system takes it,
// and a hash of it, so two names that are cleaned alike never share a folder (and no name is
// one Windows keeps for itself, such as CON).
func (web *Web) netSavesUserFolder(user string) string {
	lower := strings.ToLower(strings.TrimSpace(user))
	sum := sha256.Sum256([]byte(lower))
	name := netSafeName(lower)
	if len(name) > 40 {
		name = string([]rune(name)[:40])
	}
	return filepath.Join(web.dataFolder, netSavesFolder, strings.Trim(name+"-"+hex.EncodeToString(sum[:4]), "-. "))
}

// netSavePath is the file of a save.
func (web *Web) netSavePath(user string, titleId string, id string) string {
	return filepath.Join(web.netSavesUserFolder(user), titleId, id+".zip")
}

// readNetSaves reads the index of the saves of a user. With web.netSaves held.
func (web *Web) readNetSaves(user string) ([]netSave, error) {
	path := filepath.Join(web.netSavesUserFolder(user), netSavesIndex)
	data, ok := durable.ReadStore(path)
	if !ok {
		if _, err := os.Stat(path); err == nil || !os.IsNotExist(err) {
			return nil, errors.New("the index of the saves cannot be read")
		}
		return []netSave{}, nil
	}
	index := netSavesFile{}
	if err := durable.ParseStore(path, data, &index); err != nil {
		return nil, err
	}
	if index.Saves == nil {
		index.Saves = []netSave{}
	}
	return index.Saves, nil
}

// writeNetSaves writes the index of the saves of a user through a temporary file. With
// web.netSaves held.
func (web *Web) writeNetSaves(user string, saves []netSave) error {
	folder := web.netSavesUserFolder(user)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return err
	}
	for i := range saves {
		// the name of the game is the one of the library when it is listed, never kept
		saves[i].Game = ""
	}
	data, err := json.Marshal(netSavesFile{Saves: saves})
	if err != nil {
		return err
	}
	return durable.WriteFile(filepath.Join(folder, netSavesIndex), data)
}

// netSaveText is a text of a save on one line, at most so many characters.
func netSaveText(text string, most int) string {
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > most {
		text = strings.TrimSpace(string(runes[:most]))
	}
	return text
}

// checkNetSaveZip tells whether a file is a ZIP whose names are all relative, inside it.
func checkNetSaveZip(path string) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return errNetSaveNotZip
	}
	defer archive.Close()
	if len(archive.File) == 0 {
		return errNetSaveNotZip
	}
	for _, file := range archive.File {
		name := strings.ReplaceAll(file.Name, `\`, "/")
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(strings.SplitN(name, "/", 2)[0], ":") {
			return errNetSaveNotZip
		}
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				return errNetSaveNotZip
			}
		}
	}
	return nil
}

// pruneNetSaves keeps netSavesKeep saves of a game, deleting the oldest that are not pinned and
// never the one just kept, and gives the saves left.
func (web *Web) pruneNetSaves(user string, saves []netSave, titleId string, keep string) []netSave {
	ofGame := []int{}
	for i, save := range saves {
		if save.TitleId == titleId {
			ofGame = append(ofGame, i)
		}
	}
	extra := len(ofGame) - netSavesKeep
	if extra <= 0 {
		return saves
	}
	// oldest first
	sort.SliceStable(ofGame, func(a, b int) bool { return saves[ofGame[a]].When < saves[ofGame[b]].When })
	gone := map[int]bool{}
	for _, i := range ofGame {
		if extra == 0 {
			break
		}
		if saves[i].Pinned || saves[i].Id == keep {
			continue
		}
		if err := os.Remove(web.netSavePath(user, titleId, saves[i].Id)); err != nil && !os.IsNotExist(err) {
			web.sugarLogger.Warnf("NXDock saves: %s of %s could not be deleted: %v", saves[i].Id, titleId, err)
			continue
		}
		gone[i] = true
		extra--
	}
	kept := saves[:0]
	for i, save := range saves {
		if !gone[i] {
			kept = append(kept, save)
		}
	}
	return kept
}

// netSaveGames names the games of the saves as the library has them.
func (web *Web) netSaveGames(saves []netSave) {
	switchDB, localDB := web.state.get()
	names := map[string]string{}
	for i, save := range saves {
		name, ok := names[save.TitleId]
		if !ok {
			if localDB != nil {
				if title, found := localDB.TitlesMap[netGamePrefix(save.TitleId)]; found && title != nil {
					var known *db.SwitchTitle
					if switchDB != nil {
						known = switchDB.TitlesMap[netGamePrefix(save.TitleId)]
					}
					if title.File.Metadata != nil {
						name = getLocalTitleName(known, title)
					}
					if name == "" {
						name = strings.TrimSpace(db.ParseTitleNameFromFileName(title.File.ExtendedInfo.FileName))
					}
				}
			}
			names[save.TitleId] = name
		}
		saves[i].Game = name
	}
}

// netSavesOf lists the saves of a user, newest first, of one game when titleId is not "".
func (web *Web) netSavesOf(user string, titleId string) ([]netSave, error) {
	web.netSaves.Lock()
	saves, err := web.readNetSaves(user)
	web.netSaves.Unlock()
	if err != nil {
		return nil, err
	}
	// the index is in the order they came: read from its end, two saves of the same second are
	// listed the newest first too
	listed := []netSave{}
	for i := len(saves) - 1; i >= 0; i-- {
		if titleId == "" || saves[i].TitleId == titleId {
			listed = append(listed, saves[i])
		}
	}
	sort.SliceStable(listed, func(i, j int) bool { return listed[i].When > listed[j].When })
	web.netSaveGames(listed)
	return listed, nil
}

// netSaveFind is a save of a user, by its game and id.
func (web *Web) netSaveFind(user string, titleId string, id string) (netSave, bool) {
	if !netTitleIdPattern.MatchString(titleId) || !netSaveIdPattern.MatchString(id) {
		return netSave{}, false
	}
	saves, err := web.netSavesOf(user, titleId)
	if err != nil {
		return netSave{}, false
	}
	for _, save := range saves {
		if save.Id == id {
			return save, true
		}
	}
	return netSave{}, false
}

// serveNetSave gives a save back, as the ZIP it was uploaded.
func (web *Web) serveNetSave(w http.ResponseWriter, r *http.Request, user string) {
	titleId, id := strings.ToUpper(mux.Vars(r)["titleId"]), mux.Vars(r)["id"]
	save, ok := web.netSaveFind(user, titleId, id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(web.netSavePath(user, titleId, id))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name := titleId + " " + time.Unix(save.When, 0).UTC().Format("2006-01-02 150405") + ".zip"
	if game := netSafeName(save.Game); game != "" {
		name = game + " " + name
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", stat.ModTime(), file)
}

// handleNetSaveUpload keeps a save sent by a console, for the user who owns its key.
func (web *Web) handleNetSaveUpload(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netConsoleKey(w, r)
	if !ok {
		return
	}
	titleId := strings.ToUpper(mux.Vars(r)["titleId"])
	query := r.URL.Query()
	version := 0
	if value := strings.TrimSpace(query.Get("version")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the version is a number"})
			return
		}
		version = parsed
	}
	if r.ContentLength > netSaveMaxSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "a save is 1 GiB at most"})
		return
	}
	folder := filepath.Join(web.netSavesUserFolder(key.Name), titleId)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		web.sugarLogger.Warnf("NXDock saves: the folder of %s could not be made: %v", titleId, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the save could not be kept"})
		return
	}
	// a save of the size it tells, or of the most a save is when it does not, fits on the disk
	need := r.ContentLength
	if need < 0 {
		need = 0
	}
	if free, err := diskfree.Free(folder); err == nil && int64(free) < need+netSaveMinFree {
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": errNetSaveSpace.Error()})
		return
	}
	// a save over the Wi-Fi of the console takes a while
	http.NewResponseController(w).SetReadDeadline(time.Time{})
	temporary, err := os.CreateTemp(folder, ".upload-*.tmp")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the save could not be kept"})
		return
	}
	defer os.Remove(temporary.Name())
	size, err := io.Copy(temporary, http.MaxBytesReader(w, r.Body, netSaveMaxSize))
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "a save is 1 GiB at most"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the save was cut short"})
		return
	}
	if err := checkNetSaveZip(temporary.Name()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	random := make([]byte, 8)
	rand.Read(random)
	save := netSave{TitleId: titleId, Id: hex.EncodeToString(random), When: time.Now().Unix(), Version: version, Size: size,
		Note: netSaveText(query.Get("note"), maxNetSaveNote), Account: netSaveText(query.Get("account"), maxNetSaveAccount), Console: key.KeyName}
	web.netSaves.Lock()
	defer web.netSaves.Unlock()
	saves, err := web.readNetSaves(key.Name)
	if err == nil {
		err = durable.CommitFile(temporary.Name(), web.netSavePath(key.Name, titleId, save.Id))
	}
	if err == nil {
		saves = web.pruneNetSaves(key.Name, append(saves, save), titleId, save.Id)
		if err = web.writeNetSaves(key.Name, saves); err != nil {
			os.Remove(web.netSavePath(key.Name, titleId, save.Id))
		}
	}
	if err != nil {
		web.sugarLogger.Warnf("NXDock saves: the save of %s from %s could not be kept: %v", titleId, key.KeyName, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the save could not be kept"})
		return
	}
	web.sugarLogger.Infof("NXDock saves: %s uploaded a save of %s for %s (%d bytes)", key.KeyName, titleId, key.Name, size)
	writeJSON(w, http.StatusOK, map[string]any{"id": save.Id, "when": save.When, "size": save.Size})
}

// handleNetSaveList lists the saves of the user of the console, newest first.
func (web *Web) handleNetSaveList(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netConsoleKey(w, r)
	if !ok {
		return
	}
	web.writeNetSaveList(w, r, key.Name)
}

// writeNetSaveList answers with the saves of a user, of the game of ?title= when it is given.
func (web *Web) writeNetSaveList(w http.ResponseWriter, r *http.Request, user string) {
	titleId := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("title")))
	if titleId != "" && !netTitleIdPattern.MatchString(titleId) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the title ID is 16 hexadecimal digits"})
		return
	}
	saves, err := web.netSavesOf(user, titleId)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the saves cannot be read"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"saves": saves})
}

// handleNetSaveDownload gives a save back to the console.
func (web *Web) handleNetSaveDownload(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	key, ok := netConsoleKey(w, r)
	if !ok {
		return
	}
	web.serveNetSave(w, r, key.Name)
}

// netSaver is the user of a request of the web who may have saves: one who may download.
func (web *Web) netSaver(w http.ResponseWriter, r *http.Request) (*Principal, bool) {
	principal, ok := netPairer(r)
	if !ok {
		writeGlobalError(w, http.StatusForbidden, web.requestLanguage(r), errNetSaveNotAllow.Error())
	}
	return principal, ok
}

// handleNetSavesApi lists the saves of the user signed in.
func (web *Web) handleNetSavesApi(w http.ResponseWriter, r *http.Request) {
	principal, ok := web.netSaver(w, r)
	if !ok {
		return
	}
	web.writeNetSaveList(w, r, principal.Name)
}

// handleNetSaveGet gives a save of the user signed in.
func (web *Web) handleNetSaveGet(w http.ResponseWriter, r *http.Request) {
	principal, ok := web.netSaver(w, r)
	if !ok {
		return
	}
	web.serveNetSave(w, r, principal.Name)
}

// handleNetSaveChange writes the note of a save of the user signed in, or pins it.
func (web *Web) handleNetSaveChange(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := web.netSaver(w, r)
	if !ok {
		return
	}
	var body struct {
		Note   *string `json:"note"`
		Pinned *bool   `json:"pinned"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
		return
	}
	titleId, id := strings.ToUpper(mux.Vars(r)["titleId"]), mux.Vars(r)["id"]
	web.netSaves.Lock()
	defer web.netSaves.Unlock()
	saves, err := web.readNetSaves(principal.Name)
	if err != nil {
		writeGlobalError(w, http.StatusInternalServerError, lang, "Error!")
		return
	}
	for i := range saves {
		if saves[i].TitleId != titleId || saves[i].Id != id {
			continue
		}
		if body.Note != nil {
			saves[i].Note = netSaveText(*body.Note, maxNetSaveNote)
		}
		if body.Pinned != nil {
			saves[i].Pinned = *body.Pinned
		}
		changed := saves[i]
		if err := web.writeNetSaves(principal.Name, saves); err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, "Error!")
			return
		}
		listed := []netSave{changed}
		web.netSaveGames(listed)
		changed = listed[0]
		writeJSON(w, http.StatusOK, changed)
		return
	}
	writeGlobalError(w, http.StatusNotFound, lang, errNetSaveGone.Error())
}

// handleNetSaveDelete deletes a save of the user signed in.
func (web *Web) handleNetSaveDelete(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := web.netSaver(w, r)
	if !ok {
		return
	}
	titleId, id := strings.ToUpper(mux.Vars(r)["titleId"]), mux.Vars(r)["id"]
	web.netSaves.Lock()
	defer web.netSaves.Unlock()
	saves, err := web.readNetSaves(principal.Name)
	if err != nil {
		writeGlobalError(w, http.StatusInternalServerError, lang, "Error!")
		return
	}
	for i, save := range saves {
		if save.TitleId != titleId || save.Id != id {
			continue
		}
		path := web.netSavePath(principal.Name, titleId, id)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			writeGlobalError(w, http.StatusInternalServerError, lang, "Error!")
			return
		}
		saves = append(saves[:i], saves[i+1:]...)
		if err := web.writeNetSaves(principal.Name, saves); err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, "Error!")
			return
		}
		// the folder of a game without saves goes too
		os.Remove(filepath.Dir(path))
		web.sugarLogger.Infof("NXDock saves: %s deleted the save %s of %s", principal.Name, id, titleId)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
		return
	}
	writeGlobalError(w, http.StatusNotFound, lang, errNetSaveGone.Error())
}

// handleNetSaves adds the paths of the saves, of the console and of the web.
func (web *Web) handleNetSaves() {
	saveOf := "/{titleId:[0-9A-Fa-f]{16}}/{id:[0-9a-f]{16}}"
	web.router.HandleFunc(netInstallSaves, web.handleNetSaveList).Methods(http.MethodGet)
	web.router.HandleFunc(netInstallSaves+"/{titleId:[0-9A-Fa-f]{16}}", web.handleNetSaveUpload).Methods(http.MethodPost)
	web.router.HandleFunc(netInstallSaves+saveOf, web.handleNetSaveDownload).Methods(http.MethodGet, http.MethodHead)
	web.router.HandleFunc(netSavesApi, web.handleNetSavesApi).Methods(http.MethodGet)
	web.router.HandleFunc(netSavesApi+saveOf, web.handleNetSaveGet).Methods(http.MethodGet, http.MethodHead)
	web.router.HandleFunc(netSavesApi+saveOf, web.handleNetSaveChange).Methods(http.MethodPatch)
	web.router.HandleFunc(netSavesApi+saveOf, web.handleNetSaveDelete).Methods(http.MethodDelete)
}

// NetSavesPageData is the page Saves: the saves of the user signed in, by game.
type NetSavesPageData struct {
	GlobalPageData
	Groups []NetSaveGroup
	// all the saves, and the room they take
	Total int
	Size  int64
}

// NetSaveGroup is a game and its saves, newest first.
type NetSaveGroup struct {
	TitleId string
	Game    string
	Saves   []NetSaveRow
}

// NetSaveRow is a save as the page shows it.
type NetSaveRow struct {
	netSave
	Time time.Time
}

// netSaveGroups puts the saves, newest first, in groups by game: the game saved last first.
func netSaveGroups(saves []netSave) []NetSaveGroup {
	groups := []NetSaveGroup{}
	at := map[string]int{}
	for _, save := range saves {
		i, ok := at[save.TitleId]
		if !ok {
			i = len(groups)
			at[save.TitleId] = i
			groups = append(groups, NetSaveGroup{TitleId: save.TitleId, Game: save.Game})
		}
		groups[i].Saves = append(groups[i].Saves, NetSaveRow{netSave: save, Time: time.Unix(save.When, 0)})
	}
	return groups
}

// HandleNetSavesPage adds the page Saves, for the users who may download.
func (web *Web) HandleNetSavesPage() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/netsaves.html")
	web.router.HandleFunc(netSavesPage, func(w http.ResponseWriter, r *http.Request) {
		data := NetSavesPageData{GlobalPageData: web.globalPageData("netsaves"), Groups: []NetSaveGroup{}}
		if principal, ok := netPairer(r); ok {
			if saves, err := web.netSavesOf(principal.Name, ""); err == nil {
				data.Groups = netSaveGroups(saves)
				for _, save := range saves {
					data.Total++
					data.Size += save.Size
				}
			}
		}
		web.render(w, r, templates, data)
	}).Methods(http.MethodGet)
}
