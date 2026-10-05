package web

import (
	"os"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// SetupStatus drives the first-run checklist shown while the library is empty.
type SetupStatus struct {
	TitlesDatabase bool
	Keys           bool
	// configured folders that cannot be read
	MissingFolders []string
	Folders        bool
	Library        bool
	Done           int
	Total          int
	Percent        int
}

func (web *Web) setupStatus() SetupStatus {
	switchDB, localDB := web.state.get()
	status := SetupStatus{
		TitlesDatabase: switchDB != nil,
		Keys:           settings.IsKeysFileAvailable(),
		MissingFolders: []string{},
		Library:        localDB != nil && len(localDB.TitlesMap) > 0,
		Total:          4,
	}

	folders := scanFolders(settings.ReadSettings(web.dataFolder))
	for _, folder := range folders {
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			status.MissingFolders = append(status.MissingFolders, folder)
		}
	}
	status.Folders = len(folders) > 0 && len(status.MissingFolders) == 0

	for _, done := range []bool{status.TitlesDatabase, status.Keys, status.Folders, status.Library} {
		if done {
			status.Done++
		}
	}
	status.Percent = status.Done * 100 / status.Total
	return status
}
