package web

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
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
	// the same, on the Space page, where the user can still choose to delete it
	NOTE_SPACE_KEPT_PARTIAL_ASK = "The copy that is kept was checked without errors, but not whole: an update needs its game for that. Select this one to delete it anyway."
)

// SpaceFile is a file that takes space without being needed.
type SpaceFile struct {
	Path string
	Name string
	Size int64
	// the file the space is kept by, e.g. the newer update or the compressed copy
	KeptBy    string
	Deletable bool
	// not deleted with the others, but the user can choose it: the copy that stays was
	// checked without errors, only not whole
	OnRequest bool
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
	// the files deleted go to the quarantine, and can be put back
	Quarantine bool
	// the size of the library and the space that can be freed now
	LibrarySize  int64
	Reclaimable  int64
	Unverified   int
	Compressible int
	CompressSize int64
	// the files that stay for another reason than a verification still to do: cheats for
	// their version, a damaged newer file, a part the library uses...
	Kept int
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
		// compressed and deleted since the scan, which still has it: nothing to free
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		entry := SpaceFile{Path: path, Name: name, Size: file.Size, KeptBy: copyPath, file: file}
		// as for the copies kept of duplicates: only a copy never checked waits for the check;
		// one checked damaged, or not whole (an update needs its game for that), says so,
		// since checking it again finds the same
		record, ok := web.verifications().current(copyPath, info)
		switch {
		case !ok:
			entry.Note = NOTE_SPACE_VERIFY
		case !record.OK:
			entry.Note = NOTE_SPACE_KEPT_DAMAGED
		case !web.holdsSameContents(path, copyPath, info):
			// the copy of the same name must hold everything the original holds
			entry.Note = NOTE_SPACE_NOT_SAME
		case record.Partial:
			entry.Note = NOTE_SPACE_KEPT_PARTIAL
			entry.OnRequest = true
		default:
			entry.Deletable = true
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
			} else if note := web.keptForCheats(file); note != "" {
				entry.Deletable = false
				entry.Note = note
			}
			add(SPACE_OLD_UPDATES, entry)
		case db.REASON_DUPLICATE:
			checkCompressed(file, name, false)
			if compressed[path] {
				continue
			}
			// the compressed copy of the file it is kept by: the pair is the original and its
			// compressed copy, and the original goes (Compressed originals), never both
			if kept := keptBy(skipped.ReasonText); kept != "" && filepath.Clean(switchfs.CompressedPath(kept)) == filepath.Clean(path) {
				continue
			}
			entry := SpaceFile{Path: path, Name: name, Size: file.Size, KeptBy: keptBy(skipped.ReasonText), Deletable: true, file: file}
			// the copy is deleted, not the file with the clean name, even when the copy was
			// found first and is the one in the library: when the file with the clean name holds
			// all the copy holds (a copy with more in it, a bundle, is the one that stays)
			swapped := false
			if entry.KeptBy != "" && isCopyOf(fileBase(entry.KeptBy), file.FileName) {
				if info, err := os.Stat(entry.KeptBy); err == nil && !info.IsDir() && web.holdsSameContents(entry.KeptBy, path, fileInfoOf(path)) {
					entry.Path, entry.KeptBy = entry.KeptBy, path
					entry.Size = info.Size()
					entry.file = db.ExtendedFileInfo{FileName: filepath.Base(entry.Path), BaseFolder: filepath.Dir(entry.Path), Size: info.Size()}
					swapped = true
				}
			}
			web.requireSoundKeptCopy(&entry)
			if entry.Deletable && !swapped {
				web.requireSoundKeptCopies(&entry)
			}
			// the copy is in use only as the file the scan took: the clean one holds the same
			if inUse[entry.Path] && !swapped {
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

// the pairs of files remembered at most by holdsSameContents
const maxSameContents = 20000

// fileInfoOf is the information of a file, or nil when it cannot be read.
func fileInfoOf(path string) os.FileInfo {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return info
}

// holdsSameContents is switchfs.HoldsSameContents, remembered while the two files do not
// change: the Space page lists every pair each time it is shown.
func (web *Web) holdsSameContents(original string, copyPath string, copyInfo os.FileInfo) bool {
	originalInfo, err := os.Stat(original)
	if err != nil || copyInfo == nil {
		return false
	}
	key := fmt.Sprintf("%s|%d|%d|%s|%d|%d", original, originalInfo.Size(), originalInfo.ModTime().UnixNano(),
		copyPath, copyInfo.Size(), copyInfo.ModTime().UnixNano())
	if same, ok := web.sameContents.Load(key); ok {
		return same.(bool)
	}
	same, err := switchfs.HoldsSameContents(original, copyPath)
	same = err == nil && same
	// a pair that changed has a new key: forgotten all at once now and then, so the old keys
	// do not pile up while the app runs for months
	if web.sameContentsCount.Add(1) > maxSameContents {
		web.sameContents.Clear()
		web.sameContentsCount.Store(1)
	}
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
		entry.file = file
		web.requireSoundKeptCopies(&entry)
		if !entry.Deletable {
			return entry.Note
		}
	case db.REASON_OLD_UPDATE:
		if note := web.newerDamaged(kept); note != "" {
			return note
		}
		return web.keptForCheats(file)
	}
	return ""
}

// requireSoundKeptCopies is requireSoundKeptCopy for a duplicate with several titles: its
// reason names the copy kept of one of them only, and the copies kept of the others must be
// sound too, or deleting it would lose what only it holds sound.
func (web *Web) requireSoundKeptCopies(entry *SpaceFile) {
	copies, read := web.keptCopiesOf(entry.file)
	if !read && entry.Deletable {
		entry.Deletable, entry.Note, entry.OnRequest = false, NOTE_SPACE_UNREAD, false
		return
	}
	for _, other := range copies {
		if other == entry.KeptBy || !entry.Deletable {
			continue
		}
		kept := SpaceFile{KeptBy: other, Deletable: true}
		web.requireSoundKeptCopy(&kept)
		entry.Deletable, entry.Note, entry.OnRequest = kept.Deletable, kept.Note, kept.OnRequest
	}
}

// heldTitle is a title a file holds, with its version.
type heldTitle struct {
	id      string
	version int
}

// NOTE_SPACE_UNREAD is told of a duplicate whose titles could not be read: what only it holds
// cannot be known.
const NOTE_SPACE_UNREAD = "What this file holds could not be read: keep this one."

// heldTitles are the titles a file holds, read once while it does not change; false when the
// file could not be read, which is not kept: it is read again the next time.
func (web *Web) heldTitles(path string) ([]heldTitle, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	key := fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
	if held, ok := web.titlesHeld.Load(key); ok {
		return held.([]heldTitle), true
	}
	var contents map[string]*switchfs.ContentMetaAttributes
	switch strings.ToLower(filepath.Ext(path)) {
	case ".nsp", ".nsz":
		contents, err = switchfs.ReadNspMetadata(path)
	case ".xci", ".xcz":
		contents, err = switchfs.ReadXciMetadata(path)
	}
	if err != nil {
		return nil, false
	}
	held := []heldTitle{}
	for id, content := range contents {
		held = append(held, heldTitle{id: id, version: content.Version})
	}
	web.titlesHeld.Store(key, held)
	return held, true
}

// keptCopiesOf are the files the library keeps for the titles a file with several titles holds
// (a cartridge with its update, a SuperNSP), other than the file itself; none for a file with
// one title. read is false when the titles of the file could not be read.
func (web *Web) keptCopiesOf(file db.ExtendedFileInfo) (copies []string, read bool) {
	path := filepath.Join(file.BaseFolder, file.FileName)
	if file.FileName == "" {
		return nil, true
	}
	held, read := web.heldTitles(path)
	_, localDB := web.state.get()
	if !read || len(held) < 2 || localDB == nil {
		return nil, read
	}
	copies = []string{}
	for _, content := range held {
		id := content.id
		prefix, err := db.TitleIDPrefix(id)
		if err != nil {
			continue
		}
		title := localDB.TitlesMap[strings.ToLower(prefix)]
		if title == nil {
			continue
		}
		var kept db.SwitchFileInfo
		var ok bool
		switch kind, _ := db.ContentKind(id); kind {
		case db.KIND_BASE:
			kept, ok = title.File, title.BaseExist
		case db.KIND_UPDATE:
			kept, ok = title.Updates[content.version]
		case db.KIND_DLC:
			kept, ok = title.Dlc[strings.ToLower(id)]
		}
		if keptPath := filepath.Join(kept.ExtendedInfo.BaseFolder, kept.ExtendedInfo.FileName); ok && keptPath != path {
			copies = append(copies, keptPath)
		}
	}
	return copies, true
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
		entry.OnRequest = true
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
	// a compression gives a result with every file it writes: the page shows the content of
	// when the task began, worked out again once it ends (no cleanup runs meanwhile)
	if version, held := web.spaceHeld(); held {
		return fmt.Sprintf("space:%d", version)
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
	return web.derivedLatest("space:", web.spaceKey(), func() any { return web.buildSpaceContent() }).(SpacePageData)
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
			switch {
			case file.Deletable:
			case file.Note == NOTE_SPACE_VERIFY || file.Note == NOTE_SPACE_VERIFY_KEPT:
				data.Unverified++
			default:
				data.Kept++
			}
		}
	}
	_, localDB := web.state.get()
	if localDB != nil {
		// every file once: old updates are in the library and among the files left out
		counted := map[string]bool{}
		for _, candidate := range web.libraryFiles(".nsp", ".nsz", ".xci", ".xcz") {
			counted[candidate.Path] = true
			data.LibrarySize += candidate.Size
		}
		for file := range localDB.Skipped {
			if !counted[filepath.Join(file.BaseFolder, file.FileName)] {
				data.LibrarySize += file.Size
			}
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
				data.Quarantine = quarantineOn(settings.ReadSettings(web.dataFolder).Quarantine)
				web.render(w, r, templates, data)
				return
			}
			web.render(w, r, templates, SpacePageData{GlobalPageData: web.globalPageData("space"), Preparing: true})
			return
		}
		data := web.spacePageData()
		data.Regions = web.regionalDuplicates(web.requestLanguage(r))
		data.Quarantine = quarantineOn(settings.ReadSettings(web.dataFolder).Quarantine)
		web.render(w, r, templates, data)
	}).Methods("GET")

	web.router.HandleFunc("/space/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]bool{"ready": web.spaceReady()})
	}).Methods("GET")

	web.handleRegionRemove()
	web.handleRegionMain()

	// the files deleted so far by the cleanup running: the page takes them off its list as
	// they go, from the number of them it already took off ("from")
	web.router.HandleFunc("/space/clean/progress", func(w http.ResponseWriter, r *http.Request) {
		from, _ := strconv.Atoi(r.FormValue("from"))
		writeJSON(w, http.StatusOK, map[string]any{"paths": web.removing.since(from), "running": web.organizing.Load()})
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
		// a file deletable on request only goes when it was chosen on its own
		explicit := map[string]bool{}
		for _, path := range requested {
			explicit[path] = true
		}
		for _, group := range web.spaceGroups() {
			for i, file := range group.Files {
				if file.OnRequest && explicit[file.Path] {
					deletable[file.Path] = file
					reasons[file.Path] = group.Id
				}
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
		chosen := map[string]SpaceFile{}
		for _, path := range requested {
			if file, ok := deletable[path]; ok {
				chosen[path] = file
			}
		}
		files, why := []db.ExtendedFileInfo{}, []string{}
		var size int64
		for _, path := range requested {
			file, ok := chosen[path]
			if !ok {
				continue
			}
			// never a file together with the one it is kept by: both would be gone
			if _, alsoChosen := chosen[file.KeptBy]; alsoChosen && file.KeptBy != "" {
				continue
			}
			files = append(files, file.file)
			why = append(why, reasons[path])
			size += file.Size
			delete(chosen, path)
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
