package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/eshop"
)

// Edit, on the page of a game, finds its cover in the eShop of Europe by its name, for the games
// titledb has none of: most of the Switch 2 ones. The search is asked by the server, as the
// pages only call the app; the image chosen is then saved as the address of a cover is.

// eshopClient asks the search of the eShop; the images come through db.DownloadImage, as every
// cover given by its address.
var eshopClient = &http.Client{Timeout: 30 * time.Second}

const (
	ESHOP_NO_NAME     = "Write the name of the game."
	ESHOP_LONG_NAME   = "The name is too long."
	ESHOP_UNREACHABLE = "The eShop could not be reached. Try again later, or search on the web."
)

// EshopSearchAnswer is what a search of the eShop gives the page: the games found, and the
// address of a search of images on the web for the same name.
type EshopSearchAnswer struct {
	Games []eshop.Game `json:"games"`
	Web   string       `json:"web"`
}

// handleEshopSearch searches the eShop for the name sent, for administrators (it is a POST):
// switch2=true keeps the games of the Switch 2 only.
func (web *Web) handleEshopSearch(w http.ResponseWriter, r *http.Request) {
	lang := web.requestLanguage(r)
	name := strings.TrimSpace(r.FormValue("q"))
	switch query := eshop.Query(name); {
	case query == "":
		writeGlobalError(w, http.StatusBadRequest, lang, ESHOP_NO_NAME)
		return
	case len(query) > 200:
		writeGlobalError(w, http.StatusBadRequest, lang, ESHOP_LONG_NAME)
		return
	}
	switch2 := r.FormValue("switch2") == "true"
	games, err := eshop.Search(r.Context(), eshopClient, name, switch2)
	if err != nil {
		if !errors.Is(err, r.Context().Err()) {
			web.sugarLogger.Infof("The search of the eShop for %q failed: %v", name, err)
		}
		writeGlobalError(w, http.StatusBadGateway, lang, ESHOP_UNREACHABLE)
		return
	}
	writeJSON(w, http.StatusOK, EshopSearchAnswer{Games: games, Web: eshop.WebSearch(name, switch2)})
}
