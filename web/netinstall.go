package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Installing over the network: the library is listed for the installers of the console, so a
// user installs their own games on their own consoles without copying them to the SD card.
//
//   - /install/dbi/ lists folders and files as the directory listing of nginx, which DBI reads
//     as a source of the kind ApacheHTTP: Games, Updates, DLC and a folder per game.
//   - /install/tinfoil is the index of the kind of Tinfoil, which CyberFoil and Sphaira read too.
//   - /install/f/{id}/{name} is a file of the index.
//
// It is off until it is turned on in Settings, it needs users (never without a password) and it
// answers only on the local network unless allowed. A console signs in with an API key made
// for it, of the role Console, sent as the password: it can list and download, nothing else.
//
// The files in the experimental formats of NXPress (NCZSECT2, NCZSECT3), which the installers
// refuse, are given as a package they read, made while it is downloaded (see
// switchfs.InstallStream): the NCA of those NCZ decompressed, everything else as it is.

const (
	NET_INSTALL_PREFIX = "/install/"
	netInstallDbi      = NET_INSTALL_PREFIX + "dbi/"
	netInstallTinfoil  = NET_INSTALL_PREFIX + "tinfoil"
	netInstallFiles    = NET_INSTALL_PREFIX + "f/"
	// what the console will see, counted for Settings
	netInstallSummaryPath = "/settings/net-install/summary"
	// the longest message of the index
	maxNetInstallMessage = 200

	// the folders of the DBI listing
	netFolderGames   = "Games"
	netFolderUpdates = "Updates"
	netFolderDlc     = "DLC"
	netFolderByGame  = "By game"
)

// netInstallExtensions are the files an installer takes; a NUP is for emulators.
var netInstallExtensions = map[string]bool{".nsp": true, ".nsz": true, ".xci": true, ".xcz": true}

// netFile is a file of the library as an installer sees it.
type netFile struct {
	id   string
	path string
	// the name given: the name of the game, its title ID and version, and the extension of what
	// is given
	name    string
	size    int64
	kind    string
	game    string
	modTime time.Time
	// the package made for the installer, when the file holds an experimental NCZ
	stream *switchfs.InstallStream
}

// netInstallEnabled tells whether the network install answers.
func netInstallEnabled(s *settings.AppSettings) bool {
	return s.NetInstall.Enabled && !isDemoMode()
}

// consoleAllowed is what a key of the role Console may ask: reading the paths of /install/.
func consoleAllowed(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return strings.HasPrefix(path.Clean(r.URL.Path)+"/", NET_INSTALL_PREFIX)
}

// netInstallStreams remembers what was read of the files with experimental NCZ, by path, size
// and time, so a listing does not open every NSZ again.
type netInstallStreams struct {
	mutex sync.Mutex
	known map[string]netStreamEntry
}

type netStreamEntry struct {
	size    int64
	modTime time.Time
	stream  *switchfs.InstallStream
}

func (c *netInstallStreams) get(path string, info os.FileInfo) *switchfs.InstallStream {
	c.mutex.Lock()
	if entry, ok := c.known[path]; ok && entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
		c.mutex.Unlock()
		return entry.stream
	}
	c.mutex.Unlock()
	stream, err := switchfs.OpenInstallStream(path)
	if err != nil || stream.Translated == 0 {
		// a damaged file is served as it is: the installer tells what is wrong with it
		stream = nil
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.known == nil || len(c.known) > 20000 {
		c.known = map[string]netStreamEntry{}
	}
	c.known[path] = netStreamEntry{size: info.Size(), modTime: info.ModTime(), stream: stream}
	return stream
}

// netFileId names a file in the addresses: the start of the hash of its path.
func netFileId(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:8])
}

// netSafeName takes out of a name what a file system of the console or an address cannot hold.
func netSafeName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || strings.ContainsRune(`/\:*?"<>|#%`, r) {
			return -1
		}
		return r
	}, name)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if len([]rune(cleaned)) > 120 {
		cleaned = string([]rune(cleaned)[:120])
	}
	return strings.TrimSpace(strings.Trim(cleaned, ". "))
}

