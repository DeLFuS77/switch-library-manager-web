package web

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/diskfree"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/gorilla/mux"
)

// The saves of an emulator (Eden, Citron, Sudachi, Ryujinx), uploaded from the page Saves, kept as a
// copy of the game like the ones NXDock uploads, so NXDock restores them on the console; and a save
// of the console taken to an emulator: its ZIP holds the files of the save as they are, which go in
// the folder of saves of the emulator (the page tells where).
//
//   - POST /api/saves/{titleId}?emulator=eden&note=…&folder=1 takes the files of the folder of saves
//     of the emulator (multipart: each "file" with its path in "path", in the same order), or one
//     ZIP of them. With folder=1 the paths begin with the name of the folder dropped, which goes.

// netSaveEmulators are the emulators a save comes from, as the page names them, and the name the
// copy shows as its console; any other is "Emulator".
var netSaveEmulators = map[string]string{"eden": "Eden", "citron": "Citron", "sudachi": "Sudachi", "ryujinx": "Ryujinx"}

// the folders a save of an emulator is often zipped in, which are not part of the save: the
// folder of the game (its title ID) of Eden and its forks, and the folders 0 (the save) and 1
// (the copy being written) of Ryujinx
var netSaveWrapper = regexp.MustCompile(`^([0-9A-Fa-f]{16}|0)$`)

// the files Windows and macOS leave in a folder, which are not part of a save
var netSaveJunk = map[string]bool{".ds_store": true, "thumbs.db": true, "desktop.ini": true}

var errNetSaveEmpty = errors.New("the save has no files")

