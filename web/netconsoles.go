package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/gorilla/mux"
)

// The consoles of a user as NXDock tells them: what is installed on each, its system and how much
// each game was played.
//
//   - With what is installed (POST /install/inventory, see netpush.go) the newer versions of NXDock
//     tell the system of the console ("console": firmware, Atmosphère, emuMMC, free space, the
//     signature patches) and the time played of each game ("play"). The older ones tell only the
//     titles. All of it is kept in the file of the console; the minutes played are kept besides
//     once a day, for 400 days, so the hours of a year can be counted.
//   - GET /api/install/consoles/{id} compares a console of the user with the library: its games,
//     the newest version the library has of each, the DLC it lacks, and the files that bring it up
//     to date. POST /api/install/consoles/{id}/update sends those files to it, as Send to console
//     does.

const (
	// the titles played kept of a console at most
	netPlayMax = 5000
	// the days of time played kept of a console
	netPlayDays = 400
	// the longest text of the system of a console
	netConsoleTextMax = 32
	// how long the consoles wait for the list of files to count what the library has newer for
	// them; past that they are given without it
	netConsolesCountWait = 3 * time.Second
)

var (
	// the texts of the system of a console: a version, a name
	netConsoleTextPattern = regexp.MustCompile(`^[0-9A-Za-z .+_()-]*$`)
	// what a user asks of a console of theirs: to bring it up to date, to remove a game
	netConsoleAskPattern = regexp.MustCompile(`^` + netInstallConsoles + `/[0-9a-f]{16}/(update|remove)$`)
	errNetConsoleUnknown = errors.New("the console has not told what is installed on it")
)

// netConsoleAskPath tells a path that asks something of a console (bring it up to date, remove a
// game), which its user may ask.
func netConsoleAskPath(path string) bool {
	return netConsoleAskPattern.MatchString(path)
}

// netConsoleFacts is the system of a console, as it told it last. The space is in bytes.
type netConsoleFacts struct {
	Firmware   string `json:"firmware"`
	Atmosphere string `json:"atmosphere"`
	Emummc     bool   `json:"emummc"`
	SdFree     int64  `json:"sdFree"`
	SdTotal    int64  `json:"sdTotal"`
	NandFree   int64  `json:"nandFree"`
	NandTotal  int64  `json:"nandTotal"`
	Sigpatches string `json:"sigpatches"`
}

// netConsoleFactsOf reads the system a console tells ("console"), fact by fact, so one told wrong
// does not lose the others: a text too long or with other signs than letters, digits, spaces and
// . + - _ ( ), a number below 0 or not a whole one, a free space above its total. The facts left
// out are named in dropped; the system is nil when it tells nothing, or is not an object.
func netConsoleFactsOf(raw json.RawMessage) (*netConsoleFacts, []string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, []string{"console"}
	}
	facts, dropped := netConsoleFacts{}, []string{}
	text := func(name string, into *string) {
		value, ok := fields[name]
		if !ok {
			return
		}
		var told string
		if json.Unmarshal(value, &told) != nil {
			dropped = append(dropped, name)
			return
		}
		told = strings.TrimSpace(told)
		if len(told) > netConsoleTextMax || !netConsoleTextPattern.MatchString(told) {
			dropped = append(dropped, name)
			return
		}
		*into = told
	}
	number := func(name string, into *int64) bool {
		value, ok := fields[name]
		if !ok {
			return true
		}
		var told int64
		if json.Unmarshal(value, &told) != nil || told < 0 {
			return false
		}
		*into = told
		return true
	}
	text("firmware", &facts.Firmware)
	text("atmosphere", &facts.Atmosphere)
	text("sigpatches", &facts.Sigpatches)
	if value, ok := fields["emummc"]; ok && json.Unmarshal(value, &facts.Emummc) != nil {
		dropped = append(dropped, "emummc")
	}
	// a free space goes with its total: both, or none of them
	for _, space := range []struct {
		free, total     string
		freeTo, totalTo *int64
	}{{"sdFree", "sdTotal", &facts.SdFree, &facts.SdTotal}, {"nandFree", "nandTotal", &facts.NandFree, &facts.NandTotal}} {
		freeOk, totalOk := number(space.free, space.freeTo), number(space.total, space.totalTo)
		if !freeOk || !totalOk || (*space.totalTo > 0 && *space.freeTo > *space.totalTo) {
			dropped = append(dropped, space.free+"/"+space.total)
			*space.freeTo, *space.totalTo = 0, 0
		}
	}
	if facts == (netConsoleFacts{}) {
		return nil, dropped
	}
	return &facts, dropped
}

