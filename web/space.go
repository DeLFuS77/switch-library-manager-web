package web

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// groups of files that can be deleted to free space
const (
	SPACE_OLD_UPDATES = "old_updates"
	SPACE_DUPLICATES  = "duplicates"
	SPACE_COMPRESSED  = "compressed"
	// a compressed copy that was not verified yet
	NOTE_SPACE_VERIFY = "Verify the compressed copy first."
	// duplicates are deleted only when the copy that stays is known to be sound
	NOTE_SPACE_VERIFY_KEPT  = "Verify the copy that is kept first."
	NOTE_SPACE_KEPT_DAMAGED = "The copy that is kept is damaged: keep this one."
	NOTE_SPACE_KEPT_MISSING = "The copy that is kept was not found; rescan the library."
)

// SpaceFile is a file that takes space without being needed.
type SpaceFile struct {
	Path string
	Name string
	Size int64
	// the file the space is kept by, e.g. the newer update or the compressed copy
	KeptBy    string
	Deletable bool
	Note      string
	file      db.ExtendedFileInfo
}

type SpaceGroup struct {
	Id    string
	Files []SpaceFile
	Size  int64
	// the size of the files that can be deleted now
	DeletableSize int64
}

// shownFiles is how many files of a group are listed one by one; a big library can have
// thousands, which would make the page huge.
const shownFiles = 300

// Shown are the biggest files of the group, listed one by one.
func (g SpaceGroup) Shown() []SpaceFile {
	if len(g.Files) > shownFiles {
		return g.Files[:shownFiles]
	}
	return g.Files
}

// Rest are the deletable files that are not listed one by one: their number and size.
func (g SpaceGroup) Rest() (int, int64) {
	count, size := 0, int64(0)
	if len(g.Files) > shownFiles {
		for _, file := range g.Files[shownFiles:] {
			if file.Deletable {
				count++
				size += file.Size
			}
		}
	}
	return count, size
}

func (g SpaceGroup) RestCount() int {
	count, _ := g.Rest()
	return count
}

func (g SpaceGroup) RestSize() int64 {
	_, size := g.Rest()
	return size
}

type SpacePageData struct {
	GlobalPageData
	Groups []SpaceGroup
	// the size of the library and the space that can be freed now
	LibrarySize  int64
	Reclaimable  int64
	Unverified   int
	Compressible int
	CompressSize int64
	// games of the library with copies of several stores
	Regions []RegionGroup
}

// RemovablePercent is the share of the library that can be freed.
func (d SpacePageData) RemovablePercent() int {
	if d.LibrarySize == 0 {
		return 0
	}
	return int(d.Reclaimable * 100 / d.LibrarySize)
}

