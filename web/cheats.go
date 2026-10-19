package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"github.com/gorilla/mux"
)

// Cheats come from switch-cheats-db, a database of the community updated every day: a map of
// the build ID of every version of every game, and the cheats of each build, as the text
// files Atmosphère reads from sd:/atmosphere/contents/<title ID>/cheats/<build ID>.txt.
// A cheat only works with the build it was made for, so the cheats of a game are those of
// the version the library has: the game, or its latest update.

const (
	CHEATS_ZIP_FILENAME      = "cheats.zip"
	CHEATS_VERSIONS_FILENAME = "cheats.versions.json"
	// the build IDs read from the games of the library, for the versions the database does
	// not know
	CHEATS_BUILDS_FILENAME = "cheats.builds.json"
	// the database is looked at again after this long at most once
	cheatsRefreshInterval = 20 * time.Hour
)

// cheatsSource is where the cheats come from; tests point it elsewhere.
var cheatsSource = struct{ versions, zip, release string }{settings.DEFAULT_CHEATS_VERSIONS_URL, settings.DEFAULT_CHEATS_ZIP_URL, settings.DEFAULT_CHEATS_RELEASE_URL}

// cheatsDB is the cheats database as read: the build ID of each version of the games that
// have cheats, and the cheats of each build.
type cheatsDB struct {
	// title ID → version → build ID, all upper case
	builds map[string]map[int]string
	// title ID → build ID → the text file of its cheats
	files map[string]map[string]*zip.File
	// title ID → version → build ID read from the files of the library
	read map[string]map[int]string
}

// cheatsFile keeps the database read while its files do not change.
type cheatsFile struct {
	mutex    sync.Mutex
	stamp    string
	db       *cheatsDB
	checked  time.Time
	fetching bool
	// one reading of the build IDs of the library at a time
	learning sync.Mutex
}

// cheats returns the cheats database, or nil when it was not downloaded or is turned off.
func (web *Web) cheats() *cheatsDB {
	if settings.ReadSettings(web.dataFolder).CheatsDisabled {
		return nil
	}
	cache := &web.cheatsFile
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	zipPath := filepath.Join(web.dataFolder, CHEATS_ZIP_FILENAME)
	versionsPath := filepath.Join(web.dataFolder, CHEATS_VERSIONS_FILENAME)
	zipInfo, err := os.Stat(zipPath)
	if err != nil {
		return nil
	}
	versionsInfo, err := os.Stat(versionsPath)
	if err != nil {
		return nil
	}
	stamp := fmt.Sprint(zipInfo.Size(), zipInfo.ModTime().UnixNano(), versionsInfo.Size(), versionsInfo.ModTime().UnixNano())
	if readInfo, err := os.Stat(filepath.Join(web.dataFolder, CHEATS_BUILDS_FILENAME)); err == nil {
		stamp += fmt.Sprint(" ", readInfo.Size(), readInfo.ModTime().UnixNano())
	}
	if cache.db != nil && cache.stamp == stamp {
		return cache.db
	}
	loaded, err := readCheats(zipPath, versionsPath)
	if err != nil {
		web.sugarLogger.Warnf("The cheats database cannot be read: %v", err)
		return nil
	}
	loaded.read = web.readBuilds().byTitle()
	cache.db, cache.stamp = loaded, stamp
	return loaded
}

// cheatsPath is the path of a text file of cheats in the zip: contents/<title ID>/cheats/<build ID>.txt
var cheatsPath = regexp.MustCompile(`(?i)(?:^|/)contents/([0-9a-f]{16})/cheats/([0-9a-f]{16})\.txt$`)

// readCheats reads the zip of the cheats, in memory so the file can be replaced while it is
// in use, and the build IDs of the games it has cheats for.
func readCheats(zipPath string, versionsPath string) (*cheatsDB, error) {
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return nil, err
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	result := &cheatsDB{builds: map[string]map[int]string{}, files: map[string]map[string]*zip.File{}}
	for _, file := range archive.File {
		match := cheatsPath.FindStringSubmatch(file.Name)
		if match == nil || file.UncompressedSize64 == 0 || file.UncompressedSize64 > 4<<20 {
			continue
		}
		id, build := strings.ToUpper(match[1]), strings.ToUpper(match[2])
		if result.files[id] == nil {
			result.files[id] = map[string]*zip.File{}
		}
		result.files[id][build] = file
	}

	versionsFile, err := os.Open(versionsPath)
	if err != nil {
		return nil, err
	}
	defer versionsFile.Close()
	var versions map[string]map[string]any
	if err := json.NewDecoder(versionsFile).Decode(&versions); err != nil {
		return nil, err
	}
	for id, byVersion := range versions {
		id = strings.ToUpper(id)
		// only the games that have cheats
		if result.files[id] == nil {
			continue
		}
		builds := map[int]string{}
		for key, value := range byVersion {
			version, err := strconv.Atoi(key)
			build, ok := value.(string)
			if err != nil || !ok || len(build) != 16 {
				continue
			}
			builds[version] = strings.ToUpper(build)
		}
		result.builds[id] = builds
	}
	return result, nil
}