// netPlayOf reads the titles played a console tells, one by one: one that is not as described (a
// number that is not a whole one, a title ID that is not a text) is left out, not the others.
func netPlayOf(raw []json.RawMessage) []netPlayEntry {
	entries := make([]netPlayEntry, 0, len(raw))
	for _, value := range raw {
		var entry netPlayEntry
		if json.Unmarshal(value, &entry) == nil {
			entries = append(entries, entry)
		}
	}
	return cleanNetPlay(entries)
}

// netPlayEntry is how much a title was played on a console, as it told it: the minutes, the times
// it was started and the last time it was played (Unix time, in seconds).
type netPlayEntry struct {
	TitleId    string `json:"titleId"`
	Minutes    int64  `json:"minutes"`
	Launches   int64  `json:"launches"`
	LastPlayed int64  `json:"lastPlayed"`
}

// cleanNetPlay keeps the titles played told well, once each: a title ID, no number below 0, and
// something played.
func cleanNetPlay(entries []netPlayEntry) []netPlayEntry {
	cleaned := []netPlayEntry{}
	seen := map[string]bool{}
	for _, entry := range entries {
		entry.TitleId = strings.ToUpper(strings.TrimSpace(entry.TitleId))
		if !netTitleIdPattern.MatchString(entry.TitleId) || entry.Minutes < 0 || entry.Launches < 0 || entry.LastPlayed < 0 || seen[entry.TitleId] {
			continue
		}
		if entry.Minutes == 0 && entry.Launches == 0 && entry.LastPlayed == 0 {
			continue
		}
		seen[entry.TitleId] = true
		cleaned = append(cleaned, entry)
	}
	return cleaned
}

// netPlayStats is how much a game was played: minutes and launches summed, the latest time.
type netPlayStats struct {
	Minutes    int64 `json:"minutes"`
	Launches   int64 `json:"launches"`
	LastPlayed int64 `json:"lastPlayed"`
}

func (stats *netPlayStats) add(minutes, launches, lastPlayed int64) {
	stats.Minutes += minutes
	stats.Launches += launches
	stats.LastPlayed = max(stats.LastPlayed, lastPlayed)
}

// netPlayByGame is the time played on a console by game, by the prefix of the game.
func netPlayByGame(play []netPlayEntry) map[string]*netPlayStats {
	games := map[string]*netPlayStats{}
	for _, entry := range play {
		prefix := netGamePrefix(entry.TitleId)
		if prefix == "" {
			continue
		}
		if games[prefix] == nil {
			games[prefix] = &netPlayStats{}
		}
		games[prefix].add(entry.Minutes, entry.Launches, entry.LastPlayed)
	}
	return games
}

// netPlayHistory is the time played on a console, once a day: by the day (UTC, 2006-01-02), the
// minutes of each title played as the console told them last that day.
type netPlayHistory struct {
	KeyId string                      `json:"keyId"`
	Days  map[string]map[string]int64 `json:"days"`
}

func (web *Web) netPlayHistoryPath(keyId string) string {
	return filepath.Join(web.dataFolder, netInventoryFolder, keyId+".play.json")
}

// readNetPlayHistory is the time played kept of a console, once a day.
func (web *Web) readNetPlayHistory(keyId string) (*netPlayHistory, bool) {
	if !netKeyIdPattern.MatchString(keyId) {
		return nil, false
	}
	data, err := os.ReadFile(web.netPlayHistoryPath(keyId))
	if err != nil {
		return nil, false
	}
	history := &netPlayHistory{}
	if json.Unmarshal(data, history) != nil || history.KeyId != keyId {
		return nil, false
	}
	if history.Days == nil {
		history.Days = map[string]map[string]int64{}
	}
	return history, true
}

