package web

import (
	"net/http"
	"os"
	"path/filepath"
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

type SpacePageData struct {
	GlobalPageData
	Groups []SpaceGroup
	// the size of the library and the space that can be freed now
	LibrarySize  int64
	Reclaimable  int64
	Unverified   int
	Compressible int
	CompressSize int64
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
	checkCompressed := func(file db.ExtendedFileInfo, name string) {
		path := filepath.Join(file.BaseFolder, file.FileName)
		extension := strings.ToLower(filepath.Ext(path))
		if compressed[path] || (extension != ".nsp" && extension != ".xci") {
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
		info, err := os.Stat(candidate.Path)
		if err != nil {
			continue
		}
		checkCompressed(db.ExtendedFileInfo{FileName: filepath.Base(candidate.Path), BaseFolder: filepath.Dir(candidate.Path), Size: info.Size()}, candidate.Name)
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
			checkCompressed(file, name)
			if !compressed[path] {
				add(SPACE_DUPLICATES, SpaceFile{Path: path, Name: name, Size: file.Size, KeptBy: keptBy(skipped.ReasonText), Deletable: true, file: file})
			}
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

// keptBy is the other file named in the reason of a skipped file, e.g.
// "old update file, newer update exist locally (path)".
func keptBy(reason string) string {
	start, end := strings.LastIndex(reason, "("), strings.LastIndex(reason, ")")
	if start < 0 || end <= start {
		return ""
	}
	return reason[start+1 : end]
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
	for _, candidate := range web.compressCandidates() {
		if _, err := os.Stat(switchfs.CompressedPath(candidate.Path)); err != nil {
			data.Compressible++
			data.CompressSize += candidate.Size
		}
	}
	return data
}

func (web *Web) HandleSpace() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/space.html")

	web.router.HandleFunc("/space.html", func(w http.ResponseWriter, r *http.Request) {
		web.render(w, r, templates, web.spacePageData())
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
		for _, group := range web.spaceGroups() {
			for _, file := range group.Files {
				if file.Deletable {
					deletable[file.Path] = file
					reasons[file.Path] = group.Id
				}
			}
		}
		files, why := []db.ExtendedFileInfo{}, []string{}
		var size int64
		for _, path := range r.Form["path"] {
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