// netFiles lists the files of the library an installer takes: every game, the newest update of
// each (or all of them) and every DLC.
func (web *Web) netFiles(options settings.NetInstallOptions) []netFile {
	switchDB, localDB := web.state.get()
	files := []netFile{}
	if localDB == nil {
		return files
	}
	seen := map[string]bool{}
	add := func(file db.SwitchFileInfo, kind string, game string, dlcName string) {
		info := compressedSinceScan(file.ExtendedInfo)
		full := filepath.Join(info.BaseFolder, info.FileName)
		extension := strings.ToLower(filepath.Ext(info.FileName))
		if !netInstallExtensions[extension] || seen[full] || web.linksIntoData(full) {
			return
		}
		stat, err := os.Stat(full)
		if err != nil || stat.IsDir() {
			return
		}
		seen[full] = true
		entry := netFile{id: netFileId(full), path: full, size: stat.Size(), kind: kind, game: game, modTime: stat.ModTime()}
		if extension == ".nsz" || extension == ".xcz" {
			if stream := web.netStreams.get(full, stat); stream != nil {
				entry.stream, entry.size, extension = stream, stream.Size(), stream.Extension()
			}
		}
		name := netSafeName(strings.TrimSuffix(info.FileName, filepath.Ext(info.FileName)))
		if file.Metadata != nil {
			// the title ID and the version in the name, which Tinfoil needs and DBI shows
			base := netSafeName(game)
			if kind == netFolderDlc {
				// the name of the DLC, which usually starts with the one of its game
				base = netSafeName(dlcName)
				if base == "" {
					base = netSafeName(db.ParseTitleNameFromFileName(info.FileName))
				}
			}
			if base == "" {
				base = name
			}
			name = fmt.Sprintf("%s [%s][v%d]", base, strings.ToUpper(file.Metadata.TitleId), file.Metadata.Version)
		}
		if name == "" {
			name = entry.id
		}
		entry.name = name + extension
		files = append(files, entry)
	}
	for _, title := range localDB.TitlesMap {
		if title.File.Metadata == nil && len(title.Updates) == 0 && len(title.Dlc) == 0 {
			continue
		}
		game := ""
		var known *db.SwitchTitle
		if title.File.Metadata != nil {
			if prefix, err := db.TitleIDPrefix(title.File.Metadata.TitleId); err == nil && switchDB != nil {
				known = switchDB.TitlesMap[prefix]
			}
			game = getLocalTitleName(known, title)
		}
		if game == "" {
			game = strings.TrimSpace(db.ParseTitleNameFromFileName(title.File.ExtendedInfo.FileName))
		}
		if title.BaseExist && !title.IsSplit {
			add(title.File, netFolderGames, game, "")
		}
		versions := make([]int, 0, len(title.Updates))
		for version := range title.Updates {
			versions = append(versions, version)
		}
		sort.Ints(versions)
		for _, version := range versions {
			if options.AllUpdates || version == title.LatestUpdate {
				add(title.Updates[version], netFolderUpdates, game, "")
			}
		}
		for _, id := range sortedKeys(title.Dlc) {
			dlcName := ""
			if known != nil {
				if entry, ok := known.Dlc[id]; ok {
					dlcName = entry.Name
				}
			}
			add(title.Dlc[id], netFolderDlc, game, dlcName)
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if !strings.EqualFold(files[i].game, files[j].game) {
			return strings.ToLower(files[i].game) < strings.ToLower(files[j].game)
		}
		return files[i].name < files[j].name
	})
	return files
}

// netFilesCacheTime is how long the list of files is kept. An installer takes a file in many
// pieces, one request each, and making the list looks at every file of the library: on a NAS
// that is slow enough to hold the transfer back.
const netFilesCacheTime = 30 * time.Second

// netFilesCache is the list of files given last, and the same files by id.
type netFilesCache struct {
	mutex      sync.Mutex
	localDB    *db.LocalSwitchFilesDB
	allUpdates bool
	until      time.Time
	files      []netFile
	byId       map[string]netFile
}

