package web

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// the largest keys file accepted: a prod.keys holds a few hundred lines, a title.keys a few
// thousand
const maxKeysUpload = 1 << 20

var (
	keyLine      = regexp.MustCompile(`^([A-Za-z0-9_]+)\s*=\s*([0-9A-Fa-f]+)$`)
	titleKeyLine = regexp.MustCompile(`^[0-9A-Fa-f]{32}\s*=\s*[0-9A-Fa-f]{32}$`)

	errNotProdKeys  = errors.New("This is not a prod.keys file: it needs the header_key and the key_area_key_application keys of your console.")
	errNotTitleKeys = errors.New("This is not a title.keys file: each line is a rights ID and its title key.")
)

// TITLE_KEYS_HINT is where the app looks for the title keys, told when it has none
const TITLE_KEYS_HINT = "Optional, for games without a ticket. Name the file exactly title.keys and put it next to the prod.keys in use, with one rightsId = key per line, or upload it in Settings."

// checkProdKeys tells whether data is a prod.keys file the app can read games with: lines of
// "name = hex" with the header key and at least one key area key of applications.
func checkProdKeys(data []byte) error {
	names := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		match := keyLine.FindStringSubmatch(line)
		if match == nil {
			return errNotProdKeys
		}
		names[strings.ToLower(match[1])] = true
	}
	if scanner.Err() != nil || !names["header_key"] || !names["key_area_key_application_00"] {
		return errNotProdKeys
	}
	return nil
}

// checkTitleKeys tells whether data is a title.keys file: lines of "rights ID = title key".
func checkTitleKeys(data []byte) error {
	lines := 0
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if !titleKeyLine.MatchString(line) {
			return errNotTitleKeys
		}
		lines++
	}
	if scanner.Err() != nil || lines == 0 {
		return errNotTitleKeys
	}
	return nil
}

// writePrivateFile replaces a file at once, readable by the owner only: the keys are never left
// half written, nor readable by the other users of the system.
func writePrivateFile(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".keys-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return durable.CommitFile(temporary.Name(), path)
}

// readUpload reads a file of the form, at most maxKeysUpload bytes; nil when it was not sent.
func readUpload(r *http.Request, field string) ([]byte, error) {
	file, _, err := r.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxKeysUpload+1))
}

// HandleKeysUpload saves the prod.keys (and title.keys) sent from Settings in the data folder,
// where the app looks for them, reads them and scans the library again. A title.keys can come
// alone when a prod.keys is in use: the app reads the one of the data folder too. Their contents
// are never logged nor shown; only administrators can send them (a POST), never in the demo.
func (web *Web) HandleKeysUpload() {
	web.router.HandleFunc("/settings/keys", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		r.Body = http.MaxBytesReader(w, r.Body, 3*maxKeysUpload)
		prodKeys, err := readUpload(r, "prod_keys")
		if err != nil || len(prodKeys) > maxKeysUpload {
			writeGlobalError(w, http.StatusBadRequest, lang, i18n.Translate(lang, "Choose your prod.keys file."))
			return
		}
		titleKeys, err := readUpload(r, "title_keys")
		if err != nil || len(titleKeys) > maxKeysUpload {
			writeGlobalError(w, http.StatusBadRequest, lang, i18n.Translate(lang, errNotTitleKeys.Error()))
			return
		}
		if prodKeys == nil && (titleKeys == nil || !settings.IsKeysFileAvailable()) {
			writeGlobalError(w, http.StatusBadRequest, lang, i18n.Translate(lang, "Choose your prod.keys file."))
			return
		}
		if prodKeys != nil {
			if err := checkProdKeys(prodKeys); err != nil {
				writeGlobalError(w, http.StatusBadRequest, lang, i18n.Translate(lang, err.Error()))
				return
			}
		}
		if titleKeys != nil {
			if err := checkTitleKeys(titleKeys); err != nil {
				writeGlobalError(w, http.StatusBadRequest, lang, i18n.Translate(lang, err.Error()))
				return
			}
		}

		if prodKeys != nil {
			if err := writePrivateFile(filepath.Join(web.dataFolder, "prod.keys"), prodKeys); err != nil {
				web.sugarLogger.Errorf("[Keys] could not be saved: %v", err)
				writeGlobalError(w, http.StatusInternalServerError, lang, i18n.Translate(lang, "The keys could not be saved in the data folder."))
				return
			}
		}
		if titleKeys != nil {
			if err := writePrivateFile(filepath.Join(web.dataFolder, "title.keys"), titleKeys); err != nil {
				web.sugarLogger.Errorf("[Keys] title.keys could not be saved: %v", err)
				writeGlobalError(w, http.StatusInternalServerError, lang, i18n.Translate(lang, "The keys could not be saved in the data folder."))
				return
			}
		}
		// the keys of the data folder are the ones in use, whatever folder Settings named before;
		// a title.keys alone goes with the prod.keys in use
		if prodKeys != nil {
			settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Prodkeys = web.dataFolder })
		}
		if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
			web.sugarLogger.Errorf("[Keys] the keys sent could not be read: %v", err)
		}
		web.sugarLogger.Infof("[Keys] new keys saved in the data folder from Settings")

		message := i18n.Translate(lang, "Your keys are saved in the data folder of the app. The library is being rescanned.")
		if !web.Rescan(tasklog.TRIGGER_SETTINGS) {
			message = i18n.Translate(lang, "Your keys are saved in the data folder of the app. They will be used by the next synchronization.")
		}
		answer := map[string]string{"strongMessage": i18n.Translate(lang, "Success!"), "message": message}
		if prodKeys != nil {
			// the folder of the keys now, for the field of the wizard
			answer["folder"] = web.dataFolder
		}
		writeJSON(w, http.StatusOK, answer)
	}).Methods("POST")
}
