package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Synchronize downloads the titles database and rescans the library in the background.
// It returns false if a synchronization is already running.
func (web *Web) Synchronize(trigger string) bool {
	if !web.state.startSync() {
		return false
	}
	taskId := web.startTask(TASK_SYNC, trigger)

	go func() {
		var failure *TaskNote
		defer func() { web.finishTask(taskId, failure) }()
		defer web.state.endSync()
		// recorded even if the synchronization fails, so a schedule does not retry every minute
		defer settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			s.LastSyncTime = time.Now()
		})

		currentSwitchDB, _ := web.state.get()

		// pick up a prod.keys file that was replaced while the app was running
		if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
			web.sugarLogger.Debugf("prod.keys not loaded: %s", err)
		}

		switchDB, err := web.buildSwitchDb(currentSwitchDB)
		if err != nil {
			web.sugarLogger.Error(err)
			if currentSwitchDB == nil {
				web.taskLog().Warn(taskId, NOTE_TITLES_DOWNLOAD, err.Error())
			} else {
				web.taskLog().Warn(taskId, NOTE_TITLES_SAVED_COPY, err.Error())
			}
			// keep working with the titles we already have
			switchDB = currentSwitchDB
		}

		localDB, err := web.buildLocalDB(switchDB, true)
		if err != nil {
			web.sugarLogger.Error(err)
			failure = &TaskNote{Text: NOTE_SCAN_FAILED, Detail: err.Error()}
			return
		}
		web.taskLog().SetResult(taskId, len(localDB.TitlesMap), localDB.NumFiles)

		web.state.set(switchDB, localDB)
		if err := web.notifyChanges(); err != nil {
			web.taskLog().Warn(taskId, NOTE_NOTIFY_FAILED, err.Error())
		}
	}()

	return true
}

// Rescan rescans the library in the background without downloading the titles database.
// It returns false if a synchronization is already running.
func (web *Web) Rescan(trigger string) bool {
	return web.scanInBackground(true, trigger)
}

// scanInBackground loads the library (from the cache unless ignoreCache) without blocking,
// reporting progress like a synchronization. It returns false if one is already running.
func (web *Web) scanInBackground(ignoreCache bool, trigger string) bool {
	if !web.state.startSync() {
		return false
	}
	taskId := web.startTask(TASK_SCAN, trigger)

	go func() {
		var failure *TaskNote
		defer func() { web.finishTask(taskId, failure) }()
		defer web.state.endSync()

		switchDB, _ := web.state.get()
		localDB, err := web.buildLocalDB(switchDB, ignoreCache)
		if err != nil {
			web.sugarLogger.Error(err)
			failure = &TaskNote{Text: NOTE_SCAN_FAILED, Detail: err.Error()}
			return
		}
		web.taskLog().SetResult(taskId, len(localDB.TitlesMap), localDB.NumFiles)

		web.state.set(switchDB, localDB)
	}()

	return true
}

func (web *Web) HandleSynchronize() {
	web.router.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) {
		// if a synchronization is already running the client simply follows that one
		web.Synchronize(TRIGGER_MANUAL)
		w.WriteHeader(http.StatusAccepted)
	}).Methods("POST")

	web.router.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		jsonEncoder := json.NewEncoder(w)
		jsonEncoder.Encode(web.state.getProgress())
	}).Methods("GET")
}
