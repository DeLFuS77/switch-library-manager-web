package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// files without an icon (or that could not be read) are remembered by path and time, so they
// are not read again after every scan; "Search covers again" forgets them
const noIconFilename = ".noicon.json"

// extractMissingIcons gives the games of the library that have no cover the icon stored in
// their own files: games removed from the eShop, titles the database does not know or
// covers that are gone from the cover server. It needs the keys of the console. The
// progress is shown in the task taskId, or in a new task when it is 0. It returns how many
// icons were found.
func (web *Web) extractMissingIcons(taskId int64) int {
	if keys, _ := settings.SwitchKeys(); keys == nil || keys.GetKey("header_key") == "" {
		return 0
	}
	_, localDB := web.state.get()
	if localDB == nil {
		return 0
	}
	noIcon := web.readNoIcon()

	type candidate struct {
		prefix string
		path   string
		stamp  string
	}
	candidates := []candidate{}
	for prefix, title := range localDB.TitlesMap {
		if title.Icon != "" || !title.BaseExist || title.IsSplit {
			continue
		}
		path := filepath.Join(title.File.ExtendedInfo.BaseFolder, title.File.ExtendedInfo.FileName)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		stamp := info.ModTime().UTC().Format(time.RFC3339Nano)
		if noIcon[path] == stamp {
			continue
		}
		candidates = append(candidates, candidate{prefix: prefix, path: path, stamp: stamp})
	}
	if len(candidates) == 0 {
		return 0
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	if taskId == 0 {
		taskId = web.taskLog().Start(TASK_COVERS, TRIGGER_SCAN)
		defer web.taskLog().Finish(taskId, nil)
	}

	folder := filepath.Join(web.dataFolder, "img")
	os.MkdirAll(folder, 0755)
	found := map[string]coverUpdate{}
	for i, item := range candidates {
		if web.state.IsSynchronizing() {
			break
		}
		web.taskLog().Progress(taskId, i, len(candidates), "Reading icons from the game files")
		icon, err := switchfs.ExtractIcon(item.path)
		if err != nil {
			web.sugarLogger.Debugf("No icon in %s: %v", item.path, err)
			noIcon[item.path] = item.stamp
			continue
		}
		name := "game-" + strings.ToLower(item.prefix) + ".jpg"
		if err := os.WriteFile(filepath.Join(folder, name), icon, 0644); err != nil {
			continue
		}
		found[item.prefix] = coverUpdate{icon: name}
		if len(found)%coverBatchSize == 0 {
			web.state.applyCovers(found)
		}
	}
	if len(found) > 0 {
		web.state.applyCovers(found)
		web.sugarLogger.Infof("[%d icons read from the game files]", len(found))
	}
	web.writeNoIcon(noIcon)
	return len(found)
}

func (web *Web) readNoIcon() map[string]string {
	result := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(web.dataFolder, "img", noIconFilename)); err == nil {
		json.Unmarshal(data, &result)
	}
	return result
}

func (web *Web) writeNoIcon(values map[string]string) {
	if data, err := json.Marshal(values); err == nil {
		os.WriteFile(filepath.Join(web.dataFolder, "img", noIconFilename), data, 0644)
	}
}

// missingCovers counts the games of the library without a cover.
func (web *Web) missingCovers() int {
	_, localDB := web.state.get()
	if localDB == nil {
		return 0
	}
	count := 0
	for _, title := range localDB.TitlesMap {
		if title.BaseExist && title.Icon == "" {
			count++
		}
	}
	return count
}

// HandleCovers lets an administrator search again for every missing cover at once.
func (web *Web) HandleCovers() {
	web.router.HandleFunc("/covers/retry", func(w http.ResponseWriter, r *http.Request) {
		// forget the covers that failed and the files without an icon
		db.ForgetCoverFailures(web.dataFolder)
		web.writeNoIcon(map[string]string{})
		web.startCoverDownloads()
		writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "missing": web.missingCovers()})
	}).Methods("POST")
}
