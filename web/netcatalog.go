package web

import (
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
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
	Key     string           `json:"key"`
	Name    string           `json:"name"`
	TitleId string           `json:"titleId,omitempty"`
	Cover   string           `json:"cover,omitempty"`
	Files   []netCatalogFile `json:"files"`
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
	files, _ := web.netFilesKept(options)
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
		kind := netCatalogKinds[file.kind]
		if kind == "game" && game.TitleId == "" {
			game.TitleId = file.titleId
		}
		game.Files = append(game.Files, netCatalogFile{Id: file.id, Kind: kind, TitleId: file.titleId, Version: file.version,
			Size: file.size, Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(file.name)), "."), Name: file.name,
			DlcName: file.dlcName, RequiredSystem: file.requiredSystem, Url: netInstallFiles + file.id + "/" + url.PathEscape(file.name)})
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

// handleNetCover gives the cover of a game of the catalog, as a small JPEG the console decodes
// quickly. Only the covers the catalog names: no other image of the data folder.
func (web *Web) handleNetCover(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netInstallGate(w, r)
	if !ok {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, netInstallCover)
	files, _ := web.netFilesKept(options)
	known := false
	for _, file := range files {
		if file.cover == name {
			known = true
			break
		}
	}
	if !known || name == "" {
		http.NotFound(w, r)
		return
	}
	original, found := web.imagePath(name)
	if !found && remoteCoverName.MatchString(name) {
		fetched, err := web.fetchRemoteCover(name)
		if err == nil {
			original, found = fetched, true
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