// buildID is the build ID of a version of a game, or "" when it is not known.
func (c *cheatsDB) buildID(id string, version int) string {
	if c == nil {
		return ""
	}
	id = strings.ToUpper(id)
	if build := c.builds[id][version]; build != "" {
		return build
	}
	return c.read[id][version]
}

// hasAny tells whether there are cheats for some version of a game.
func (c *cheatsDB) hasAny(id string) bool {
	return c != nil && len(c.files[strings.ToUpper(id)]) > 0
}

// has tells whether there are cheats for a version of a game.
func (c *cheatsDB) has(id string, version int) bool {
	build := c.buildID(id, version)
	return build != "" && c.files[strings.ToUpper(id)][build] != nil
}

// text is the text file of the cheats of a build of a game.
func (c *cheatsDB) text(id string, build string) (string, error) {
	if c == nil {
		return "", errors.New("no cheats database")
	}
	file := c.files[strings.ToUpper(id)][strings.ToUpper(build)]
	if file == nil {
		return "", os.ErrNotExist
	}
	reader, err := file.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 4<<20))
	return string(data), err
}

// versionsWithCheats are the versions of a game there are cheats for, oldest first.
func (c *cheatsDB) versionsWithCheats(id string) []int {
	if c == nil {
		return nil
	}
	id = strings.ToUpper(id)
	versions := []int{}
	for version, build := range c.builds[id] {
		if c.files[id][build] != nil {
			versions = append(versions, version)
		}
	}
	sort.Ints(versions)
	return versions
}

// cheatName is the name of a cheat, "[Infinite HP]", or the master code, "{Master Code}".
var cheatName = regexp.MustCompile(`^\s*[\[{](.+)[\]}]\s*$`)