// netFilesKept is netFiles, kept for a while: the same library and options give the same list.
func (web *Web) netFilesKept(options settings.NetInstallOptions) ([]netFile, map[string]netFile) {
	_, localDB := web.state.get()
	cache := &web.netList
	cache.mutex.Lock()
	if cache.files != nil && cache.localDB == localDB && cache.allUpdates == options.AllUpdates && time.Now().Before(cache.until) {
		files, byId := cache.files, cache.byId
		cache.mutex.Unlock()
		return files, byId
	}
	cache.mutex.Unlock()

	files := web.netFiles(options)
	byId := make(map[string]netFile, len(files))
	for _, file := range files {
		byId[file.id] = file
	}
	cache.mutex.Lock()
	cache.localDB, cache.allUpdates, cache.until, cache.files, cache.byId = localDB, options.AllUpdates, time.Now().Add(netFilesCacheTime), files, byId
	cache.mutex.Unlock()
	return files, byId
}

// netFilesForget drops the kept list, so the next request makes it again.
func (web *Web) netFilesForget() {
	web.netList.mutex.Lock()
	web.netList.files, web.netList.byId = nil, nil
	web.netList.mutex.Unlock()
}

// netFileChanged tells a kept file that is no longer what it was on disk: gone, or written again.
func netFileChanged(file netFile) bool {
	stat, err := os.Stat(file.path)
	if err != nil || !stat.ModTime().Equal(file.modTime) {
		return true
	}
	return file.stream == nil && stat.Size() != file.size
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// netInstallGate answers a request the network install does not take, and tells whether it
// takes it: turned on, with users (never without a password), and from the local network
// unless allowed.
func (web *Web) netInstallGate(w http.ResponseWriter, r *http.Request) (settings.NetInstallOptions, bool) {
	appSettings := settings.ReadSettings(web.dataFolder)
	if !netInstallEnabled(appSettings) {
		http.NotFound(w, r)
		return appSettings.NetInstall, false
	}
	if !web.auth.Enabled() {
		http.Error(w, "installing over the network needs a user with a password", http.StatusForbidden)
		return appSettings.NetInstall, false
	}
	if !appSettings.NetInstall.Remote && !isLocalRequest(r) {
		http.Error(w, "installing over the network answers only on the local network", http.StatusForbidden)
		return appSettings.NetInstall, false
	}
	return appSettings.NetInstall, true
}

// netInstallMessage is the message of the index, on one line and not too long.
func netInstallMessage(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if runes := []rune(message); len(runes) > maxNetInstallMessage {
		message = string(runes[:maxNetInstallMessage])
	}
	return message
}

// NetInstallSummary is what the console will see, for Settings.
type NetInstallSummary struct {
	Games      int `json:"games"`
	Updates    int `json:"updates"`
	Dlc        int `json:"dlc"`
	Translated int `json:"translated"`
}

// handleNetInstallSummary counts what the console will see, with the settings as they are saved.
func (web *Web) handleNetInstallSummary(w http.ResponseWriter, r *http.Request) {
	summary := NetInstallSummary{}
	for _, file := range web.netFiles(settings.ReadSettings(web.dataFolder).NetInstall) {
		switch file.kind {
		case netFolderGames:
			summary.Games++
		case netFolderUpdates:
			summary.Updates++
		case netFolderDlc:
			summary.Dlc++
		}
		if file.stream != nil {
			summary.Translated++
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}

// HandleNetInstall answers the installers of the console.
func (web *Web) HandleNetInstall() {
	web.router.HandleFunc(netInstallSummaryPath, web.handleNetInstallSummary).Methods(http.MethodGet)
	web.router.PathPrefix(netInstallDbi).HandlerFunc(web.handleNetDbi).Methods(http.MethodGet, http.MethodHead)
	web.router.HandleFunc(strings.TrimSuffix(netInstallDbi, "/"), func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, netInstallDbi, http.StatusMovedPermanently)
	}).Methods(http.MethodGet, http.MethodHead)
	web.router.HandleFunc(netInstallTinfoil, web.handleNetTinfoil).Methods(http.MethodGet, http.MethodHead)
	web.router.PathPrefix(netInstallFiles).HandlerFunc(web.handleNetFile).Methods(http.MethodGet, http.MethodHead)
}

// netDbiFolders are the folders of the DBI listing and their files.
func netDbiFolders(files []netFile) map[string][]netFile {
	folders := map[string][]netFile{netFolderGames: {}, netFolderUpdates: {}, netFolderDlc: {}}
	for _, file := range files {
		folders[file.kind] = append(folders[file.kind], file)
		game := netSafeName(file.game)
		if game == "" {
			game = "Unknown"
		}
		key := netFolderByGame + "/" + game
		folders[key] = append(folders[key], file)
	}
	return folders
}

// netUniqueNames gives each file of a folder a name of its own.
func netUniqueNames(files []netFile) []netFile {
	used := map[string]bool{}
	out := make([]netFile, len(files))
	for i, file := range files {
		name := file.name
		if used[strings.ToLower(name)] {
			extension := filepath.Ext(name)
			name = strings.TrimSuffix(name, extension) + " (" + file.id[:6] + ")" + extension
		}
		used[strings.ToLower(name)] = true
		file.name = name
		out[i] = file
	}
	return out
}

// handleNetDbi lists a folder, or gives a file of it.
func (web *Web) handleNetDbi(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netInstallGate(w, r)
	if !ok {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, netInstallDbi)
	if strings.Contains(rest, "..") || strings.Contains(rest, "\\") {
		http.NotFound(w, r)
		return
	}
	kept, _ := web.netFilesKept(options)
	folders := netDbiFolders(kept)
	isFolder := rest == "" || strings.HasSuffix(rest, "/")
	folder := strings.TrimSuffix(rest, "/")
	if isFolder {
		entries := []netListEntry{}
		switch folder {
		case "":
			for _, name := range []string{netFolderGames, netFolderUpdates, netFolderDlc, netFolderByGame} {
				entries = append(entries, netListEntry{name: name + "/", size: -1})
			}
		case netFolderByGame:
			for key := range folders {
				if game, ok := strings.CutPrefix(key, netFolderByGame+"/"); ok {
					entries = append(entries, netListEntry{name: game + "/", size: -1})
				}
			}
			sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].name) < strings.ToLower(entries[j].name) })
		default:
			files, ok := folders[folder]
			if !ok {
				http.NotFound(w, r)
				return
			}
			for _, file := range netUniqueNames(files) {
				entries = append(entries, netListEntry{name: file.name, size: file.size, modTime: file.modTime})
			}
		}
		writeNginxListing(w, r, entries)
		return
	}
	parent, name := path.Split(folder)
	find := func(folders map[string][]netFile) (netFile, bool) {
		for _, file := range netUniqueNames(folders[strings.TrimSuffix(parent, "/")]) {
			if file.name == name {
				return file, true
			}
		}
		return netFile{}, false
	}
	file, ok := find(folders)
	if ok && netFileChanged(file) {
		// written again since the list was made: make it again
		web.netFilesForget()
		kept, _ = web.netFilesKept(options)
		file, ok = find(netDbiFolders(kept))
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	web.serveNetFile(w, r, file)
}