// spaceGroups finds old updates, duplicates and originals that already have a compressed
// copy. Originals can only be deleted once their compressed copy was verified.
func (web *Web) spaceGroups() []SpaceGroup {
	_, localDB := web.state.get()
	groups := map[string]*SpaceGroup{}
	for _, id := range []string{SPACE_OLD_UPDATES, SPACE_DUPLICATES, SPACE_COMPRESSED} {
		groups[id] = &SpaceGroup{Id: id, Files: []SpaceFile{}}
	}
	if localDB == nil {
		return []SpaceGroup{*groups[SPACE_OLD_UPDATES], *groups[SPACE_DUPLICATES], *groups[SPACE_COMPRESSED]}
	}
	add := func(id string, file SpaceFile) {
		group := groups[id]
		group.Files = append(group.Files, file)
		group.Size += file.Size
		if file.Deletable {
			group.DeletableSize += file.Size
		}
	}

	// originals with a compressed copy next to them, from the library and the duplicates
	compressed := map[string]bool{}
	copies := web.compressedCopies()
	checkCompressed := func(file db.ExtendedFileInfo, name string, known bool) {
		path := filepath.Join(file.BaseFolder, file.FileName)
		extension := strings.ToLower(filepath.Ext(path))
		if compressed[path] || (extension != ".nsp" && extension != ".xci") {
			return
		}
		// files of the library were looked up once for this scan; duplicates are asked
		if known && !copies[path] {
			return
		}
		copyPath := switchfs.CompressedPath(path)
		info, err := os.Stat(copyPath)
		if err != nil {
			return
		}
		compressed[path] = true
		entry := SpaceFile{Path: path, Name: name, Size: file.Size, KeptBy: copyPath, file: file}
		if record, ok := web.verifications().current(copyPath, info); ok && record.OK {
			entry.Deletable = true
		} else {
			entry.Note = NOTE_SPACE_VERIFY
		}
		add(SPACE_COMPRESSED, entry)
	}
	for _, candidate := range web.compressCandidates() {
		if !copies[candidate.Path] {
			continue
		}
		checkCompressed(db.ExtendedFileInfo{FileName: filepath.Base(candidate.Path), BaseFolder: filepath.Dir(candidate.Path), Size: candidate.Size}, candidate.Name, true)
	}

	for file, skipped := range localDB.Skipped {
		path := filepath.Join(file.BaseFolder, file.FileName)
		name := strings.TrimSpace(db.ParseTitleNameFromFileName(file.FileName))
		if name == "" {
			name = file.FileName
		}
		switch skipped.ReasonCode {
		case db.REASON_OLD_UPDATE:
			add(SPACE_OLD_UPDATES, SpaceFile{Path: path, Name: name, Size: file.Size, KeptBy: keptBy(skipped.ReasonText), Deletable: true, file: file})
		case db.REASON_DUPLICATE:
			checkCompressed(file, name, false)
			if compressed[path] {
				continue
			}
			entry := SpaceFile{Path: path, Name: name, Size: file.Size, KeptBy: keptBy(skipped.ReasonText), Deletable: true, file: file}
			// the copy is deleted, not the file with the clean name, even when the copy was
			// found first and is the one in the library
			if entry.KeptBy != "" && isCopyOf(fileBase(entry.KeptBy), file.FileName) {
				if info, err := os.Stat(entry.KeptBy); err == nil && !info.IsDir() {
					entry.Path, entry.KeptBy = entry.KeptBy, path
					entry.Size = info.Size()
					entry.file = db.ExtendedFileInfo{FileName: filepath.Base(entry.Path), BaseFolder: filepath.Dir(entry.Path), Size: info.Size()}
				}
			}
			web.requireSoundKeptCopy(&entry)
			add(SPACE_DUPLICATES, entry)
		}
	}

	result := []SpaceGroup{}
	for _, id := range []string{SPACE_OLD_UPDATES, SPACE_DUPLICATES, SPACE_COMPRESSED} {
		group := groups[id]
		sort.Slice(group.Files, func(i, j int) bool {
			if group.Files[i].Size == group.Files[j].Size {
				return group.Files[i].Path < group.Files[j].Path
			}
			return group.Files[i].Size > group.Files[j].Size
		})
		result = append(result, *group)
	}
	return result
}

// requireSoundKeptCopy lets a duplicate be deleted only when the copy that stays was
// verified and found sound: the two files are the same game and version, but one of them
// could be damaged.
func (web *Web) requireSoundKeptCopy(entry *SpaceFile) {
	if entry.KeptBy == "" {
		entry.Deletable = false
		entry.Note = NOTE_SPACE_KEPT_MISSING
		return
	}
	info, err := os.Stat(entry.KeptBy)
	if err != nil || info.IsDir() {
		entry.Deletable = false
		entry.Note = NOTE_SPACE_KEPT_MISSING
		return
	}
	record, ok := web.verifications().current(entry.KeptBy, info)
	switch {
	case !ok:
		entry.Deletable = false
		entry.Note = NOTE_SPACE_VERIFY_KEPT
	case !record.OK:
		entry.Deletable = false
		entry.Note = NOTE_SPACE_KEPT_DAMAGED
	}
}

// spaceVerifyPaths are the files that stay and must be verified before the files they
// replace can be deleted: compressed copies and kept duplicates.
func (web *Web) spaceVerifyPaths() []string {
	paths := []string{}
	seen := map[string]bool{}
	for _, group := range web.spaceGroups() {
		for _, file := range group.Files {
			if (file.Note == NOTE_SPACE_VERIFY || file.Note == NOTE_SPACE_VERIFY_KEPT) && !seen[file.KeptBy] {
				seen[file.KeptBy] = true
				paths = append(paths, file.KeptBy)
			}
		}
	}
	return paths
}

