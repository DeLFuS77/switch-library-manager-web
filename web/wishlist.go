package web

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/userdata"
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

// ApiWishItem is a game of the wishlist, as /api/wishlist lists it.
type ApiWishItem struct {
	Id      string    `json:"id"`
	Name    string    `json:"name"`
	Region  string    `json:"region,omitempty"`
	AddedAt time.Time `json:"addedAt"`
	// as the store gives it: a day (2026-03-14), or only the month or the year when that is all
	// it knows; empty when it does not know
	ReleaseDate string `json:"releaseDate,omitempty"`
	Released    bool   `json:"released"`
	IconUrl     string `json:"iconUrl,omitempty"`
}

// releaseDateText writes a release date of the titles database (20260314, 202603 or 2026) as
// 2026-03-14, 2026-03 or 2026.
func releaseDateText(value int) string {
	switch text := strconv.Itoa(value); {
	case value <= 0:
		return ""
	case len(text) == 8:
		return text[:4] + "-" + text[4:6] + "-" + text[6:]
	case len(text) == 6:
		return text[:4] + "-" + text[4:]
	default:
		return text
	}
}

// apiWishlist lists the wishlist, the first wished first. A game the titles database does not
// know (yet) keeps its ID, without a name.
func (web *Web) apiWishlist(now time.Time) []ApiWishItem {
	switchDB, localDB := web.state.get()
	today := dateNumber(now)
	items := []ApiWishItem{}
	for _, wish := range web.wishes().Wishes() {
		item := ApiWishItem{Id: wish.Id, AddedAt: wish.Added.UTC()}
		if switchDB != nil {
			if prefix, err := db.TitleIDPrefix(wish.Id); err == nil {
				if title := switchDB.TitlesMap[prefix]; title != nil && strings.EqualFold(title.Attributes.Id, wish.Id) {
					attributes := title.Attributes
					item.Name, item.Region = attributes.Name, attributes.Region
					item.ReleaseDate = releaseDateText(attributes.ReleaseDate)
					// a date of the month or the year alone is out from its first day
					if release, err := intToTime(attributes.ReleaseDate); err == nil && !release.IsZero() {
						item.Released = dateNumber(release) <= today
					}
					item.IconUrl = coverUrl(switchDB, localDB, attributes)
				}
			}
		}
		items = append(items, item)
	}
	return items
}

func (web *Web) HandleWishlist() {
	// the wishlist as JSON, for other tools: read only, with the sign-in and API keys of /api/titles
	web.router.HandleFunc("/api/wishlist", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, web.apiWishlist(time.Now()))
	}).Methods("GET")

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