// netListEntry is a line of a listing; a folder has size -1 and a name that ends with "/".
type netListEntry struct {
	name    string
	size    int64
	modTime time.Time
}

// writeNginxListing writes a listing as the autoindex of nginx does, which DBI reads.
func writeNginxListing(w http.ResponseWriter, r *http.Request, entries []netListEntry) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	title := html.EscapeString(r.URL.Path)
	var b strings.Builder
	fmt.Fprintf(&b, "<html>\r\n<head><title>Index of %s</title></head>\r\n<body>\r\n<h1>Index of %s</h1><hr><pre><a href=\"../\">../</a>\r\n", title, title)
	for _, entry := range entries {
		href := (&url.URL{Path: entry.name}).EscapedPath()
		// a name with a colon would be read as a scheme
		if strings.Contains(strings.SplitN(href, "/", 2)[0], ":") {
			href = "./" + href
		}
		shown := entry.name
		if len([]rune(shown)) > 50 {
			shown = string([]rune(shown)[:47]) + "..>"
		}
		size, when := "-", "-"
		if entry.size >= 0 {
			size = fmt.Sprintf("%d", entry.size)
		}
		if !entry.modTime.IsZero() {
			when = entry.modTime.UTC().Format("02-Jan-2006 15:04")
		} else {
			when = time.Now().UTC().Format("02-Jan-2006 15:04")
		}
		padding := max(1, 51-len([]rune(shown)))
		fmt.Fprintf(&b, "<a href=\"%s\">%s</a>%s%s %19s\r\n", html.EscapeString(href), html.EscapeString(shown), strings.Repeat(" ", padding), when, size)
	}
	b.WriteString("</pre><hr></body>\r\n</html>\r\n")
	if r.Method == http.MethodHead {
		return
	}
	w.Write([]byte(b.String()))
}

