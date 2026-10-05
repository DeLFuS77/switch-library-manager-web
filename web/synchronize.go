package web

import (
	"encoding/json"
	"net/http"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Synchronize downloads the titles database and rescans the library in the background.
// It returns false if a synchronization is already running.
func (web *Web) Synchronize() bool {
	if !web.state.startSync() {
		return false
	}

	go func() {
		defer web.state.endSync()

		currentSwitchDB, _ := web.state.get()

		// pick up a prod.keys file that was replaced while the app was running
		if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
			web.sugarLogger.Warnf("Failed to initialize switch keys: %s", err)
		}

		switchDB, err := web.buildSwitchDb()
		if err != nil {
			web.sugarLogger.Error(err)
			// keep working with the titles we already have
			switchDB = currentSwitchDB
		}

		localDB, err := web.buildLocalDB(switchDB, true)
		if err != nil {
			web.sugarLogger.Error(err)
			return
		}

		web.state.set(switchDB, localDB)
	}()

	return true
}

// Rescan rescans the library in the background without downloading the titles database.
// It returns false if a synchronization is already running.
func (web *Web) Rescan() bool {
	if !web.state.startSync() {
		return false
	}

	go func() {
		defer web.state.endSync()

		switchDB, _ := web.state.get()
		localDB, err := web.buildLocalDB(switchDB, true)
		if err != nil {
			web.sugarLogger.Error(err)
			return
		}

		web.state.set(switchDB, localDB)
	}()

	return true
}

func (web *Web) HandleSynchronize() {
	web.router.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) {
		// if a synchronization is already running the client simply follows that one
		web.Synchronize()
		w.WriteHeader(http.StatusAccepted)
	}).Methods("POST")

	web.router.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		jsonEncoder := json.NewEncoder(w)
		jsonEncoder.Encode(web.state.getProgress())
	}).Methods("GET")
}
