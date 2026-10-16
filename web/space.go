package web

import (
	"fmt"
	"math"
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
	NOTE_SPACE_VERIFY_KEPT   = "Verify the copy that is kept first."
	NOTE_SPACE_KEPT_DAMAGED  = "The copy that is kept is damaged: keep this one."
	NOTE_SPACE_KEPT_MISSING  = "The copy that is kept was not found; rescan the library."
	NOTE_SPACE_NEWER_DAMAGED = "The newer file is damaged: keep this one."
	NOTE_SPACE_NOT_SAME      = "The compressed file does not hold everything this file holds: keep this one."
	NOTE_SPACE_KEPT_PARTIAL  = "The copy that is kept could not be checked whole: keep this one."
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
	// the content is being prepared: the page says so and loads it when it is ready; or the
	// content shown is the last one, while a newer one is prepared
	Preparing bool
	Updating  bool
}

// RemovablePercent is the share of the library that can be freed.
func (d SpacePageData) RemovablePercent() int {
	if d.LibrarySize == 0 {
		return 0
	}
	return int(d.Reclaimable * 100 / d.LibrarySize)
}

// SpaceShare is the part of a group in the space listed, for the stacked bar.
type SpaceShare struct {
	Id      string
	Percent float64
}

// Shares splits the bar between the groups by what can be freed of each, the colors of the
// legend: the share of the library is often too small to be seen.
func (d SpacePageData) Shares() []SpaceShare {
	total := int64(0)
	for _, group := range d.Groups {
		total += group.DeletableSize
	}
	shares := []SpaceShare{}
	if total == 0 {
		return shares
	}
	for _, group := range d.Groups {
		if group.DeletableSize > 0 {
			shares = append(shares, SpaceShare{Id: group.Id, Percent: math.Round(float64(group.DeletableSize)*1000/float64(total)) / 10})
		}
	}
	return shares
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
		if record, ok := web.verifications().current(copyPath, info); ok && record.sound() {
			// the copy of the same name must hold everything the original holds
			if web.holdsSameContents(path, copyPath, info) {
				entry.Deletable = true
			} else {
				entry.Note = NOTE_SPACE_NOT_SAME
			}
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

	inUse := process.FilesInUse(localDB)
	for file, skipped := range localDB.Skipped {
		path := filepath.Join(file.BaseFolder, file.FileName)
		name := strings.TrimSpace(db.ParseTitleNameFromFileName(file.FileName))
		if name == "" {
			name = file.FileName
		}
		switch skipped.ReasonCode {
		case db.REASON_OLD_UPDATE:
			// a file that also holds the game, its latest update or a DLC is not an old update
			// to free: it is never listed
			if inUse[path] {
				continue
			}
			entry := SpaceFile{Path: path, Name: name, Size: file.Size, KeptBy: keptBy(skipped.ReasonText), Deletable: true, file: file}
			if note := web.newerDamaged(entry.KeptBy); note != "" {
				entry.Deletable = false
				entry.Note = note
			}
			if inUse[path] {
				entry.Deletable = false
				entry.Note = process.REASON_IN_USE
			}
			add(SPACE_OLD_UPDATES, entry)
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
			if inUse[entry.Path] {
				entry.Deletable = false
				entry.Note = process.REASON_IN_USE
			}
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

// holdsSameContents is switchfs.HoldsSameContents, remembered while the two files do not
// change: the Space page lists every pair each time it is shown.
func (web *Web) holdsSameContents(original string, copyPath string, copyInfo os.FileInfo) bool {
	originalInfo, err := os.Stat(original)
	if err != nil {
		return false
	}
	key := fmt.Sprintf("%s|%d|%d|%s|%d|%d", original, originalInfo.Size(), originalInfo.ModTime().UnixNano(),
		copyPath, copyInfo.Size(), copyInfo.ModTime().UnixNano())
	if same, ok := web.sameContents.Load(key); ok {
		return same.(bool)
	}
	same, err := switchfs.HoldsSameContents(original, copyPath)
	same = err == nil && same
	web.sameContents.Store(key, same)
	return same
}

// newerDamaged refuses to delete an old update or DLC whose newer version was found
// damaged: the old one may be the only one that works.
func (web *Web) newerDamaged(newer string) string {
	// the newer file must still be there, moved or deleted by hand since the scan or not
	if info, err := os.Stat(newer); newer == "" || err != nil || info.IsDir() {
		return NOTE_SPACE_KEPT_MISSING
	}
	if web.verifications().foundDamaged(newer) {
		return NOTE_SPACE_NEWER_DAMAGED
	}
	return ""
}

// cleanupRefusal applies the rules of the Space page to the cleanup of Organize and of the
// automations: a duplicate only goes when the copy that stays was checked and is sound, an
// old update only when its newer version is not damaged.
func (web *Web) cleanupRefusal(file db.ExtendedFileInfo, skipped db.SkippedFile) string {
	kept := keptBy(skipped.ReasonText)
	switch skipped.ReasonCode {
	case db.REASON_DUPLICATE:
		entry := SpaceFile{KeptBy: kept, Deletable: true}
		web.requireSoundKeptCopy(&entry)
		if !entry.Deletable {
			return entry.Note
		}
	case db.REASON_OLD_UPDATE:
		return web.newerDamaged(kept)
	}
	return ""
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
	case record.Partial:
		entry.Deletable = false
		entry.Note = NOTE_SPACE_KEPT_PARTIAL
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

// spaceKey names the cached content of the Space page: it follows the library, the settings
// (through derived) and the verification results.
func (web *Web) spaceKey() string {
	store := web.verifications()
	// while a check runs its results change with every file: the content would be worked
	// out again before it is ever done, so it follows the results as they are saved
	if web.verifying() {
		return fmt.Sprintf("space:%d", store.SavedVersion())
	}
	return fmt.Sprintf("space:%d", store.Version())
}

// lastSpaceContent is the content of the Space page last worked out, shown while a newer
// one is prepared, or nil.
func (web *Web) lastSpaceContent() *SpacePageData {
	return web.lastSpace.Load()
}

// spaceContent is the content of the Space page, computed once: it opens every original and
// its compressed copy and looks at many files, which takes a while on a NAS.
func (web *Web) spaceContent() SpacePageData {
	return web.derived(web.spaceKey(), func() any { return web.buildSpaceContent() }).(SpacePageData)
}

// spaceReady tells whether the Space page can be shown at once; if not, its content is
// prepared in the background, once at a time.
func (web *Web) spaceReady() bool {
	if _, ok := web.derivedCached(web.spaceKey()); ok {
		return true
	}
	if web.spacePreparing.CompareAndSwap(false, true) {
		go func() {
			defer web.spacePreparing.Store(false)
			web.spaceContent()
		}()
	}
	return false
}

func (web *Web) spacePageData() SpacePageData {
	data := web.spaceContent()
	data.GlobalPageData = web.globalPageData("space")
	return data
}

func (web *Web) buildSpaceContent() SpacePageData {
	data := SpacePageData{Groups: web.spaceGroups()}
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
	last := data
	web.lastSpace.Store(&last)
	return data
}

func (web *Web) HandleSpace() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/space.html")

	web.router.HandleFunc("/space.html", func(w http.ResponseWriter, r *http.Request) {
		// the page opens at once and says what it is doing while its content is prepared
		if !web.spaceReady() {
			// the content last worked out, while the newer one is prepared
			if last := web.lastSpaceContent(); last != nil {
				data := *last
				data.GlobalPageData = web.globalPageData("space")
				data.Updating = true
				data.Regions = web.regionalDuplicates(web.requestLanguage(r))
				web.render(w, r, templates, data)
				return
			}
			web.render(w, r, templates, SpacePageData{GlobalPageData: web.globalPageData("space"), Preparing: true})
			return
		}
		data := web.spacePageData()
		data.Regions = web.regionalDuplicates(web.requestLanguage(r))
		web.render(w, r, templates, data)
	}).Methods("GET")

	web.router.HandleFunc("/space/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]bool{"ready": web.spaceReady()})
	}).Methods("GET")

	web.handleRegionRemove()

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
		// never delete files while the library is scanned, compressed or checked
		deleted, freed, err := web.removeFilesAsTask(files, why)
		if err != nil {
			writeGlobalError(w, http.StatusConflict, lang, err.Error())
			return
		}
		web.sugarLogger.Infof("[Space] %d files deleted, %d bytes freed", deleted, freed)

		// the cached library no longer matches the files on disk
		web.Rescan(TRIGGER_ORGANIZE)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "freed": freed, "requested": size})
	}).Methods("POST")
}
