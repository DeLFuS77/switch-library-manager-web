package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const (
	// the largest backup accepted (it holds the covers chosen by hand too), the largest file in
	// it (the verification results of a large library) and all its files together
	maxBackupSize      = 64 << 20
	maxBackupFileSize  = 64 << 20
	maxBackupTotalSize = 256 << 20
)

// backupFiles are the files of the data folder kept in a backup: the configuration, the
// users, the verification results and the notified versions, the copies chosen of the games of
// several regions and what was changed of games by hand. Console keys, the session key and the
// caches are never included.
var backupFiles = []string{settings.SETTINGS_FILENAME, USERS_FILENAME, VERIFY_FILENAME, NOTIFICATIONS_STATE_FILENAME, WISHLIST_FILENAME, activity.ACTIVITY_FILENAME, HISTORY_FILENAME, COLLECTIONS_FILENAME, FAVORITES_FILENAME, PLAY_FILENAME, SMART_COLLECTIONS_FILENAME,
	REGION_CHOICES_FILENAME, TITLE_OVERRIDES_FILENAME}

// backupCover is the name in a backup of a cover chosen by hand, kept in OVERRIDE_COVERS_FOLDER:
// the name of a file alone, never a path.
func backupCover(name string) (string, bool) {
	folder, file := path.Split(name)
	file = filepath.Base(file)
	return file, folder == OVERRIDE_COVERS_FOLDER+"/" && overrideCoverName.MatchString(file)
}

// errors shown to the user, translated by the interface
var (
	ErrBackupInvalid = errors.New("The file is not a backup of this app.")
	ErrBackupFile    = errors.New("The backup contains a damaged file.")
	ErrBackupAdmin   = errors.New("The backup has users but no administrator.")
)

