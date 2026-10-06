package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const (
	// the largest backup accepted, and the largest file in it
	maxBackupSize     = 16 << 20
	maxBackupFileSize = 8 << 20
)

// backupFiles are the files of the data folder kept in a backup: the configuration, the
// users, the verification results and the notified versions. Console keys, the session
// key and the caches are never included.
var backupFiles = []string{settings.SETTINGS_FILENAME, USERS_FILENAME, VERIFY_FILENAME, NOTIFICATIONS_STATE_FILENAME, WISHLIST_FILENAME, ACTIVITY_FILENAME, HISTORY_FILENAME, COLLECTIONS_FILENAME, FAVORITES_FILENAME}

// errors shown to the user, translated by the interface
var (
	ErrBackupInvalid = errors.New("The file is not a backup of this app.")
	ErrBackupFile    = errors.New("The backup contains a damaged file.")
	ErrBackupAdmin   = errors.New("The backup has users but no administrator.")
)

var backupErrors = []error{ErrBackupInvalid, ErrBackupFile, ErrBackupAdmin}

// writeBackup writes a ZIP file with the backup files that exist.
func (web *Web) writeBackup(w io.Writer) error {
	archive := zip.NewWriter(w)
	for _, name := range backupFiles {
		data, err := os.ReadFile(filepath.Join(web.dataFolder, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		file, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return err
		}
		if _, err := file.Write(data); err != nil {
			return err
		}
	}
	return archive.Close()
}

// readBackup returns the files of a backup by name, after checking that each one is
// readable by the app. Files with other names are ignored.
func readBackup(data []byte) (map[string][]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, ErrBackupInvalid
	}
	allowed := map[string]struct{}{}
	for _, name := range backupFiles {
		allowed[name] = struct{}{}
	}
	files := map[string][]byte{}
	for _, entry := range archive.File {
		if _, ok := allowed[entry.Name]; !ok || entry.UncompressedSize64 > maxBackupFileSize {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, ErrBackupFile
		}
		content, err := io.ReadAll(io.LimitReader(reader, maxBackupFileSize+1))
		reader.Close()
		if err != nil || len(content) > maxBackupFileSize {
			return nil, ErrBackupFile
		}
		files[entry.Name] = content
	}
	if _, ok := files[settings.SETTINGS_FILENAME]; !ok {
		return nil, ErrBackupInvalid
	}

	var target any
	for name, content := range files {
		switch name {
		case settings.SETTINGS_FILENAME:
			target = &settings.AppSettings{}
		case USERS_FILENAME:
			target = &[]*User{}
		case VERIFY_FILENAME:
			target = &verifyFile{}
		default:
			target = &map[string]any{}
		}
		if err := json.Unmarshal(content, target); err != nil {
			return nil, ErrBackupFile
		}
	}
	return files, nil
}

// restoreBackup replaces the backup files of the data folder with those of the backup
// and loads them again. Files missing from the backup are left as they are.
func (web *Web) restoreBackup(data []byte) error {
	files, err := readBackup(data)
	if err != nil {
		return err
	}
	if content, ok := files[USERS_FILENAME]; ok {
		users := []*User{}
		json.Unmarshal(content, &users)
		hasAdmin := web.auth != nil && web.auth.users.reservedName != ""
		for _, user := range users {
			if user.Role == ROLE_ADMIN {
				hasAdmin = true
			}
		}
		if len(users) > 0 && !hasAdmin {
			return ErrBackupAdmin
		}
	}

	for _, name := range backupFiles {
		content, ok := files[name]
		if !ok {
			continue
		}
		mode := os.FileMode(0644)
		if name == USERS_FILENAME {
			mode = 0600
		}
		path := filepath.Join(web.dataFolder, name)
		if err := os.WriteFile(path+".tmp", content, mode); err != nil {
			return err
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			return err
		}
	}

	settings.ReloadSettings(web.dataFolder)
	if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
		web.sugarLogger.Debugf("prod.keys not loaded: %s", err)
	}
	if _, ok := files[USERS_FILENAME]; ok && web.auth != nil {
		if err := web.auth.users.reload(); err != nil {
			return err
		}
	}
	if _, ok := files[VERIFY_FILENAME]; ok {
		web.verifications().reload()
	}
	if _, ok := files[ACTIVITY_FILENAME]; ok {
		web.activities().reload()
	}
	if _, ok := files[FAVORITES_FILENAME]; ok {
		web.favorites().reload()
	}
	if _, ok := files[COLLECTIONS_FILENAME]; ok {
		web.collections().reload()
	}
	if _, ok := files[HISTORY_FILENAME]; ok {
		web.history().reload()
	}
	if _, ok := files[WISHLIST_FILENAME]; ok {
		web.wishes().reload()
	}
	web.invalidateDerived()
	return nil
}

func (web *Web) HandleBackup() {
	web.router.HandleFunc("/backup/download", func(w http.ResponseWriter, r *http.Request) {
		buffer := &bytes.Buffer{}
		if err := web.writeBackup(buffer); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		name := "slm-backup-" + time.Now().Format("2006-01-02") + ".zip"
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Write(buffer.Bytes())
	}).Methods("GET")

	web.router.HandleFunc("/backup/restore", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		r.Body = http.MaxBytesReader(w, r.Body, maxBackupSize)
		file, _, err := r.FormFile("backup")
		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, ErrBackupInvalid.Error())
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, ErrBackupInvalid.Error())
			return
		}
		if err := web.restoreBackup(data); err != nil {
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
		web.sugarLogger.Info("[Backup restored]")
		web.Rescan(TRIGGER_SETTINGS)
		writeJSON(w, http.StatusOK, SuccessResponse{
			StrongMessage: translate(lang, "Success!"),
			Message:       translate(lang, "The backup was restored. The library is being rescanned."),
		})
	}).Methods("POST")
}
