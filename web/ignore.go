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
	Id      string `json:"id"`
	Kind    string `json:"kind"`
	Ignored bool   `json:"ignored"`
}

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

// HandleIgnore adds or removes a title ID from the ignored DLC or ignored updates list.
// Missing DLC and updates are computed on every request, so no rescan is needed.
func (web *Web) HandleIgnore() {
	web.router.HandleFunc("/ignore", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.FormValue("id"))
		kind := r.FormValue("kind")
		ignored := r.FormValue("ignored") != "false"

		if !titleIdRegex.MatchString(id) {
			writeGlobalError(w, http.StatusBadRequest, "Invalid Title ID (16 hexadecimal characters)")
			return
		}
		if kind != IGNORE_KIND_DLC && kind != IGNORE_KIND_UPDATE {
			writeGlobalError(w, http.StatusBadRequest, "Unknown ignore list")
			return
		}

		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			if kind == IGNORE_KIND_DLC {
				s.IgnoreDLCTitleIds = setIgnored(s.IgnoreDLCTitleIds, id, ignored)
			} else {
				s.IgnoreUpdateTitleIds = setIgnored(s.IgnoreUpdateTitleIds, id, ignored)
			}
		})

		writeJSON(w, http.StatusOK, IgnoreResponse{Id: strings.ToUpper(id), Kind: kind, Ignored: ignored})
	}).Methods("POST")
}
