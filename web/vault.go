package web

import (
	"archive/zip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/webdav"
)

// The save vault keeps the save data backups of the console. JKSV, the save manager of the
// Switch, uploads them over WebDAV: the app answers at /dav/ with its own user and password
// (not those of the web interface), off until it is turned on in Settings. JKSV makes a folder
// per game and uploads a ZIP per backup, with a small file inside that tells the title ID and
// the time, so every backup is matched to its game of the library.

const (
	VAULT_PREFIX = "/dav"
	// the folder of the data folder that holds the backups
	VAULT_FOLDER = "saves"
	// the folder JKSV is told to use ("basepath" of its webdav.json)
	VAULT_BASE = "JKSV"
	// a save backup is rarely bigger than a few hundred megabytes
	maxSaveBody = 4 << 30
	// the file JKSV writes inside each ZIP, and its first bytes ("JKSV")
	jksvMetaName  = ".nx_save_meta.bin"
	jksvMetaMagic = 0x56534B4A
	// a verified password is remembered for a while: JKSV sends it with every request
	vaultAuthCache = 10 * time.Minute
	vaultPassCost  = 10
)

// allowed numbers of backups kept per game; 0 keeps them all
var allowedVaultKeep = map[int]struct{}{0: {}, 5: {}, 10: {}, 20: {}, 50: {}}

// vaultRoot is the folder of the backups.
func (web *Web) vaultRoot() string {
	return filepath.Join(web.dataFolder, VAULT_FOLDER)
}

// vaultEnabled reports whether the vault answers: turned on, with a user and a password.
func vaultEnabled(s *settings.AppSettings) bool {
	return s.Vault.Enabled && s.Vault.User != "" && s.Vault.PasswordHash != "" && !isDemoMode()
}

// hashVaultPassword makes the hash kept in the settings; the password itself is never kept.
func hashVaultPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), vaultPassCost)
	return string(hash), err
}

// vaultAuth checks the user and password of a request, remembering the right ones for a while.
type vaultAuth struct {
	mutex sync.Mutex
	known map[string]time.Time
}

func (v *vaultAuth) check(s *settings.AppSettings, user string, password string) bool {
	if subtle.ConstantTimeCompare([]byte(user), []byte(s.Vault.User)) != 1 {
		return false
	}
	sum := sha256.Sum256([]byte(s.Vault.PasswordHash + "\x00" + password))
	key := hex.EncodeToString(sum[:])
	v.mutex.Lock()
	if v.known == nil {
		v.known = map[string]time.Time{}
	}
	if expires, ok := v.known[key]; ok && time.Now().Before(expires) {
		v.mutex.Unlock()
		return true
	}
	v.mutex.Unlock()
	if bcrypt.CompareHashAndPassword([]byte(s.Vault.PasswordHash), []byte(password)) != nil {
		return false
	}
	v.mutex.Lock()
	// a new password or user forgets the old ones
	if len(v.known) > 64 {
		v.known = map[string]time.Time{}
	}
	v.known[key] = time.Now().Add(vaultAuthCache)
	v.mutex.Unlock()
	return true
}

// vaultHandler answers the WebDAV requests of JKSV.
func (web *Web) vaultHandler() http.Handler {
	locks := webdav.NewMemLS()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appSettings := settings.ReadSettings(web.dataFolder)
		if !vaultEnabled(appSettings) {
			http.NotFound(w, r)
			return
		}
		ip := clientIp(r)
		user, password, ok := r.BasicAuth()
		keys := []string{"vault-ip:" + ip, "vault-user:" + strings.ToLower(user)}
		if retryAfter, delay := web.auth.limiter.check(keys...); retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			http.Error(w, "too many failed logins, try again later", http.StatusTooManyRequests)
			return
		} else if delay > 0 {
			sleepFor(delay)
		}
		if !ok || !web.vault.check(appSettings, user, password) {
			if ok {
				web.auth.limiter.fail(keys...)
				web.sugarLogger.Warnf("Failed save vault login for %q from %s", user, ip)
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="Save vault", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		web.auth.limiter.succeed(keys...)
		// an upload over the Wi-Fi of the console can take longer than the usual read timeout
		if r.Method == http.MethodPut {
			http.NewResponseController(w).SetReadDeadline(time.Now().Add(time.Hour))
		}

		root := web.vaultRoot()
		if err := os.MkdirAll(filepath.Join(root, VAULT_BASE), 0755); err != nil {
			http.Error(w, "the vault folder cannot be created", http.StatusInternalServerError)
			return
		}
		handler := &webdav.Handler{Prefix: VAULT_PREFIX, FileSystem: webdav.Dir(root), LockSystem: locks,
			Logger: func(r *http.Request, err error) {
				if err != nil {
					web.sugarLogger.Debugf("Save vault %s %s: %v", r.Method, r.URL.Path, err)
				}
			}}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		handler.ServeHTTP(recorder, r)
		if (r.Method == http.MethodPut || r.Method == "MOVE") && recorder.status >= 200 && recorder.status < 300 {
			web.afterVaultUpload(r)
		}
	})
}