// keepNetPlayDay keeps the minutes played a console told, as the ones of the day: in place of what
// it told before that day. The days older than netPlayDays go. With web.netInventories held.
func (web *Web) keepNetPlayDay(keyId string, play []netPlayEntry, now time.Time) error {
	history, ok := web.readNetPlayHistory(keyId)
	if !ok {
		history = &netPlayHistory{KeyId: keyId, Days: map[string]map[string]int64{}}
	}
	minutes := map[string]int64{}
	for _, entry := range play {
		if entry.Minutes > 0 {
			minutes[entry.TitleId] = entry.Minutes
		}
	}
	now = now.UTC()
	history.Days[now.Format(time.DateOnly)] = minutes
	oldest := now.AddDate(0, 0, -netPlayDays).Format(time.DateOnly)
	days := make([]string, 0, len(history.Days))
	for day := range history.Days {
		if day <= oldest {
			delete(history.Days, day)
			continue
		}
		days = append(days, day)
	}
	// a clock put back leaves days ahead: never more than netPlayDays all the same
	sort.Strings(days)
	for len(days) > netPlayDays {
		delete(history.Days, days[0])
		days = days[1:]
	}
	data, err := json.Marshal(history)
	if err != nil {
		return err
	}
	return durable.WriteFile(web.netPlayHistoryPath(keyId), data)
}

// netTitlePlay is how much a user played a game on their consoles, as the consoles told it: the
// minutes and the launches summed, the latest time it was played. ok is false when no console of
// the user told it played.
func (web *Web) netTitlePlay(user string, titleId string) (netPlayStats, bool) {
	prefix := netGamePrefix(strings.ToUpper(strings.TrimSpace(titleId)))
	stats, found := netPlayStats{}, false
	if prefix == "" {
		return stats, false
	}
	for _, key := range web.netUserConsoles(user) {
		inventory, ok := web.readNetInventory(key.ID)
		if !ok {
			continue
		}
		if played, ok := netPlayByGame(inventory.Play)[prefix]; ok {
			stats.add(played.Minutes, played.Launches, played.LastPlayed)
			found = true
		}
	}
	return stats, found
}

// netConsoleFirmware is the firmware a console of a user told: as it told it, and as a system
// version (0 when it is not one, see parseFirmware).
type netConsoleFirmware struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Firmware string `json:"firmware"`
	Code     int    `json:"code"`
}

// netUserFirmwares is the firmware of each console of a user that told it.
func (web *Web) netUserFirmwares(user string) []netConsoleFirmware {
	firmwares := []netConsoleFirmware{}
	for _, key := range web.netUserConsoles(user) {
		inventory, ok := web.readNetInventory(key.ID)
		if !ok || inventory.System == nil || inventory.System.Firmware == "" {
			continue
		}
		code, _ := parseFirmware(inventory.System.Firmware)
		firmwares = append(firmwares, netConsoleFirmware{Id: key.ID, Name: key.Name, Firmware: inventory.System.Firmware, Code: code})
	}
	return firmwares
}

// netConsoleGame is a game installed on a console, compared with the library.
type netConsoleGame struct {
	// the title ID of the game, and its name in the library or in the titles database
	TitleId string `json:"titleId"`
	Name    string `json:"name"`
	// the version installed, of the game or of its update, and where
	Version int    `json:"version"`
	Storage string `json:"storage,omitempty"`
	// the library has files of the game, and the newest version of them
	InLibrary bool `json:"inLibrary"`
	Newest    int  `json:"newest"`
	// the DLC installed, and the ones of the library the console lacks
	Dlc        int             `json:"dlc"`
	MissingDlc []netMissingDlc `json:"missingDlc"`
	// the library has a newer update of it, or a DLC the console lacks
	Outdated bool `json:"outdated"`
	// how much it was played on the console, when the console told it
	Play *netPlayStats `json:"play,omitempty"`
}

// netMissingDlc is a DLC of the library a console lacks.
type netMissingDlc struct {
	TitleId string `json:"titleId"`
	Name    string `json:"name"`
	FileId  string `json:"fileId"`
	Size    int64  `json:"size"`
}