// netTinfoilIndex is the index of the kind of Tinfoil.
type netTinfoilIndex struct {
	Files   []netTinfoilFile `json:"files"`
	Success string           `json:"success,omitempty"`
}

type netTinfoilFile struct {
	Url  string `json:"url"`
	Size int64  `json:"size"`
}

// handleNetTinfoil gives the index of the kind of Tinfoil, with the addresses of the files.
func (web *Web) handleNetTinfoil(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netInstallGate(w, r)
	if !ok {
		return
	}
	scheme := "http"
	if isHttps(r) {
		scheme = "https"
	}
	index := netTinfoilIndex{Files: []netTinfoilFile{}, Success: strings.TrimSpace(options.Message)}
	kept, _ := web.netFilesKept(options)
	for _, file := range kept {
		address := url.URL{Scheme: scheme, Host: r.Host, Path: netInstallFiles + file.id + "/" + file.name, Fragment: file.name}
		index.Files = append(index.Files, netTinfoilFile{Url: address.String(), Size: file.size})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(index)
}

// handleNetFile gives a file of the index by its id; the name after it is only for the installer.
func (web *Web) handleNetFile(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netInstallGate(w, r)
	if !ok {
		return
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, netInstallFiles), "/")
	_, byId := web.netFilesKept(options)
	file, ok := byId[id]
	if ok && netFileChanged(file) {
		// written again since the list was made: make it again
		web.netFilesForget()
		_, byId = web.netFilesKept(options)
		file, ok = byId[id]
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	web.serveNetFile(w, r, file)
}

// netInstallNoteEvery is how often the same file sent to the same console is noted: an installer
// that tries again asks for its start once more.
const netInstallNoteEvery = 10 * time.Minute

// netInstallNotes are the files noted last, by console and file, so a retry is not noted again.
type netInstallNotes struct {
	mutex sync.Mutex
	last  map[string]time.Time
}

// noteNetInstall notes in the activity that a file starts going to a console: the user, the name
// of the key of the console and the file.
func (web *Web) noteNetInstall(r *http.Request, file netFile) {
	principal := principalFrom(r)
	web.sugarLogger.Infof("Network install: %s sent to %s (%s)", file.name, clientIp(r), principal.Name)
	if isDemoMode() {
		return
	}
	now := time.Now()
	key := principal.Name + "|" + principal.KeyName + "|" + file.id
	web.netNotes.mutex.Lock()
	if web.netNotes.last == nil || len(web.netNotes.last) > 1000 {
		web.netNotes.last = map[string]time.Time{}
	}
	if last, ok := web.netNotes.last[key]; ok && now.Sub(last) < netInstallNoteEvery {
		web.netNotes.mutex.Unlock()
		return
	}
	web.netNotes.last[key] = now
	web.netNotes.mutex.Unlock()
	detail := file.name
	if principal.KeyName != "" {
		detail = quotedKeyName(principal.KeyName) + ": " + file.name
	}
	web.activities().Add(activity.Entry{Time: now, User: principal.Name, Action: activity.ACTION_NET_INSTALL, Detail: detail, IP: clientIp(r)})
}

// serveNetFile gives a file with range requests: as it is, or the package made for the
// installer when it holds an experimental NCZ.
func (web *Web) serveNetFile(w http.ResponseWriter, r *http.Request, file netFile) {
	// a download over the Wi-Fi of the console takes long
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Ranges", "bytes")
	if r.Method == http.MethodGet && (r.Header.Get("Range") == "" || strings.HasPrefix(r.Header.Get("Range"), "bytes=0-")) {
		web.noteNetInstall(r, file)
	}
	if file.stream != nil {
		reader := file.stream.Reader()
		defer reader.Close()
		http.ServeContent(w, r, "", file.modTime, reader)
		return
	}
	source, err := os.Open(file.path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer source.Close()
	http.ServeContent(w, r, "", file.modTime, source)
}
