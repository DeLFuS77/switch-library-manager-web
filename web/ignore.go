package web

import (
	"net/http"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const (
	IGNORE_KIND_DLC    = "dlc"
	IGNORE_KIND_UPDATE = "update"
)

type IgnoreResponse struct {
	Ids     []string `json:"ids"`
	Kind    string   `json:"kind"`
	Ignored bool     `json:"ignored"`
}

// maximum number of title IDs changed by one request
const maxIgnoreIds = 1000

// setIgnored adds a title ID to (or removes it from) an ignore list, without duplicates.
func setIgnored(list []string, id string, ignored bool) []string {
	result := []string{}
	for _, existing := range list {
		if !strings.EqualFold(strings.TrimSpace(existing), id) {
			result = append(result, existing)
		}
	}
	if ignored {
		result = append(result, strings.ToUpper(id))
	}
	return result
}

// HandleIgnore adds or removes title IDs (one or several "id" values) from the ignored DLC
// or ignored updates list. Either all IDs are valid and applied, or none.
// Missing DLC and updates are computed on every request, so no rescan is needed.
func (web *Web) HandleIgnore() {
	web.router.HandleFunc("/ignore", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		kind := r.FormValue("kind")
		ignored := r.FormValue("ignored") != "false"

		ids := []string{}
		for _, id := range r.Form["id"] {
			id = strings.TrimSpace(id)
			if !titleIdRegex.MatchString(id) {
				writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), "Invalid Title ID (16 hexadecimal characters)")
				return
			}
			ids = append(ids, strings.ToUpper(id))
		}
		if len(ids) == 0 || len(ids) > maxIgnoreIds {
			writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), "Invalid Title ID (16 hexadecimal characters)")
			return
		}
		if kind != IGNORE_KIND_DLC && kind != IGNORE_KIND_UPDATE {
			writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), "Unknown ignore list")
			return
		}

		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			for _, id := range ids {
				if kind == IGNORE_KIND_DLC {
					s.IgnoreDLCTitleIds = setIgnored(s.IgnoreDLCTitleIds, id, ignored)
				} else {
					s.IgnoreUpdateTitleIds = setIgnored(s.IgnoreUpdateTitleIds, id, ignored)
				}
			}
		})

		writeJSON(w, http.StatusOK, IgnoreResponse{Ids: ids, Kind: kind, Ignored: ignored})
	}).Methods("POST")
}