// withVault sends the requests of the vault to its handler, before the login of the web
// interface: the vault has its own user and password.
func (web *Web) withVault(next http.Handler) http.Handler {
	vault := web.vaultHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == VAULT_PREFIX || strings.HasPrefix(r.URL.Path, VAULT_PREFIX+"/") {
			vault.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SaveBackup is one backup of a save, a ZIP uploaded by JKSV.
type SaveBackup struct {
	// the path inside the vault, with forward slashes, to download or delete it
	Path string
	Name string
	// the folder of the game, named by JKSV after the game
	Folder  string
	TitleId string
	Time    time.Time
	Size    int64
}

// SaveGame is a game with its backups, the newest first.
type SaveGame struct {
	Name     string
	TitleId  string
	ImageUrl string
	// the game is in the library
	InLibrary bool
	Backups   []SaveBackup
	Size      int64
	Latest    time.Time
}

// jksvMeta reads the title ID and the time of a backup from the file JKSV puts in its ZIP.
func jksvMeta(path string) (string, time.Time, bool) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", time.Time{}, false
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name != jksvMetaName || file.UncompressedSize64 < 57 || file.UncompressedSize64 > 4096 {
			continue
		}
		in, err := file.Open()
		if err != nil {
			return "", time.Time{}, false
		}
		data, err := io.ReadAll(io.LimitReader(in, 4096))
		in.Close()
		if err != nil || len(data) < 57 || binary.LittleEndian.Uint32(data[0:4]) != jksvMetaMagic {
			return "", time.Time{}, false
		}
		// packed: magic (4), revision (1), application ID (8), account (16), system save ID (8),
		// type, rank (1 + 1), index (2), owner ID (8), timestamp (8)
		id := strings.ToUpper(strconv.FormatUint(binary.LittleEndian.Uint64(data[5:13]), 16))
		id = strings.Repeat("0", max(16-len(id), 0)) + id
		var when time.Time
		if stamp := binary.LittleEndian.Uint64(data[49:57]); stamp > 1500000000 && stamp < 4102444800 {
			when = time.Unix(int64(stamp), 0)
		}
		return id, when, true
	}
	return "", time.Time{}, false
}

// vaultIndex remembers what was read from each backup, until the file changes.
type vaultIndex struct {
	mutex sync.Mutex
	files map[string]vaultIndexEntry
}

type vaultIndexEntry struct {
	size    int64
	modTime time.Time
	titleId string
	time    time.Time
}

// vaultBackups lists every backup of the vault.
func (web *Web) vaultBackups() []SaveBackup {
	root := web.vaultRoot()
	backups := []SaveBackup{}
	web.vaultFiles.mutex.Lock()
	defer web.vaultFiles.mutex.Unlock()
	if web.vaultFiles.files == nil {
		web.vaultFiles.files = map[string]vaultIndexEntry{}
	}
	seen := map[string]bool{}
	filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		cached, ok := web.vaultFiles.files[path]
		if !ok || cached.size != info.Size() || !cached.modTime.Equal(info.ModTime()) {
			cached = vaultIndexEntry{size: info.Size(), modTime: info.ModTime()}
			cached.titleId, cached.time, _ = jksvMeta(path)
			web.vaultFiles.files[path] = cached
		}
		seen[path] = true
		relative, _ := filepath.Rel(root, path)
		backup := SaveBackup{Path: filepath.ToSlash(relative), Name: entry.Name(), Folder: filepath.Base(filepath.Dir(path)),
			TitleId: cached.titleId, Time: cached.time, Size: info.Size()}
		if backup.Time.IsZero() {
			backup.Time = info.ModTime()
		}
		backups = append(backups, backup)
		return nil
	})
	for path := range web.vaultFiles.files {
		if !seen[path] {
			delete(web.vaultFiles.files, path)
		}
	}
	return backups
}