// netUpdateFile is a file of the library that brings a console up to date.
type netUpdateFile struct {
	FileId  string `json:"fileId"`
	TitleId string `json:"titleId"`
	Game    string `json:"game"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // update or dlc
	Version int    `json:"version"`
	Size    int64  `json:"size"`
}

// netLibraryGame is what the library has of a game, for compareNetConsole.
type netLibraryGame struct {
	name   string
	newest int
	update *netFile
	dlc    []netFile
}

// netUpdateFileOf is a file of the library as it brings a console up to date.
func netUpdateFileOf(file netFile) netUpdateFile {
	return netUpdateFile{FileId: file.id, TitleId: file.titleId, Game: file.game, Name: netPushName(file),
		Kind: netCatalogKinds[file.kind], Version: file.version, Size: file.size}
}

// compareNetConsole compares what is installed on a console with the files of the library that
// may be sent: each game installed, and the files that bring the console up to date (the newest
// update of a game, when it is newer than the version installed, and the DLC it lacks).
func compareNetConsole(inventory *netInventory, kept []netFile, switchDB *db.SwitchTitlesDB) ([]netConsoleGame, []netUpdateFile) {
	games := map[string]*netConsoleGame{}
	installedDlc := map[string]map[string]bool{}
	for _, item := range inventory.Items {
		prefix := netGamePrefix(item.TitleId)
		if prefix == "" {
			continue
		}
		game, ok := games[prefix]
		if !ok {
			game = &netConsoleGame{TitleId: strings.ToUpper(prefix) + "000", MissingDlc: []netMissingDlc{}}
			games[prefix], installedDlc[prefix] = game, map[string]bool{}
		}
		switch item.Type {
		case "dlc":
			installedDlc[prefix][item.TitleId] = true
		case "game":
			if item.Storage != "" {
				game.Storage = item.Storage
			}
			game.Version = max(game.Version, item.Version)
		default:
			if game.Storage == "" {
				game.Storage = item.Storage
			}
			game.Version = max(game.Version, item.Version)
		}
	}
	library := map[string]*netLibraryGame{}
	for _, file := range kept {
		if games[file.gameKey] == nil {
			continue
		}
		entry, ok := library[file.gameKey]
		if !ok {
			entry = &netLibraryGame{}
			library[file.gameKey] = entry
		}
		if entry.name == "" {
			entry.name = file.game
		}
		switch file.kind {
		case netFolderGames:
			entry.newest = max(entry.newest, file.version)
		case netFolderUpdates:
			entry.newest = max(entry.newest, file.version)
			if entry.update == nil || file.version > entry.update.version {
				update := file
				entry.update = &update
			}
		case netFolderDlc:
			// a DLC without a title ID cannot be told from the ones installed
			if file.titleId != "" {
				entry.dlc = append(entry.dlc, file)
			}
		}
	}
	played := netPlayByGame(inventory.Play)
	list := make([]netConsoleGame, 0, len(games))
	files := []netUpdateFile{}
	for prefix, game := range games {
		game.Dlc = len(installedDlc[prefix])
		entry := library[prefix]
		if entry != nil {
			game.Name, game.InLibrary, game.Newest = entry.name, true, entry.newest
			if entry.update != nil && entry.update.version > game.Version {
				files = append(files, netUpdateFileOf(*entry.update))
				game.Outdated = true
			}
			for _, dlc := range entry.dlc {
				if installedDlc[prefix][dlc.titleId] {
					continue
				}
				game.MissingDlc = append(game.MissingDlc, netMissingDlc{TitleId: dlc.titleId, Name: netPushName(dlc), FileId: dlc.id, Size: dlc.size})
				files = append(files, netUpdateFileOf(dlc))
				game.Outdated = true
			}
		}
		if game.Name == "" && switchDB != nil {
			if known := switchDB.TitlesMap[prefix]; known != nil && usableName(known.Attributes.Name) {
				game.Name = known.Attributes.Name
			}
		}
		game.Play = played[prefix]
		sort.SliceStable(game.MissingDlc, func(i, j int) bool { return game.MissingDlc[i].Name < game.MissingDlc[j].Name })
		list = append(list, *game)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name); a != b {
			// the games without a name after the others
			if a == "" || b == "" {
				return b == ""
			}
			return a < b
		}
		return list[i].TitleId < list[j].TitleId
	})
	order := map[string]int{"update": 0, "dlc": 1}
	sort.SliceStable(files, func(i, j int) bool {
		if a, b := strings.ToLower(files[i].Game), strings.ToLower(files[j].Game); a != b {
			return a < b
		}
		if order[files[i].Kind] != order[files[j].Kind] {
			return order[files[i].Kind] < order[files[j].Kind]
		}
		return files[i].Name < files[j].Name
	})
	return list, files
}

// countNetConsole adds to the summary of a console the games outdated and the size of the files
// that bring them up to date.
func countNetConsole(summary *netInventorySummary, games []netConsoleGame, files []netUpdateFile) {
	outdated, size := 0, int64(0)
	for _, game := range games {
		if game.Outdated {
			outdated++
		}
	}
	for _, file := range files {
		size += file.Size
	}
	summary.Outdated, summary.ToUpdateSize = &outdated, &size
}

// netConsoleDetail is a console of the user compared with the library, for its page.
type netConsoleDetail struct {
	netConsoleRow
	Enabled bool             `json:"enabled"`
	Games   []netConsoleGame `json:"games"`
	// the files that bring it up to date, and whether they fit in the free space of its SD card
	// (missing when the console did not tell it)
	ToUpdate []netUpdateFile `json:"toUpdate"`
	Fits     *bool           `json:"fits"`
}

// netConsoleCompared is what is installed on a console compared with the library, and the files
// of the library by id.
func (web *Web) netConsoleCompared(console KeyRow, options settings.NetInstallOptions) (netConsoleDetail, map[string]netFile) {
	detail := netConsoleDetail{netConsoleRow: netConsoleRow{Id: console.ID, Name: console.Name, Waiting: web.netPushes.waiting(console.ID)},
		Games: []netConsoleGame{}, ToUpdate: []netUpdateFile{}}
	inventory, ok := web.readNetInventory(console.ID)
	if !ok {
		return detail, nil
	}
	kept, byId := web.netFilesKept(options)
	switchDB, _ := web.state.get()
	detail.Games, detail.ToUpdate = compareNetConsole(inventory, kept, switchDB)
	detail.Inventory, detail.System = summarizeNetInventory(inventory), inventory.System
	countNetConsole(detail.Inventory, detail.Games, detail.ToUpdate)
	if inventory.System != nil && inventory.System.SdTotal > 0 {
		fits := *detail.Inventory.ToUpdateSize <= inventory.System.SdFree
		detail.Fits = &fits
	}
	return detail, byId
}

// handleNetConsoleDetail gives a console of the user compared with the library: its games, what
// the library has newer of them and the files that bring it up to date.
func (web *Web) handleNetConsoleDetail(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := netPairer(r)
	if !ok {
		writeGlobalError(w, http.StatusForbidden, lang, errNetPushNotAllow.Error())
		return
	}
	console, ok := web.netUserConsole(principal.Name, mux.Vars(r)["id"])
	if !ok {
		writeGlobalError(w, http.StatusNotFound, lang, errNetPushConsole.Error())
		return
	}
	appSettings := settings.ReadSettings(web.dataFolder)
	detail, _ := web.netConsoleCompared(console, appSettings.NetInstall)
	detail.Enabled = netInstallEnabled(appSettings)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, detail)
}

// handleNetConsoleUpdate sends to a console of the user the files that bring it up to date, as
// many as fit in what may wait for it: it tells how many were sent and how many are left.
func (web *Web) handleNetConsoleUpdate(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	principal, ok := netPairer(r)
	if !ok {
		writeGlobalError(w, http.StatusForbidden, lang, errNetPushNotAllow.Error())
		return
	}
	appSettings := settings.ReadSettings(web.dataFolder)
	if !netInstallEnabled(appSettings) {
		writeGlobalError(w, http.StatusConflict, lang, netPushOffMessage)
		return
	}
	console, ok := web.netUserConsole(principal.Name, mux.Vars(r)["id"])
	if !ok {
		writeGlobalError(w, http.StatusNotFound, lang, errNetPushConsole.Error())
		return
	}
	// optional: {"compressed": true} sends them as Send to console does with Compressed
	var body struct {
		Compressed bool `json:"compressed"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeGlobalError(w, http.StatusBadRequest, lang, "the body is nothing, or {\"compressed\": true}")
		return
	}
	if _, ok := web.readNetInventory(console.ID); !ok {
		writeGlobalError(w, http.StatusConflict, lang, errNetConsoleUnknown.Error())
		return
	}
	detail, byId := web.netConsoleCompared(console, appSettings.NetInstall)
	files := detail.ToUpdate
	if len(files) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"queued": 0, "preparing": 0, "left": 0, "console": console.Name})
		return
	}
	room := netPushMax - web.netPushes.waiting(console.ID) - web.netCopyWaits.count(console.ID)
	if room <= 0 {
		writeGlobalError(w, http.StatusTooManyRequests, lang, errNetPushFull.Error())
		return
	}
	ids := []string{}
	for _, file := range files[:min(room, len(files))] {
		ids = append(ids, file.FileId)
	}
	sent, preparing, status, message := web.netPushFiles(r, principal, console, ids, body.Compressed, byId)
	if status != http.StatusOK {
		writeGlobalError(w, status, lang, message)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queued": sent, "preparing": preparing, "left": len(files) - sent, "console": console.Name})
}
