package web

import (
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

// The wishlist keeps games that are not in the library yet. It is shown on the Missing Games
// page and the notifications report when a wished game is released or gets new DLC.

var titleIdPattern = regexp.MustCompile(`^[0-9A-F]{16}$`)

func (web *Web) wishes() *userdata.Wishlist {
	web.wishOnce.Do(func() {
		web.wish = userdata.LoadWishlist(filepath.Join(web.dataFolder, userdata.WISHLIST_FILENAME))
	})
	return web.wish
}

// forgetOwnedWishes removes the wished games that are in the library now.
func (web *Web) forgetOwnedWishes() {
	_, localDB := web.state.get()
	if localDB == nil {
		return
	}
	if web.wishes().RemoveOwned(func(id string) bool {
		local, ok := localDB.TitlesMap[strings.ToLower(id[:13])]
		return ok && local.BaseExist
	}) {
		// the lists and counts show the hearts
		web.invalidateDerived()
	}
}

func (web *Web) HandleWishlist() {
	web.router.HandleFunc("/wishlist", func(w http.ResponseWriter, r *http.Request) {
		id := strings.ToUpper(strings.TrimSpace(r.FormValue("id")))
		if !titleIdPattern.MatchString(id) {
			writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), "Invalid request")
			return
		}
		wanted := r.FormValue("wanted") == "true"
		if web.wishes().Set(id, wanted) {
			// the Missing Games lists show the hearts
			web.invalidateDerived()
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "wanted": wanted, "count": web.wishes().Count()})
	}).Methods("POST")
}
