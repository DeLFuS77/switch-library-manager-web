package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/process"
)

// The same game is sold in several stores with a different title ID for each one: the
// European and the American copy are two games for the console. They are found by their
// names (see regionalName) and one copy is suggested to keep.

// RegionCopy is a copy of a game of the library sold in one store.
type RegionCopy struct {
	Id        string
	Name      string
	Region    string
	Languages []string
	// the language of the interface is one of the languages of the game
	HasLanguage bool
	// the update and the DLC of the library for this copy
	LocalUpdate int
	Dlc         int
	Size        int64
	// a file of the copy was found damaged by a check; all of its files are compressed
	Damaged    bool
	Compressed bool
	// the copy to keep, and why
	Keep    bool
	Reasons []string
	// the game, its updates and DLC, deleted together
	files []db.ExtendedFileInfo
}

// RegionGroup is a game of the library with copies of several stores.
type RegionGroup struct {
	Name   string
	Copies []RegionCopy
	// the space of the copies that are not suggested to keep
	Savable int64
}

// reasons to keep a copy, translated by the interface
const (
	REGION_REASON_LANGUAGE  = "In your language"
	REGION_REASON_LANGUAGES = "More languages"
	REGION_REASON_UPDATE    = "Newer update"
	REGION_REASON_DLC       = "More DLC"
	REGION_REASON_SOUND     = "Not damaged"
	REGION_REASON_SMALLER   = "Already compressed"
)

func (web *Web) regionalDuplicates(lang string) []RegionGroup {
	return web.derived("regionalDuplicates:"+lang, func() any { return web.buildRegionalDuplicates(lang) }).([]RegionGroup)
}

func (web *Web) buildRegionalDuplicates(lang string) []RegionGroup {
	switchDB, localDB := web.state.get()
	if switchDB == nil || localDB == nil {
		return nil
	}
	damaged := web.verifications().damaged()
	byName := map[string][]RegionCopy{}
	for prefix, local := range localDB.TitlesMap {
		if !local.BaseExist || local.File.Metadata == nil {
			continue
		}
		title := switchDB.TitlesMap[prefix]
		original := getLocalTitleName(title, local)
		key := regionalName(original)
		if key == "" {
			continue
		}
		copy := RegionCopy{
			Id:          strings.ToUpper(local.File.Metadata.TitleId),
			Name:        titleName(switchDB, lang, local.File.Metadata.TitleId, original),
			LocalUpdate: local.LatestUpdate,
			Dlc:         len(local.Dlc),
			Size:        local.File.ExtendedInfo.Size,
		}
		copy.files = append(copy.files, local.File.ExtendedInfo)
		for _, update := range local.Updates {
			copy.Size += update.ExtendedInfo.Size
			copy.files = append(copy.files, update.ExtendedInfo)
		}
		for _, dlc := range local.Dlc {
			copy.Size += dlc.ExtendedInfo.Size
			copy.files = append(copy.files, dlc.ExtendedInfo)
		}
		copy.Compressed = true
		for _, file := range copy.files {
			path := filepath.Join(file.BaseFolder, file.FileName)
			if _, bad := damaged[path]; bad {
				copy.Damaged = true
			}
			switch strings.ToLower(filepath.Ext(file.FileName)) {
			case ".nsz", ".xcz":
			default:
				copy.Compressed = false
			}
		}
		if title != nil {
			copy.Region = title.Attributes.Region
			copy.Languages = title.Attributes.Languages
			copy.HasLanguage = hasValue(title.Attributes.Languages, lang)
		}
		byName[key] = append(byName[key], copy)
	}

	groups := []RegionGroup{}
	for _, copies := range byName {
		if len(copies) < 2 {
			continue
		}
		groups = append(groups, suggestRegionCopy(copies))
	}
	sort.Slice(groups, func(i, j int) bool { return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name) })
	return groups
}

// suggestRegionCopy marks the copy to keep: never a damaged one, then the one in the language
// of the interface, with more languages, the newer update, more DLC and already compressed.
func suggestRegionCopy(copies []RegionCopy) RegionGroup {
	score := func(c RegionCopy) int {
		value := len(c.Languages)*10 + c.Dlc
		if c.HasLanguage {
			value += 1000
		}
		if c.Damaged {
			value -= 10000
		}
		if c.LocalUpdate > 0 {
			value += 5
		}
		if c.Compressed {
			value += 2
		}
		return value
	}
	sort.SliceStable(copies, func(i, j int) bool {
		if score(copies[i]) != score(copies[j]) {
			return score(copies[i]) > score(copies[j])
		}
		return copies[i].Id < copies[j].Id
	})
	best := &copies[0]
	best.Keep = true
	others := copies[1:]
	reason := func(text string, better func(other RegionCopy) bool) {
		for _, other := range others {
			if !better(other) {
				return
			}
		}
		best.Reasons = append(best.Reasons, text)
	}
	reason(REGION_REASON_SOUND, func(other RegionCopy) bool { return !best.Damaged && other.Damaged })
	reason(REGION_REASON_LANGUAGE, func(other RegionCopy) bool { return best.HasLanguage && !other.HasLanguage })
	reason(REGION_REASON_LANGUAGES, func(other RegionCopy) bool { return len(best.Languages) > len(other.Languages) })
	reason(REGION_REASON_UPDATE, func(other RegionCopy) bool { return best.LocalUpdate > other.LocalUpdate })
	reason(REGION_REASON_DLC, func(other RegionCopy) bool { return best.Dlc > other.Dlc })
	reason(REGION_REASON_SMALLER, func(other RegionCopy) bool { return best.Compressed && !other.Compressed })

	group := RegionGroup{Name: best.Name, Copies: copies}
	for _, other := range others {
		group.Savable += other.Size
	}
	return group
}

