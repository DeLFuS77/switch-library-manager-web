package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// The catalog NXDock shows: the games of the library with their files (the game, its updates and
// its DLC), what each needs, and their covers. Behind the same gate and keys as the rest of
// /install/: a console key reads it.

const (
	netInstallCatalog = NET_INSTALL_PREFIX + "catalog"
	netInstallCover   = NET_INSTALL_PREFIX + "cover/"
)

type netCatalogFile struct {
	Id             string `json:"id"`
	Kind           string `json:"kind"` // game, update or dlc
	TitleId        string `json:"titleId"`
	Version        int    `json:"version"`
	Size           int64  `json:"size"`
	Format         string `json:"format"` // nsp, nsz, xci or xcz, as it is given
	Name           string `json:"name"`
	DlcName        string `json:"dlcName,omitempty"`
	RequiredSystem int    `json:"requiredSystem,omitempty"`
	Url            string `json:"url"`
}

type netCatalogGame struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	TitleId string `json:"titleId,omitempty"`
	Cover   string `json:"cover,omitempty"`
	// when its newest file came to the library (its date on the disk), seconds since 1970: the
	// console can sort by it
	Added int64            `json:"added,omitempty"`
	Files []netCatalogFile `json:"files"`
}

type netCatalog struct {
	App   netHelloAnswer   `json:"app"`
	Games []netCatalogGame `json:"games"`
}

var netCatalogKinds = map[string]string{netFolderGames: "game", netFolderUpdates: "update", netFolderDlc: "dlc"}

func (web *Web) handleNetCatalog(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netInstallGate(w, r)
	if !ok {
		return
	}
	files, _, ready := web.netFilesReady(options, netCatalogWait)
	if !ready {
		// the list is being made, which in a big library on a NAS takes minutes the first time:
		// the console is told so, and how far it is, and asks again
		done, total := web.netFilesMaking()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"preparing": true, "done": done, "total": total})
		return
	}
	// a console that installs the NCZ of NXPress's extra compression itself (NXDock with
	// ?ncz=native) gets them as they are: smaller, nothing to rebuild here
	native := r.URL.Query().Get("ncz") == "native"
	games := map[string]*netCatalogGame{}
	for _, file := range files {
		game, ok := games[file.gameKey]
		if !ok {
			game = &netCatalogGame{Key: file.gameKey, Name: file.game, Files: []netCatalogFile{}}
			if file.cover != "" {
				game.Cover = netInstallCover + url.PathEscape(file.cover)
			}
			games[file.gameKey] = game
		}
		if added := file.modTime.Unix(); !file.modTime.IsZero() && added > game.Added {
			game.Added = added
		}
		kind := netCatalogKinds[file.kind]
		if kind == "game" && game.TitleId == "" {
			game.TitleId = file.titleId
		}
		entry := netCatalogFile{Id: file.id, Kind: kind, TitleId: file.titleId, Version: file.version,
			Size: file.size, Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(file.name)), "."), Name: file.name,
			DlcName: file.dlcName, RequiredSystem: file.requiredSystem, Url: netInstallFiles + file.id + "/" + url.PathEscape(file.name)}
		if native && file.stream != nil {
			if stat, err := os.Stat(file.path); err == nil {
				entry.Size = stat.Size()
				entry.Format = strings.TrimPrefix(strings.ToLower(filepath.Ext(file.path)), ".")
				entry.Url += "?raw=1"
			}
		}
		game.Files = append(game.Files, entry)
	}
	catalog := netCatalog{App: web.netHello(), Games: make([]netCatalogGame, 0, len(games))}
	order := map[string]int{"game": 0, "update": 1, "dlc": 2}
	for _, game := range games {
		sort.SliceStable(game.Files, func(i, j int) bool {
			if order[game.Files[i].Kind] != order[game.Files[j].Kind] {
				return order[game.Files[i].Kind] < order[game.Files[j].Kind]
			}
			if game.Files[i].Version != game.Files[j].Version {
				return game.Files[i].Version > game.Files[j].Version
			}
			return game.Files[i].Name < game.Files[j].Name
		})
		catalog.Games = append(catalog.Games, *game)
	}
	sort.Slice(catalog.Games, func(i, j int) bool {
		return strings.ToLower(catalog.Games[i].Name) < strings.ToLower(catalog.Games[j].Name)
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, catalog)
}

// netCoverRetryAfter is the seconds a console waits before asking again for a cover the cover
// server did not give.
const netCoverRetryAfter = "30"

// handleNetCover gives the cover of a game of the catalog, as a small JPEG the console decodes
// quickly. Only the covers the catalog names: no other image of the data folder.
func (web *Web) handleNetCover(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netInstallGate(w, r)
	if !ok {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, netInstallCover)
	if name == "" || !web.netCoverKnown(options, name) {
		http.NotFound(w, r)
		return
	}
	original, found := web.imagePath(name)
	if !found && remoteCoverName.MatchString(name) {
		fetched, err := web.fetchRemoteCover(name)
		if err == nil {
			original, found = fetched, true
		} else if !db.IsNotFoundAnswer(err) {
			// the cover server did not answer now (no connection, too slow): the cover is there,
			// the console asks again later instead of taking the game as one without a cover
			w.Header().Set("Retry-After", netCoverRetryAfter)
			http.Error(w, "the cover could not be fetched now", http.StatusServiceUnavailable)
			return
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	thumbnail := filepath.Join(web.dataFolder, "img", "thumbs", name)
	if err := web.thumbnails().make(original, thumbnail); err != nil {
		serveImage(w, r, original)
		return
	}
	serveImage(w, r, thumbnail)
}