var backupErrors = []error{ErrBackupInvalid, ErrBackupFile, ErrBackupAdmin, ErrBackupPasswordNeeded, ErrBackupPasswordWrong, ErrBackupPasswordShort,
	ErrBackupSetupCode, ErrSetupCode, ErrSetupCodeBlocked}

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
	// the covers chosen by hand, images stored as they are
	for _, override := range web.titleOverrides().snapshot() {
		if override.Cover == "" {
			continue
		}
		data, err := os.ReadFile(web.overrideCoverPath(override.Cover))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		file, err := archive.CreateHeader(&zip.FileHeader{Name: OVERRIDE_COVERS_FOLDER + "/" + override.Cover, Method: zip.Store, Modified: time.Now()})
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
	total := 0
	for _, entry := range archive.File {
		_, known := allowed[entry.Name]
		_, cover := backupCover(entry.Name)
		if !known && !cover {
			continue
		}
		// a file too large or twice is not a backup of the app
		if _, twice := files[entry.Name]; twice || entry.UncompressedSize64 > maxBackupFileSize || cover && entry.UncompressedSize64 > maxOverrideCoverBytes {
			return nil, ErrBackupFile
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
		if total += len(content); total > maxBackupTotalSize {
			return nil, ErrBackupFile
		}
		files[entry.Name] = content
	}
	if _, ok := files[settings.SETTINGS_FILENAME]; !ok {
		return nil, ErrBackupInvalid
	}

	var target any
	for name, content := range files {
		if _, cover := backupCover(name); cover {
			if !backupCoverImage(content) {
				return nil, ErrBackupFile
			}
			continue
		}
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

// backupCoverImage tells a cover of a backup the app made: a JPEG or PNG image of a size it keeps.
func backupCoverImage(data []byte) bool {
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png":
	default:
		return false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	return err == nil && config.Width > 0 && config.Height > 0 && config.Width <= maxOverrideCoverSide && config.Height <= maxOverrideCoverSide
}

// backupSetupCode checks the setup code sent with a backup: one with users, while login is off,
// creates the first ones, and needs the code as creating them in Users does.
func (web *Web) backupSetupCode(given string) func() error {
	return func() error {
		err := web.auth.checkSetupCode(given)
		if err != nil {
			web.setupCodeFailed(err)
		}
		if errors.Is(err, ErrSetupCodeMissing) {
			return ErrBackupSetupCode
		}
		return err
	}
}

// restoreBackup replaces the backup files of the data folder with those of the backup
// and loads them again. Files missing from the backup are left as they are. Users that the
// backup brings while login is off are the first ones: firstUsers checks they may come (the
// setup code), or nil lets them, for the copies the app made itself, which can only hold the
// accounts this app had.
func (web *Web) restoreBackup(data []byte, firstUsers func() error) error {
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
		if len(users) > 0 && web.auth != nil && !web.auth.Enabled() && firstUsers != nil {
			if err := firstUsers(); err != nil {
				return err
			}
		}
		// a backup made before the login was enabled does not disable it
		if len(users) == 0 && web.auth != nil && web.auth.users.Count() > 0 {
			delete(files, USERS_FILENAME)
		}
	}

	// the covers first, so the changes restored find them
	for name, content := range files {
		// the name is checked again where the file is written: a name of the app, inside its folder
		if cover, ok := backupCover(name); ok && overrideCoverName.MatchString(cover) && filepath.IsLocal(cover) {
			if err := os.MkdirAll(filepath.Dir(web.overrideCoverPath(cover)), 0755); err != nil {
				return err
			}
			if err := durable.WriteFile(web.overrideCoverPath(cover), content); err != nil {
				return err
			}
		}
	}
	for _, name := range backupFiles {
		content, ok := files[name]
		if !ok {
			continue
		}
		// the settings hold notification tokens, the users their password hashes
		mode := os.FileMode(0644)
		if name == USERS_FILENAME || name == settings.SETTINGS_FILENAME {
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
		web.auth.forget()
		// users came back: the setup code is not asked any more
		web.auth.endSetupCode()
	}
	if _, ok := files[VERIFY_FILENAME]; ok {
		web.verifications().reload()
	}
	if _, ok := files[activity.ACTIVITY_FILENAME]; ok {
		web.activities().Reload()
	}
	if _, ok := files[FAVORITES_FILENAME]; ok {
		web.favorites().reload()
	}
	if _, ok := files[COLLECTIONS_FILENAME]; ok {
		web.collections().reload()
	}
	if _, ok := files[PLAY_FILENAME]; ok {
		web.play().reload()
	}
	if _, ok := files[SMART_COLLECTIONS_FILENAME]; ok {
		web.smartCollections().reload()
	}
	if _, ok := files[HISTORY_FILENAME]; ok {
		web.history().reload()
		// what came after the backup is not new: no notifications, no automations
		web.history().quietNext()
	}
	if _, ok := files[WISHLIST_FILENAME]; ok {
		web.wishes().reload()
	}
	if _, ok := files[REGION_CHOICES_FILENAME]; ok {
		web.regionChoices().reload()
	}
	if _, ok := files[TITLE_OVERRIDES_FILENAME]; ok {
		web.titleOverrides().reload()
		web.removeUnusedOverrideCovers()
		web.applyTitleOverrides()
	}
	web.invalidateDerived()
	return nil
}

func (web *Web) HandleBackup() {
	// a backup as a ZIP file, or sealed with a password when one is given (sent in the body of
	// the form, never in the address)
	download := func(w http.ResponseWriter, r *http.Request) {
		buffer := &bytes.Buffer{}
		if err := web.writeBackup(buffer); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data, name, kind := buffer.Bytes(), "slm-backup-"+time.Now().Format("2006-01-02")+".zip", "application/zip"
		if password := r.PostFormValue("password"); r.Method == http.MethodPost && password != "" {
			sealed, err := encryptBackup(data, password)
			if err != nil {
				http.Error(w, i18n.Translate(web.requestLanguage(r), err.Error()), http.StatusBadRequest)
				return
			}
			data, name, kind = sealed, strings.TrimSuffix(name, ".zip")+".slmbackup", "application/octet-stream"
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Write(data)
	}
	web.router.HandleFunc("/backup/download", download).Methods("GET", "POST")

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
		// a backup sealed with a password is opened first
		if data, err = decryptBackup(data, r.FormValue("password")); err == nil {
			err = web.restoreBackup(data, web.backupSetupCode(r.FormValue("setup_code")))
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
		web.sugarLogger.Info("[Backup restored]")
		web.Rescan(tasklog.TRIGGER_SETTINGS)
		writeJSON(w, http.StatusOK, SuccessResponse{
			StrongMessage: i18n.Translate(lang, "Success!"),
			Message:       i18n.Translate(lang, "The backup was restored. The library is being rescanned."),
		})
	}).Methods("POST")
}