// emulatorSaveNames are the names the files of a save of an emulator take in its ZIP: relative,
// with "/", without the folder dropped (folder) or a folder the save was zipped in, and of the
// folder 0 alone when the folders 0 and 1 of Ryujinx came. Files the system leaves go (keep tells
// which, by their place in names).
func emulatorSaveNames(names []string, folder bool) (out []string, keep []bool, err error) {
	parts := make([][]string, len(names))
	keep = make([]bool, len(names))
	for i, name := range names {
		name = strings.TrimLeft(strings.ReplaceAll(name, `\`, "/"), "/")
		for strings.HasPrefix(name, "./") {
			name = name[2:]
		}
		if name == "" || strings.Contains(strings.SplitN(name, "/", 2)[0], ":") {
			return nil, nil, errNetSaveNotZip
		}
		split := strings.Split(name, "/")
		for _, part := range split {
			if part == ".." || part == "" {
				return nil, nil, errNetSaveNotZip
			}
		}
		if folder {
			if len(split) < 2 {
				return nil, nil, errNetSaveNotZip
			}
			split = split[1:]
		}
		parts[i] = split
		keep[i] = !netSaveJunk[strings.ToLower(split[len(split)-1])]
	}
	// a folder every file is in, the save was zipped in: it goes, as many times as it is so
	for {
		top, all := "", true
		for i, split := range parts {
			if !keep[i] {
				continue
			}
			if len(split) < 2 || (top != "" && split[0] != top) {
				all = false
				break
			}
			top = split[0]
		}
		if !all || top == "" || !netSaveWrapper.MatchString(top) {
			break
		}
		for i := range parts {
			if keep[i] && len(parts[i]) > 1 {
				parts[i] = parts[i][1:]
			}
		}
	}
	// the folders 0 and 1 of a save of Ryujinx: the files of 0
	zero, onlyRyujinx := false, true
	for i, split := range parts {
		if !keep[i] {
			continue
		}
		if len(split) < 2 || (split[0] != "0" && split[0] != "1") {
			onlyRyujinx = false
			break
		}
		zero = zero || split[0] == "0"
	}
	if onlyRyujinx && zero {
		for i, split := range parts {
			if keep[i] {
				keep[i] = split[0] == "0"
				parts[i] = split[1:]
			}
		}
	}
	out = make([]string, len(names))
	seen := map[string]bool{}
	files := 0
	for i, split := range parts {
		if !keep[i] {
			continue
		}
		out[i] = path.Join(split...)
		if seen[strings.ToLower(out[i])] {
			return nil, nil, fmt.Errorf("two files are named %s", out[i])
		}
		seen[strings.ToLower(out[i])] = true
		files++
	}
	if files == 0 {
		return nil, nil, errNetSaveEmpty
	}
	return out, keep, nil
}

// emulatorSaveSource is a file of a save sent from the page: its name as it came, and how it is
// read.
type emulatorSaveSource struct {
	name string
	open func() (io.ReadCloser, error)
}

// emulatorSaveSources are the files sent: the ones of a ZIP, when one ZIP alone came, or else the
// files themselves, each with its path.
func emulatorSaveSources(files []*multipart.FileHeader, paths []string) ([]emulatorSaveSource, bool, error) {
	if len(files) == 1 && strings.EqualFold(filepath.Ext(files[0].Filename), ".zip") {
		file, err := files[0].Open()
		if err != nil {
			return nil, false, err
		}
		archive, err := zip.NewReader(file, files[0].Size)
		if err != nil {
			file.Close()
			return nil, false, errNetSaveNotZip
		}
		sources := []emulatorSaveSource{}
		for _, entry := range archive.File {
			if entry.FileInfo().IsDir() {
				continue
			}
			sources = append(sources, emulatorSaveSource{name: entry.Name, open: entry.Open})
		}
		// the file stays open while the ZIP is read; the request closes it with its form
		return sources, true, nil
	}
	sources := []emulatorSaveSource{}
	for i, header := range files {
		name := header.Filename
		if i < len(paths) && strings.TrimSpace(paths[i]) != "" {
			name = paths[i]
		}
		header := header
		sources = append(sources, emulatorSaveSource{name: name, open: func() (io.ReadCloser, error) { return header.Open() }})
	}
	return sources, false, nil
}

// handleNetSaveEmulatorUpload keeps a save of an emulator sent from the page Saves, for the user
// signed in.
func (web *Web) handleNetSaveEmulatorUpload(w http.ResponseWriter, r *http.Request) {
	principal, ok := web.netSaver(w, r)
	if !ok {
		return
	}
	lang := web.requestLanguage(r)
	titleId := strings.ToUpper(mux.Vars(r)["titleId"])
	if !netTitleIdPattern.MatchString(titleId) {
		writeGlobalError(w, http.StatusBadRequest, lang, "the title ID is 16 hexadecimal digits")
		return
	}
	query := r.URL.Query()
	emulator := netSaveEmulators[strings.ToLower(strings.TrimSpace(query.Get("emulator")))]
	if emulator == "" {
		emulator = "Emulator"
	}
	r.Body = http.MaxBytesReader(w, r.Body, netSaveMaxSize+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeGlobalError(w, http.StatusRequestEntityTooLarge, lang, "a save is 1 GiB at most")
			return
		}
		writeGlobalError(w, http.StatusBadRequest, lang, "the save could not be read")
		return
	}
	defer r.MultipartForm.RemoveAll()
	sources, zipped, err := emulatorSaveSources(r.MultipartForm.File["file"], r.MultipartForm.Value["path"])
	if err != nil {
		writeGlobalError(w, http.StatusBadRequest, lang, err.Error())
		return
	}
	names := make([]string, len(sources))
	for i, source := range sources {
		names[i] = source.name
	}
	// a folder dropped brings its own name first; a ZIP never does
	inZip, keep, err := emulatorSaveNames(names, !zipped && query.Get("folder") == "1")
	if err != nil {
		writeGlobalError(w, http.StatusBadRequest, lang, err.Error())
		return
	}
	folder := filepath.Join(web.netSavesUserFolder(principal.Name), titleId)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		writeGlobalError(w, http.StatusInternalServerError, lang, "the save could not be kept")
		return
	}
	if free, err := diskfree.Free(folder); err == nil && int64(free) < r.ContentLength+netSaveMinFree {
		writeGlobalError(w, http.StatusInsufficientStorage, lang, errNetSaveSpace.Error())
		return
	}
	temporary, err := os.CreateTemp(folder, ".upload-*.tmp")
	if err != nil {
		writeGlobalError(w, http.StatusInternalServerError, lang, "the save could not be kept")
		return
	}
	defer os.Remove(temporary.Name())
	err = writeEmulatorSaveZip(temporary, sources, inZip, keep)
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = checkNetSaveZip(temporary.Name())
	}
	if err != nil {
		writeGlobalError(w, http.StatusBadRequest, lang, err.Error())
		return
	}
	info, err := os.Stat(temporary.Name())
	if err != nil {
		writeGlobalError(w, http.StatusInternalServerError, lang, "the save could not be kept")
		return
	}
	random := make([]byte, 8)
	rand.Read(random)
	save := netSave{TitleId: titleId, Id: hex.EncodeToString(random), When: time.Now().Unix(), Size: info.Size(),
		Note: netSaveText(query.Get("note"), maxNetSaveNote), Console: emulator, Emulator: true}
	web.netSaves.Lock()
	defer web.netSaves.Unlock()
	saves, err := web.readNetSaves(principal.Name)
	if err == nil {
		err = durable.CommitFile(temporary.Name(), web.netSavePath(principal.Name, titleId, save.Id))
	}
	if err == nil {
		saves = web.pruneNetSaves(principal.Name, append(saves, save), titleId, save.Id)
		if err = web.writeNetSaves(principal.Name, saves); err != nil {
			os.Remove(web.netSavePath(principal.Name, titleId, save.Id))
		}
	}
	if err != nil {
		web.sugarLogger.Warnf("Saves: the save of %s from %s could not be kept: %v", titleId, emulator, err)
		writeGlobalError(w, http.StatusInternalServerError, lang, "the save could not be kept")
		return
	}
	web.sugarLogger.Infof("Saves: %s uploaded a save of %s from %s (%d bytes)", principal.Name, titleId, emulator, save.Size)
	writeJSON(w, http.StatusOK, map[string]any{"id": save.Id, "when": save.When, "size": save.Size})
}

// writeEmulatorSaveZip writes the files kept of a save into a ZIP, by the names they take.
func writeEmulatorSaveZip(out io.Writer, sources []emulatorSaveSource, names []string, keep []bool) error {
	archive := zip.NewWriter(out)
	var total int64
	for i, source := range sources {
		if !keep[i] {
			continue
		}
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: names[i], Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return err
		}
		reader, err := source.open()
		if err != nil {
			return err
		}
		written, err := io.Copy(entry, io.LimitReader(reader, netSaveMaxSize-total+1))
		reader.Close()
		if err != nil {
			return err
		}
		if total += written; total > netSaveMaxSize {
			return errors.New("a save is 1 GiB at most")
		}
	}
	return archive.Close()
}

// NetSaveGame is a game of the library the page offers to take a save of an emulator to.
type NetSaveGame struct {
	TitleId string
	Name    string
}

// netSaveGameChoices are the games of the library, by their name.
func (web *Web) netSaveGameChoices() []NetSaveGame {
	switchDB, localDB := web.state.get()
	games := []NetSaveGame{}
	if localDB == nil {
		return games
	}
	for prefix, title := range localDB.TitlesMap {
		if title == nil || !title.BaseExist {
			continue
		}
		var known *db.SwitchTitle
		if switchDB != nil {
			known = switchDB.TitlesMap[prefix]
		}
		name := ""
		if title.File.Metadata != nil {
			name = getLocalTitleName(known, title)
		}
		if name == "" {
			name = strings.TrimSpace(db.ParseTitleNameFromFileName(title.File.ExtendedInfo.FileName))
		}
		games = append(games, NetSaveGame{TitleId: strings.ToUpper(prefix + "000"), Name: name})
	}
	sort.Slice(games, func(i, j int) bool { return strings.ToLower(games[i].Name) < strings.ToLower(games[j].Name) })
	return games
}