// keptBy is the other file named in the reason of a skipped file, e.g.
// "old update file, newer update exist locally (path)". File names may have parentheses
// themselves, e.g. "Game [v0](2).nsz".
func keptBy(reason string) string {
	if match := trailingPath.FindStringSubmatch(reason); match != nil {
		return match[1]
	}
	return ""
}

// a copy made by a file manager or a download tool: "Game(2).nsz", "Game - Copy.nsz"
var copySuffix = regexp.MustCompile(`(?i)^(.*?)\s*(\(\d+\)|- (copy|copia|kopie|copie)( \(\d+\))?)(\.[^.]+)$`)

// isCopyOf reports whether the file name copy is name with a copy suffix.
func isCopyOf(copy string, name string) bool {
	match := copySuffix.FindStringSubmatch(copy)
	return match != nil && strings.EqualFold(match[1]+match[5], name)
}

func (web *Web) spacePageData() SpacePageData {
	data := SpacePageData{GlobalPageData: web.globalPageData("space"), Groups: web.spaceGroups()}
	for _, group := range data.Groups {
		data.Reclaimable += group.DeletableSize
		for _, file := range group.Files {
			if !file.Deletable {
				data.Unverified++
			}
		}
	}
	_, localDB := web.state.get()
	if localDB != nil {
		for _, candidate := range web.libraryFiles(".nsp", ".nsz", ".xci", ".xcz") {
			data.LibrarySize += candidate.Size
		}
		for file := range localDB.Skipped {
			data.LibrarySize += file.Size
		}
	}
	for _, candidate := range web.uncompressedCandidates() {
		data.Compressible++
		data.CompressSize += candidate.Size
	}
	return data
}

func (web *Web) HandleSpace() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/space.html")

	web.router.HandleFunc("/space.html", func(w http.ResponseWriter, r *http.Request) {
		data := web.spacePageData()
		data.Regions = web.regionalDuplicates(web.requestLanguage(r))
		web.render(w, r, templates, data)
	}).Methods("GET")

	web.router.HandleFunc("/space/clean", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := r.ParseForm(); err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		// only files listed on the page, and deletable now, are deleted
		deletable := map[string]SpaceFile{}
		reasons := map[string]string{}
		requested := r.Form["path"]
		wholeGroups := map[string]bool{}
		for _, id := range r.Form["rest"] {
			wholeGroups[id] = true
		}
		for _, group := range web.spaceGroups() {
			for i, file := range group.Files {
				if file.Deletable {
					deletable[file.Path] = file
					reasons[file.Path] = group.Id
					// "the rest of the group": the files not listed one by one
					if i >= shownFiles && wholeGroups[group.Id] {
						requested = append(requested, file.Path)
					}
				}
			}
		}
		files, why := []db.ExtendedFileInfo{}, []string{}
		var size int64
		for _, path := range requested {
			if file, ok := deletable[path]; ok {
				files = append(files, file.file)
				why = append(why, reasons[path])
				size += file.Size
				delete(deletable, path)
			}
		}
		if len(files) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Select the files to delete.")
			return
		}
		// never delete files while the library is being scanned or compressed
		if web.compressionRunning() || !web.state.startSync() {
			writeGlobalError(w, http.StatusConflict, lang, "A synchronization is running, try again when it has finished.")
			return
		}
		taskId := web.startTask(TASK_CLEANUP, TRIGGER_MANUAL)
		operations := process.RemoveFiles(files, why, false)
		deleted, freed := 0, int64(0)
		var failure *TaskNote
		for i, op := range operations {
			if op.Error == "" {
				deleted++
				freed += files[i].Size
			} else if failure == nil {
				failure = &TaskNote{Text: NOTE_ORGANIZE_FAILED, Detail: op.From + ": " + op.Error}
			}
		}
		web.taskLog().SetCompressResult(taskId, deleted, freed)
		web.finishTask(taskId, failure)
		web.state.endSync()
		web.sugarLogger.Infof("[Space] %d files deleted, %d bytes freed", deleted, freed)

		// the cached library no longer matches the files on disk
		web.Rescan(TRIGGER_ORGANIZE)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "freed": freed, "requested": size})
	}).Methods("POST")
}