// saveGames groups the backups by game, matched to the library by their title ID (or by the
// name of their folder for backups without it), the most recent backup first.
func (web *Web) saveGames(lang string) []SaveGame {
	switchDB, _ := web.state.get()
	library := web.derived("library:"+lang, func() any { return web.buildLibrary(lang) }).([]TitleItem)
	byId := map[string]TitleItem{}
	byName := map[string]TitleItem{}
	for _, item := range library {
		byId[strings.ToUpper(item.Id)] = item
		byName[searchText(item.Name)] = item
		if item.OriginalName != "" {
			byName[searchText(item.OriginalName)] = item
		}
	}
	games := map[string]*SaveGame{}
	for _, backup := range web.vaultBackups() {
		key := "folder:" + strings.ToLower(backup.Folder)
		game := SaveGame{Name: backup.Folder}
		item, known := byId[backup.TitleId]
		if !known {
			item, known = byName[searchText(backup.Folder)]
		}
		if known {
			key = "id:" + strings.ToUpper(item.Id)
			game = SaveGame{Name: item.Name, TitleId: strings.ToUpper(item.Id), ImageUrl: item.ImageUrl, InLibrary: true}
		} else if backup.TitleId != "" {
			key = "id:" + backup.TitleId
			game.TitleId = backup.TitleId
			if switchDB != nil {
				if name := titleName(switchDB, lang, backup.TitleId, ""); name != "" {
					game.Name = name
				}
			}
		}
		existing, ok := games[key]
		if !ok {
			copy := game
			existing = &copy
			games[key] = existing
		}
		existing.Backups = append(existing.Backups, backup)
		existing.Size += backup.Size
		if backup.Time.After(existing.Latest) {
			existing.Latest = backup.Time
		}
	}
	result := make([]SaveGame, 0, len(games))
	for _, game := range games {
		sort.Slice(game.Backups, func(i, j int) bool { return game.Backups[i].Time.After(game.Backups[j].Time) })
		result = append(result, *game)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Latest.After(result[j].Latest) })
	return result
}

// vaultPath resolves a path of the vault given by a page, refusing anything outside it.
func (web *Web) vaultPath(relative string) (string, bool) {
	root := web.vaultRoot()
	clean := filepath.Clean(filepath.FromSlash("/" + relative))
	path := filepath.Join(root, clean)
	inside, err := filepath.Rel(root, path)
	if err != nil || inside == "." || strings.HasPrefix(inside, "..") || !strings.EqualFold(filepath.Ext(path), ".zip") {
		return "", false
	}
	return path, true
}

// afterVaultUpload keeps the number of backups asked for in the folder of the new one, and
// tells about it, in the background.
func (web *Web) afterVaultUpload(r *http.Request) {
	target := strings.TrimPrefix(r.URL.Path, VAULT_PREFIX)
	if r.Method == "MOVE" {
		if destination := r.Header.Get("Destination"); destination != "" {
			if index := strings.Index(destination, VAULT_PREFIX+"/"); index >= 0 {
				target = destination[index+len(VAULT_PREFIX):]
			}
		}
	}
	path, ok := web.vaultPath(target)
	if !ok {
		return
	}
	web.backgroundWork.Add(1)
	go func() {
		defer web.backgroundWork.Add(-1)
		appSettings := settings.ReadSettings(web.dataFolder)
		if keep := appSettings.Vault.Keep; keep > 0 {
			pruneVaultFolder(filepath.Dir(path), keep)
		}
		web.invalidateDerived()
		if appSettings.Vault.Notify {
			web.notifyVaultUpload(path)
		}
	}()
}

// pruneVaultFolder deletes the oldest backups of a folder beyond the number kept.
func pruneVaultFolder(folder string, keep int) int {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return 0
	}
	type backup struct {
		path string
		when time.Time
	}
	backups := []backup{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".zip") {
			continue
		}
		path := filepath.Join(folder, entry.Name())
		_, when, ok := jksvMeta(path)
		if !ok || when.IsZero() {
			if info, err := entry.Info(); err == nil {
				when = info.ModTime()
			}
		}
		backups = append(backups, backup{path, when})
	}
	if len(backups) <= keep {
		return 0
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].when.After(backups[j].when) })
	removed := 0
	for _, old := range backups[keep:] {
		if os.Remove(old.path) == nil {
			removed++
		}
	}
	return removed
}

