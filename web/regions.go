package web

import (
	"net/http"
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
	for _, group := range web.buildRegionalDuplicates(lang) {
		for i := range group.Copies {
			if strings.EqualFold(group.Copies[i].Id, id) && len(group.Copies) > 1 {
				copy := group.Copies[i]
				target = &copy
			}
		}
	}
	if target == nil || len(target.files) == 0 {
		return 0, 0, errRegionCopy
	}
	if web.compressionRunning() || !web.state.startSync() {
		return 0, 0, errRegionBusy
	}
	defer web.state.endSync()
	reasons := make([]string, len(target.files))
	for i := range reasons {
		reasons[i] = "other region"
	}
	taskId := web.startTask(TASK_CLEANUP, TRIGGER_MANUAL)
	deleted, freed := 0, int64(0)
	var failure *TaskNote
	for i, op := range process.RemoveFiles(target.files, reasons, false) {
		if op.Error == "" {
			deleted++
			freed += target.files[i].Size
		} else if failure == nil {
			failure = &TaskNote{Text: NOTE_ORGANIZE_FAILED, Detail: op.From + ": " + op.Error}
		}
	}
	web.taskLog().SetCompressResult(taskId, deleted, freed)
	web.finishTask(taskId, failure)
	web.sugarLogger.Infof("[Space] copy %s of another region deleted: %d files, %d bytes", target.Id, deleted, freed)
	return deleted, freed, nil
}

var (
	errRegionCopy = errorString("This copy cannot be deleted: no other copy of the game is in the library.")
	errRegionBusy = errorString("A synchronization is running, try again when it has finished.")
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
			if err == errRegionBusy {
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
