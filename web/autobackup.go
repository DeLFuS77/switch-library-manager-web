package web

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/gorilla/mux"
)

// The app keeps a copy of its configuration every week in the data folder: settings, users,
// favorites, collections, wishlist and history, like the backup downloaded by hand. The last
// copies are kept, so a mistake can be undone without having made a backup.

const (
	AUTO_BACKUP_FOLDER = "backups"
	autoBackupEvery    = 7 * 24 * time.Hour
	autoBackupsKept    = 5
)

// names of the copies: one per day at most
var autoBackupName = regexp.MustCompile(`^slm-backup-\d{4}-\d{2}-\d{2}\.zip$`)

// AutoBackup is a copy of the configuration made by the app.
type AutoBackup struct {
	Name string
	Size int64
	Time time.Time
}

func (web *Web) autoBackupFolder() string {
	return filepath.Join(web.dataFolder, AUTO_BACKUP_FOLDER)
}

// autoBackups returns the copies, the newest first.
func (web *Web) autoBackups() []AutoBackup {
	backups := []AutoBackup{}
	entries, err := os.ReadDir(web.autoBackupFolder())
	if err != nil {
		return backups
	}
	for _, entry := range entries {
		if entry.IsDir() || !autoBackupName.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		backups = append(backups, AutoBackup{Name: entry.Name(), Size: info.Size(), Time: info.ModTime()})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].Time.After(backups[j].Time) })
	return backups
}

// autoBackupDue reports whether the last copy is a week old, or there is none.
func (web *Web) autoBackupDue(now time.Time) bool {
	backups := web.autoBackups()
	return len(backups) == 0 || now.Sub(backups[0].Time) >= autoBackupEvery
}

// makeAutoBackup writes a copy of the configuration and removes the oldest ones.
func (web *Web) makeAutoBackup(now time.Time) error {
	folder := web.autoBackupFolder()
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	buffer := &bytes.Buffer{}
	if err := web.writeBackup(buffer); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(folder, "slm-backup-"+now.Format("2006-01-02")+".zip"), buffer.Bytes()); err != nil {
		return err
	}
	backups := web.autoBackups()
	for _, old := range backups[min(len(backups), autoBackupsKept):] {
		os.Remove(filepath.Join(folder, old.Name))
	}
	return nil
}

// autoBackupPath returns the path of a copy named in a request, or "" if there is none.
func (web *Web) autoBackupPath(name string) string {
	if !autoBackupName.MatchString(name) {
		return ""
	}
	path := filepath.Join(web.autoBackupFolder(), name)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return ""
	}
	return path
}

func (web *Web) HandleAutoBackups() {
	web.router.HandleFunc("/backup/auto", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := web.makeAutoBackup(time.Now()); err != nil {
			web.sugarLogger.Errorf("Automatic backup failed: %v", err)
			writeGlobalError(w, http.StatusInternalServerError, lang, "The backup could not be made.")
			return
		}
		writeJSON(w, http.StatusOK, SuccessResponse{StrongMessage: translate(lang, "Success!"), Message: translate(lang, "A copy of the configuration was saved.")})
	}).Methods("POST")

	web.router.HandleFunc("/backup/auto/{name}", func(w http.ResponseWriter, r *http.Request) {
		path := web.autoBackupPath(mux.Vars(r)["name"])
		if path == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, path)
	}).Methods("GET")

	web.router.HandleFunc("/backup/auto/{name}/restore", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		path := web.autoBackupPath(mux.Vars(r)["name"])
		if path == "" {
			writeGlobalError(w, http.StatusNotFound, lang, ErrBackupInvalid.Error())
			return
		}
		data, err := os.ReadFile(path)
		if err == nil {
			err = web.restoreBackup(data)
		}
		if err != nil {
			for _, known := range backupErrors {
				if errors.Is(err, known) {
					writeGlobalError(w, http.StatusBadRequest, lang, err.Error())
					return
				}
			}
			web.sugarLogger.Errorf("Restoring a backup failed: %v", err)
			writeGlobalError(w, http.StatusInternalServerError, lang, "The backup could not be restored.")
			return
		}
		web.sugarLogger.Infof("[Backup %s restored]", filepath.Base(path))
		web.Rescan(TRIGGER_SETTINGS)
		writeJSON(w, http.StatusOK, SuccessResponse{StrongMessage: translate(lang, "Success!"), Message: translate(lang, "The backup was restored. The library is being rescanned.")})
	}).Methods("POST")
}