// notifyVaultUpload tells about a new backup on the notification channels.
func (web *Web) notifyVaultUpload(path string) {
	settingsObj := settings.ReadSettings(web.dataFolder)
	if !notificationsConfigured(settingsObj.Notifications) {
		return
	}
	lang := settingsObj.Language
	if !isSupportedLanguage(lang) {
		lang = DEFAULT_LANGUAGE
	}
	name := filepath.Base(filepath.Dir(path))
	if id, _, ok := jksvMeta(path); ok {
		switchDB, _ := web.state.get()
		if switchDB != nil {
			if title := titleName(switchDB, lang, id, ""); title != "" {
				name = title
			}
		}
	}
	title := translate(lang, "Switch Library Manager: save backed up")
	text := translatef(lang, "New backup of your save of %v.", name)
	if err := sendMessage(settingsObj.Notifications, title, text, map[string]string{"game": name, "file": filepath.Base(path)}); err != nil {
		web.sugarLogger.Warnf("Failed to send the save vault message: %v", err)
	}
}

type SavesPageData struct {
	GlobalPageData
	Games []SaveGame
	// the vault is on, and its totals
	Enabled bool
	Backups int
	Size    int64
	Latest  time.Time
	// games of the library without any backup
	Missing []TitleItem
}

func (web *Web) HandleSaves() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/saves.html")
	web.router.HandleFunc("/saves.html", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		// the demo shows made-up backups, with the vault as if it were on
		data := SavesPageData{GlobalPageData: web.globalPageData("saves"), Enabled: vaultEnabled(settings.ReadSettings(web.dataFolder)) || isDemoMode(), Games: web.saveGames(lang)}
		saved := map[string]bool{}
		for _, game := range data.Games {
			data.Backups += len(game.Backups)
			data.Size += game.Size
			if game.Latest.After(data.Latest) {
				data.Latest = game.Latest
			}
			if game.TitleId != "" {
				saved[game.TitleId] = true
			}
		}
		if len(data.Games) > 0 {
			library := web.derived("library:"+lang, func() any { return web.buildLibrary(lang) }).([]TitleItem)
			for _, item := range library {
				if !saved[strings.ToUpper(item.Id)] && !item.Demo {
					data.Missing = append(data.Missing, item)
				}
			}
			sort.Slice(data.Missing, func(i, j int) bool { return sortName(data.Missing[i].Name) < sortName(data.Missing[j].Name) })
		}
		web.render(w, r, templates, data)
	}).Methods("GET")

	web.router.HandleFunc("/saves/download", func(w http.ResponseWriter, r *http.Request) {
		path, ok := web.vaultPath(r.URL.Query().Get("path"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		name := filepath.Base(filepath.Dir(path)) + " - " + filepath.Base(path)
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
		http.ServeFile(w, r, path)
	}).Methods("GET")

	web.router.HandleFunc("/saves/delete", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		path, ok := web.vaultPath(r.FormValue("path"))
		if !ok {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		if err := os.Remove(path); err != nil {
			writeGlobalError(w, http.StatusInternalServerError, lang, err.Error())
			return
		}
		// an empty folder of a game goes too, as on the console
		folder := filepath.Dir(path)
		if entries, err := os.ReadDir(folder); err == nil && len(entries) == 0 && filepath.Dir(folder) != filepath.Dir(web.vaultRoot()) {
			if inside, err := filepath.Rel(filepath.Join(web.vaultRoot(), VAULT_BASE), folder); err == nil && inside != "." && !strings.HasPrefix(inside, "..") {
				os.Remove(folder)
			}
		}
		web.sugarLogger.Infof("Save backup %s deleted", r.FormValue("path"))
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}).Methods("POST")
}

// titleSaves are the backups of a game, for its page.
func (web *Web) titleSaves(id string, lang string) []SaveBackup {
	for _, game := range web.saveGames(lang) {
		if strings.EqualFold(game.TitleId, id) {
			return game.Backups
		}
	}
	return nil
}