// cheatNames are the names of the cheats of a text file, without the line that names the
// game, its version and who made them.
func cheatNames(text string) []string {
	names := []string{}
	for _, line := range strings.Split(text, "\n") {
		match := cheatName.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		name := strings.TrimSpace(match[1])
		upper := strings.ToUpper(name)
		if name == "" || strings.Contains(upper, "BID:") || strings.Contains(upper, "TID:") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// CheatsInfo is what the game page shows of the cheats of a game.
type CheatsInfo struct {
	// the version the library has, its build ID, and the names of its cheats
	Version int
	BuildID string
	Names   []string
	// the versions there are cheats for, when none is for the version the library has
	OtherVersions []int
}

// cheatsInfo gives the cheats of a game of the library at the version it has, or nil when
// the database knows of none.
func (web *Web) cheatsInfo(id string, version int) *CheatsInfo {
	cheats := web.cheats()
	if cheats == nil {
		return nil
	}
	if build := cheats.buildID(id, version); build != "" && cheats.has(id, version) {
		text, err := cheats.text(id, build)
		if err != nil {
			return nil
		}
		return &CheatsInfo{Version: version, BuildID: build, Names: cheatNames(text)}
	}
	if others := cheats.versionsWithCheats(id); len(others) > 0 {
		return &CheatsInfo{Version: version, OtherVersions: others}
	}
	return nil
}

// downloadCheats gets the cheats database again when it changed, at most once in a while:
// the map of build IDs, and the zip of the cheats when its release changed.
func (web *Web) downloadCheats() {
	settingsObj := settings.ReadSettings(web.dataFolder)
	if settingsObj.CheatsDisabled {
		return
	}
	cache := &web.cheatsFile
	cache.mutex.Lock()
	if cache.fetching || time.Since(cache.checked) < cheatsRefreshInterval {
		cache.mutex.Unlock()
		// the files of the library may have changed since
		web.learnBuildIds()
		return
	}
	cache.fetching = true
	cache.mutex.Unlock()
	defer func() {
		cache.mutex.Lock()
		cache.fetching = false
		cache.mutex.Unlock()
	}()

	etags := settingsObj.LocalizedTitlesEtags
	versionsPath := filepath.Join(web.dataFolder, CHEATS_VERSIONS_FILENAME)
	versionsEtag := etags["cheats-versions"]
	if _, err := os.Stat(versionsPath); err != nil {
		versionsEtag = ""
	}
	versions, err := db.LoadAndUpdate([]string{cheatsSource.versions}, versionsPath, versionsEtag)
	if err != nil {
		web.sugarLogger.Infof("The cheats database is not available: %v", err)
		return
	}
	versions.File.Close()

	// the zip changes with each release of the database, told by a small file
	zipPath := filepath.Join(web.dataFolder, CHEATS_ZIP_FILENAME)
	release, err := db.FetchSmall(cheatsSource.release, 64)
	release = strings.TrimSpace(release)
	if err != nil || release == "" {
		web.sugarLogger.Infof("The release of the cheats database is not known: %v", err)
		return
	}
	_, missing := os.Stat(zipPath)
	if missing != nil || etags["cheats-release"] != release {
		result, err := db.LoadAndUpdateChecked([]string{cheatsSource.zip}, zipPath, "", checkCheatsZip)
		if err != nil {
			web.sugarLogger.Infof("The cheats were not downloaded: %v", err)
			return
		}
		result.File.Close()
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		if s.LocalizedTitlesEtags == nil {
			s.LocalizedTitlesEtags = map[string]string{}
		}
		s.LocalizedTitlesEtags["cheats-versions"] = versions.Etag
		s.LocalizedTitlesEtags["cheats-release"] = release
	})
	cache.mutex.Lock()
	cache.checked = time.Now()
	cache.mutex.Unlock()
	web.invalidateDerived()
	web.learnBuildIds()
	// games of the library that got cheats are told now, not at the next synchronization
	if err := web.notifyChanges(); err != nil {
		web.sugarLogger.Infof("The notifications were not sent: %v", err)
	}
}

// readBuild is a build ID read from a file of the library, kept while the file stays the same.
type readBuild struct {
	Id      string `json:"id"`
	Version int    `json:"version"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Build   string `json:"build"`
}

// readBuilds are the build IDs read from the files, by path.
type readBuilds map[string]readBuild

func (r readBuilds) byTitle() map[string]map[int]string {
	result := map[string]map[int]string{}
	for _, build := range r {
		if build.Build == "" {
			continue
		}
		if result[build.Id] == nil {
			result[build.Id] = map[int]string{}
		}
		result[build.Id][build.Version] = build.Build
	}
	return result
}

func (web *Web) readBuilds() readBuilds {
	builds := readBuilds{}
	if data, err := os.ReadFile(filepath.Join(web.dataFolder, CHEATS_BUILDS_FILENAME)); err == nil {
		json.Unmarshal(data, &builds)
	}
	return builds
}

// learnBuildIds reads the build ID of the games of the library that have cheats for some
// version, when the database does not know the build of the version the library has. Only
// a few kilobytes of each file are read; the result is kept while the file stays the same.
func (web *Web) learnBuildIds() {
	if !web.cheatsFile.learning.TryLock() {
		return
	}
	defer web.cheatsFile.learning.Unlock()
	cheats := web.cheats()
	_, localDB := web.state.get()
	if cheats == nil || localDB == nil {
		return
	}
	if keys, _ := settings.SwitchKeys(); keys == nil || keys.GetKey("header_key") == "" {
		return
	}
	known := web.readBuilds()
	changed := false
	for _, local := range localDB.TitlesMap {
		if !local.BaseExist || local.File.Metadata == nil {
			continue
		}
		id := strings.ToUpper(local.File.Metadata.TitleId)
		if !cheats.hasAny(id) || cheats.builds[id][local.LatestUpdate] != "" {
			continue
		}
		// the program of the version: the latest update carries the whole ExeFS
		file, programId := local.File, id
		if update, ok := local.Updates[local.LatestUpdate]; ok && local.LatestUpdate > 0 {
			file = update
			if update.Metadata != nil {
				programId = strings.ToUpper(update.Metadata.TitleId)
			}
		}
		path := filepath.Join(file.ExtendedInfo.BaseFolder, file.ExtendedInfo.FileName)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if seen, ok := known[path]; ok && seen.Size == info.Size() && seen.ModTime == info.ModTime().UnixNano() && seen.Version == local.LatestUpdate {
			continue
		}
		build, err := switchfs.ReadBuildID(path, programId)
		if err != nil {
			web.sugarLogger.Debugf("The build ID of %s cannot be read: %v", path, err)
		}
		// kept also when it cannot be read, so the file is not read again until it changes
		known[path] = readBuild{Id: id, Version: local.LatestUpdate, Size: info.Size(), ModTime: info.ModTime().UnixNano(), Build: build}
		changed = true
	}
	if !changed {
		return
	}
	data, err := json.Marshal(known)
	if err == nil {
		err = os.WriteFile(filepath.Join(web.dataFolder, CHEATS_BUILDS_FILENAME), data, 0o644)
	}
	if err != nil {
		web.sugarLogger.Warnf("The build IDs read cannot be saved: %v", err)
		return
	}
	web.invalidateDerived()
}

// checkCheatsZip tells whether a downloaded file is a zip of cheats.
func checkCheatsZip(path string) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, file := range archive.File {
		if cheatsPath.MatchString(file.Name) {
			return nil
		}
	}
	return errors.New("no cheats in the zip")
}

// ownedCheats are the cheats of the games of the library at the versions it has: the title
// ID, the build ID and the name of each game.
type ownedCheat struct {
	Id, Build, Name string
}

func (web *Web) ownedCheats(lang string) []ownedCheat {
	cheats := web.cheats()
	switchDB, localDB := web.state.get()
	result := []ownedCheat{}
	if cheats == nil || localDB == nil {
		return result
	}
	for prefix, local := range localDB.TitlesMap {
		if !local.BaseExist || local.File.Metadata == nil {
			continue
		}
		id := strings.ToUpper(local.File.Metadata.TitleId)
		if !cheats.has(id, local.LatestUpdate) {
			continue
		}
		var title *db.SwitchTitle
		if switchDB != nil {
			title = switchDB.TitlesMap[prefix]
		}
		result = append(result, ownedCheat{Id: id, Build: cheats.buildID(id, local.LatestUpdate), Name: titleName(switchDB, lang, id, getLocalTitleName(title, local))})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Id < result[j].Id })
	return result
}

// writeCheatsZip writes the cheats as the SD card holds them, for Atmosphère.
func writeCheatsZip(w io.Writer, cheats *cheatsDB, owned []ownedCheat) error {
	archive := zip.NewWriter(w)
	for _, game := range owned {
		text, err := cheats.text(game.Id, game.Build)
		if err != nil {
			continue
		}
		entry, err := archive.Create("atmosphere/contents/" + game.Id + "/cheats/" + game.Build + ".txt")
		if err != nil {
			return err
		}
		if _, err := io.WriteString(entry, text); err != nil {
			return err
		}
	}
	return archive.Close()
}

func (web *Web) HandleCheats() {
	// the cheats of a game at the version the library has
	cheatsOf := func(w http.ResponseWriter, r *http.Request) (*cheatsDB, ownedCheat, bool) {
		cheats := web.cheats()
		_, localDB := web.state.get()
		local := findLocalTitle(localDB, mux.Vars(r)["titleId"])
		if cheats == nil || local == nil {
			http.NotFound(w, r)
			return nil, ownedCheat{}, false
		}
		id := strings.ToUpper(local.File.Metadata.TitleId)
		if !cheats.has(id, local.LatestUpdate) {
			http.NotFound(w, r)
			return nil, ownedCheat{}, false
		}
		return cheats, ownedCheat{Id: id, Build: cheats.buildID(id, local.LatestUpdate)}, true
	}

	web.router.HandleFunc("/api/titles/{titleId}/cheats.txt", func(w http.ResponseWriter, r *http.Request) {
		cheats, game, ok := cheatsOf(w, r)
		if !ok {
			return
		}
		text, err := cheats.text(game.Id, game.Build)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(game.Build+".txt"))
		io.WriteString(w, text)
	}).Methods("GET")

	web.router.HandleFunc("/api/titles/{titleId}/cheats.zip", func(w http.ResponseWriter, r *http.Request) {
		cheats, game, ok := cheatsOf(w, r)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote("cheats "+game.Id+".zip"))
		if err := writeCheatsZip(w, cheats, []ownedCheat{game}); err != nil {
			web.sugarLogger.Warnf("Writing the cheats failed: %v", err)
		}
	}).Methods("GET")

	// the cheats of every game of the library, for the SD card
	web.router.HandleFunc("/api/cheats.zip", func(w http.ResponseWriter, r *http.Request) {
		cheats := web.cheats()
		owned := web.ownedCheats(web.requestLanguage(r))
		if cheats == nil || len(owned) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote("cheats.zip"))
		if err := writeCheatsZip(w, cheats, owned); err != nil {
			web.sugarLogger.Warnf("Writing the cheats failed: %v", err)
		}
	}).Methods("GET")
}