// removeRegionCopy deletes a copy of a game, with its updates and DLC, when another copy of the
// same game stays in the library. Only the copies listed on the Space page can be deleted.
func (web *Web) removeRegionCopy(id string, lang string) (int, int64, error) {
	var target *RegionCopy
	var others []RegionCopy
	for _, group := range web.buildRegionalDuplicates(lang) {
		for i := range group.Copies {
			if strings.EqualFold(group.Copies[i].Id, id) && len(group.Copies) > 1 {
				copy := group.Copies[i]
				target = &copy
				others = append(append([]RegionCopy{}, group.Copies[:i]...), group.Copies[i+1:]...)
			}
		}
	}
	if target == nil || len(target.files) == 0 {
		return 0, 0, errRegionCopy
	}
	if !web.soundCopyStays(*target, others) {
		return 0, 0, errRegionVerify
	}
	files := web.regionFilesToDelete(*target)
	if len(files) == 0 {
		return 0, 0, errRegionCopy
	}
	reasons := make([]string, len(files))
	for i := range reasons {
		reasons[i] = "other region"
	}
	deleted, freed, err := web.removeFilesAsTask(files, reasons)
	if err != nil {
		return 0, 0, err
	}
	web.sugarLogger.Infof("[Space] copy %s of another region deleted: %d files, %d bytes", target.Id, deleted, freed)
	return deleted, freed, nil
}

// soundCopyStays tells whether a copy of the game from another region stays, checked and
// sound: two games of the same name and region are different games, not copies, and a
// copy is never deleted while the one that stays could be damaged.
func (web *Web) soundCopyStays(target RegionCopy, others []RegionCopy) bool {
	store := web.verifications()
	for _, other := range others {
		if other.Damaged || len(other.files) == 0 || other.Region == "" || strings.EqualFold(other.Region, target.Region) {
			continue
		}
		base := other.files[0]
		path := filepath.Join(base.BaseFolder, base.FileName)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if record, ok := store.current(path, info); ok && record.sound() {
			return true
		}
	}
	return false
}

// regionFilesToDelete are the files of a copy, each once, without the ones that also hold
// another game (a collection on one card).
func (web *Web) regionFilesToDelete(target RegionCopy) []db.ExtendedFileInfo {
	_, localDB := web.state.get()
	others := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}}
	if localDB != nil {
		for prefix, title := range localDB.TitlesMap {
			if title.File.Metadata == nil || !strings.EqualFold(title.File.Metadata.TitleId, target.Id) {
				others.TitlesMap[prefix] = title
			}
		}
	}
	inUse := process.FilesInUse(others)
	files := []db.ExtendedFileInfo{}
	seen := map[string]bool{}
	for _, file := range target.files {
		path := filepath.Join(file.BaseFolder, file.FileName)
		if seen[path] || inUse[path] {
			continue
		}
		seen[path] = true
		files = append(files, file)
	}
	return files
}

var (
	errRegionVerify = errorString("A copy is only deleted while a copy of the game from another region stays, checked and sound: check the files first.")
	errRegionCopy   = errorString("This copy cannot be deleted: no other copy of the game is in the library.")
	errRegionBusy   = errorString("A synchronization is running, try again when it has finished.")
)

// errorString is an error whose text is shown translated.
type errorString string

func (e errorString) Error() string { return string(e) }

func (web *Web) handleRegionRemove() {
	web.router.HandleFunc("/space/regions/remove", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		if !titleIdPattern.MatchString(id) {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid Title ID (16 hexadecimal characters)")
			return
		}
		deleted, freed, err := web.removeRegionCopy(id, lang)
		if err != nil {
			status := http.StatusBadRequest
			if err == errRegionBusy || err == errOrganizeBusy {
				status = http.StatusConflict
			}
			writeGlobalError(w, status, lang, err.Error())
			return
		}
		// the cached library no longer matches the files on disk
		web.Rescan(TRIGGER_ORGANIZE)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "freed": freed})
	}).Methods("POST")
}
