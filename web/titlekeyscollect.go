package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"github.com/magiconair/properties"
)

// titleKeysCollectPath takes the title keys of the common tickets of the games of the library into
// the title.keys of the data folder, where the app reads them with the prod.keys in use: another
// copy of a game without its ticket (an NSZ made without it, a dump) can then be read, compressed
// and checked. Only the title keys title.keys lacks are added; the file is read by the owner
// alone, its contents never shown nor logged. Only administrators can ask (a POST).
const titleKeysCollectPath = "/settings/title-keys/collect"

func (web *Web) handleTitleKeysCollect(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	if isDemoMode() {
		writeGlobalError(w, http.StatusForbidden, lang, i18n.Translate(lang, DEMO_DISABLED))
		return
	}
	if !settings.IsKeysFileAvailable() {
		writeGlobalError(w, http.StatusBadRequest, lang, i18n.Translate(lang, "Choose your prod.keys file."))
		return
	}
	added, err := web.collectTitleKeys()
	if err != nil {
		web.sugarLogger.Errorf("[Keys] the title keys of the tickets could not be saved: %v", err)
		writeGlobalError(w, http.StatusInternalServerError, lang, i18n.Translate(lang, "The keys could not be saved in the data folder."))
		return
	}
	message := i18n.Translate(lang, "No new title keys: the tickets of your games hold none that title.keys lacks.")
	if added > 0 {
		web.sugarLogger.Infof("[Keys] %d title keys taken from the tickets of the library", added)
		message = fmt.Sprintf(i18n.Translate(lang, "%v title keys taken from the tickets of your games."), added)
	}
	writeJSON(w, http.StatusOK, map[string]any{"strongMessage": i18n.Translate(lang, "Success!"), "message": message, "added": added})
}

// collectTitleKeys adds to the title.keys of the data folder the title keys of the tickets of the
// library it lacks, and reads the keys again; it tells how many it added.
func (web *Web) collectTitleKeys() (int, error) {
	keys, _ := settings.SwitchKeys()
	path := filepath.Join(web.dataFolder, "title.keys")
	kept := map[string]string{}
	if existing, err := properties.LoadFile(path, properties.UTF8); err == nil {
		for _, rightsId := range existing.Keys() {
			value, _ := existing.Get(rightsId)
			kept[strings.ToLower(strings.TrimSpace(rightsId))] = strings.TrimSpace(value)
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	added := 0
	for _, file := range web.libraryFiles(".nsp", ".nsz") {
		found, err := switchfs.TicketTitleKeys(file.Path)
		if err != nil {
			continue
		}
		for rightsId, key := range found {
			if _, ok := kept[rightsId]; ok {
				continue
			}
			if _, ok := keys.TitleKey(rightsId); ok {
				continue
			}
			kept[rightsId] = key
			added++
		}
	}
	if added == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(kept))
	for rightsId := range kept {
		ids = append(ids, rightsId)
	}
	sort.Strings(ids)
	var data strings.Builder
	for _, rightsId := range ids {
		fmt.Fprintf(&data, "%s = %s\n", rightsId, kept[rightsId])
	}
	if err := writePrivateFile(path, []byte(data.String())); err != nil {
		return 0, err
	}
	if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
		return added, err
	}
	return added, nil
}
