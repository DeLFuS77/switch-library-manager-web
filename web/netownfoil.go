package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The shop of the kind of Ownfoil, which Sphaira reads (#180): Sphaira reads no index of Tinfoil,
// its menu of servers speaks the API of Ownfoil. Given the address of the server and the key of a
// console as the password, it says hello with OPTIONS at that address and asks its catalog with
// POST .../api/graphql: the games, updates and DLC of the library, a page at a time, the details
// of a game, and where to download each file, which are the files of /install/f/ with their
// ranges. The same list of files as the index of Tinfoil and the catalog of NXDock, behind the
// same gate and keys.
//
// Only what Sphaira asks is answered: the query apps (owned, by type, grouped by app ID, filtered
// by app ID or title ID, searched, ordered by name, date added or release date, a page at a time)
// and the query title (a game, its versions, its files and its DLC).

const (
	netInstallOwnfoil      = NET_INSTALL_PREFIX + "ownfoil"
	netInstallOwnfoilQuery = netInstallOwnfoil + "/api/graphql"
	// the version of the protocol said in the hello
	netOwnfoilProtocol = 1
	// the largest page and the largest request
	netOwnfoilMaxPage = 1000
	netOwnfoilMaxBody = 1 << 20
)

// netOwnfoilBases are the addresses a shop may be given besides /install/ownfoil: the server
// alone, and the addresses of the index of Tinfoil, as Sphaira keeps the path it is given.
var netOwnfoilBases = map[string]bool{"": true, NET_INSTALL_PREFIX[:len(NET_INSTALL_PREFIX)-1]: true, netInstallTinfoil: true}

// ownfoilShopAddress takes the requests of a shop of the kind of Ownfoil, sent with the key of a
// console, at the address of the server or of the index of Tinfoil, to /install/ownfoil, where
// they are answered. Without a key (a browser) nothing changes.
func ownfoilShopAddress(r *http.Request) {
	if r.Method != http.MethodOptions && r.Method != http.MethodPost {
		return
	}
	if _, ok := basicKey(r); !ok {
		return
	}
	p := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodOptions && netOwnfoilBases[p]:
		r.URL.Path, r.URL.RawPath = netInstallOwnfoil, ""
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/api/graphql") && netOwnfoilBases[strings.TrimSuffix(p, "/api/graphql")]:
		r.URL.Path, r.URL.RawPath = netInstallOwnfoilQuery, ""
	}
}

// netOwnfoilPath tells the paths of the shop a console key may ask, with the method each takes.
func netOwnfoilPath(r *http.Request) bool {
	p := strings.TrimSuffix(r.URL.Path, "/")
	return (r.Method == http.MethodOptions && p == netInstallOwnfoil) || (r.Method == http.MethodPost && p == netInstallOwnfoilQuery)
}

func (web *Web) handleNetOwnfoilPaths() {
	for _, p := range []string{netInstallOwnfoil, netInstallOwnfoil + "/"} {
		web.router.HandleFunc(p, web.handleNetOwnfoilHello).Methods(http.MethodOptions)
	}
	for _, p := range []string{netInstallOwnfoilQuery, netInstallOwnfoilQuery + "/"} {
		web.router.HandleFunc(p, web.handleNetOwnfoilQuery).Methods(http.MethodPost)
	}
}

// ownfoilError answers a refusal the way the shop does, which Sphaira shows as it is.
func ownfoilError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": message})
}

// netOwnfoilGate is netInstallGate, telling why in the words Sphaira shows.
func (web *Web) netOwnfoilGate(w http.ResponseWriter, r *http.Request) (settings.NetInstallOptions, bool) {
	appSettings := settings.ReadSettings(web.dataFolder)
	switch {
	case !netInstallEnabled(appSettings):
		ownfoilError(w, http.StatusNotFound, "Install over the network is off in Switch Library Manager (Settings › Install over the network)")
	case !web.auth.Enabled():
		ownfoilError(w, http.StatusForbidden, "Install over the network needs a user with a password in Switch Library Manager")
	case !appSettings.NetInstall.Remote && !isLocalRequest(r):
		ownfoilError(w, http.StatusForbidden, "Switch Library Manager answers only on the local network")
	default:
		return appSettings.NetInstall, true
	}
	return appSettings.NetInstall, false
}

type netOwnfoilFeatures struct {
	Shop              bool `json:"shop"`
	DumpsUpload       bool `json:"dumps_upload"`
	SaveBackup        bool `json:"save_backup"`
	ResumableDownload bool `json:"resumable_download"`
	ResumableUpload   bool `json:"resumable_upload"`
}

type netOwnfoilHello struct {
	Uid             string             `json:"uid"`
	Name            string             `json:"name"`
	Version         string             `json:"version"`
	ProtocolVersion int                `json:"protocol_version"`
	Motd            string             `json:"motd,omitempty"`
	Public          bool               `json:"public"`
	Features        netOwnfoilFeatures `json:"features"`
}

// handleNetOwnfoilHello says what the shop is: its name, version and message, and that it is a
// shop whose downloads go on where they stopped.
func (web *Web) handleNetOwnfoilHello(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netOwnfoilGate(w, r)
	if !ok {
		return
	}
	hello := web.netHello()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, netOwnfoilHello{Uid: hello.Id, Name: hello.Name, Version: hello.Version, ProtocolVersion: netOwnfoilProtocol,
		Motd: netInstallMessage(options.Message), Features: netOwnfoilFeatures{Shop: true, ResumableDownload: true}})
}

// netOwnfoilApp is a file of the library as the shop lists it: an app (a game, an update or a
// DLC) at a version.
type netOwnfoilApp struct {
	file    netFile
	appType string // BASE, UPDATE or DLC
	appId   string
	titleId string // of its game
	game    *netOwnfoilGame
}

// netOwnfoilGame is a game of the library: its title ID, its name, what the titles database
// knows of it, and its apps.
type netOwnfoilGame struct {
	titleId string
	name    string
	known   *db.SwitchTitle
	cover   string
	apps    []*netOwnfoilApp
}

// netOwnfoilShop is the catalog a query is answered from, with the address its links begin with.
type netOwnfoilShop struct {
	origin string
	games  map[string]*netOwnfoilGame
	apps   []*netOwnfoilApp
}

var netOwnfoilTypes = map[string]string{netFolderGames: "BASE", netFolderUpdates: "UPDATE", netFolderDlc: "DLC"}

// netGameTitleId is the title ID of the game of a title ID: an update's has 0x800 more, a DLC's
// 0x1000 more and its number in the last three digits.
func netGameTitleId(titleId string, appType string) string {
	id, err := strconv.ParseUint(titleId, 16, 64)
	if err != nil {
		return strings.ToUpper(titleId)
	}
	switch appType {
	case "UPDATE":
		id &^= 0x800
	case "DLC":
		id = (id - 0x1000) &^ 0xFFF
	}
	return fmt.Sprintf("%016X", id)
}

func (web *Web) netOwnfoilShop(r *http.Request, files []netFile) *netOwnfoilShop {
	switchDB, _ := web.state.get()
	scheme := "http"
	if isHttps(r) {
		scheme = "https"
	}
	shop := &netOwnfoilShop{origin: scheme + "://" + r.Host, games: map[string]*netOwnfoilGame{}}
	for _, file := range files {
		appType := netOwnfoilTypes[file.kind]
		if appType == "" || file.titleId == "" {
			continue
		}
		titleId := netGameTitleId(file.titleId, appType)
		game, ok := shop.games[titleId]
		if !ok {
			game = &netOwnfoilGame{titleId: titleId, name: file.game, cover: file.cover}
			if switchDB != nil {
				if prefix, err := db.TitleIDPrefix(titleId); err == nil {
					game.known = switchDB.TitlesMap[prefix]
				}
			}
			if game.name == "" {
				game.name = titleId
			}
			shop.games[titleId] = game
		}
		app := &netOwnfoilApp{file: file, appType: appType, appId: strings.ToUpper(file.titleId), titleId: titleId, game: game}
		game.apps = append(game.apps, app)
		shop.apps = append(shop.apps, app)
	}
	return shop
}

type netOwnfoilRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// handleNetOwnfoilQuery answers a query of the catalog. As GraphQL does, a query it cannot answer
// is answered with a 200 that tells why in errors.
func (web *Web) handleNetOwnfoilQuery(w http.ResponseWriter, r *http.Request) {
	options, ok := web.netOwnfoilGate(w, r)
	if !ok {
		return
	}
	var request netOwnfoilRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, netOwnfoilMaxBody))
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil || request.Query == "" {
		ownfoilError(w, http.StatusBadRequest, "a query in JSON was expected")
		return
	}
	fields, err := parseGraphql(request.Query, request.Variables)
	if err != nil {
		web.ownfoilErrors(w, err.Error())
		return
	}
	files, _, ready := web.netFilesReady(options, netCatalogWait)
	if !ready {
		done, total := web.netFilesMaking()
		w.Header().Set("Retry-After", "5")
		ownfoilError(w, http.StatusServiceUnavailable, fmt.Sprintf("Switch Library Manager is preparing the list of the library (%d of %d games). Try again in a moment", done, total))
		return
	}
	shop := web.netOwnfoilShop(r, files)
	data := map[string]any{}
	for _, field := range fields {
		switch field.name {
		case "apps":
			data[field.key()] = shop.appsConnection(field, shop.apps, true)
		case "title":
			id := strings.ToUpper(gqlString(field.args["titleId"]))
			game := shop.games[id]
			if game == nil {
				// the page of an update or a DLC asks for its game
				for _, app := range shop.apps {
					if app.appId == id {
						game = app.game
						break
					}
				}
			}
			if game == nil {
				data[field.key()] = nil
				continue
			}
			data[field.key()] = shop.gameFields(game, field.sel)
		case "__typename":
			data[field.key()] = "Query"
		default:
			web.ownfoilErrors(w, fmt.Sprintf("Cannot query field %q on type \"Query\"", field.name))
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (web *Web) ownfoilErrors(w http.ResponseWriter, message string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"data": nil, "errors": []map[string]string{{"message": message}}})
}

// ownfoilMatch tells whether a value passes a filter of the kind {in: [...], notIn: [...]}.
func ownfoilMatch(filter any, value string) bool {
	conditions, ok := filter.(map[string]any)
	if !ok {
		return true
	}
	if list, ok := gqlStrings(conditions["in"]); ok && !containsFold(list, value) {
		return false
	}
	if list, ok := gqlStrings(conditions["notIn"]); ok && containsFold(list, value) {
		return false
	}
	if equal, ok := conditions["eq"]; ok && !strings.EqualFold(gqlString(equal), value) {
		return false
	}
	return true
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

// name is the name an app is listed and ordered by: its game's, or a DLC's own.
func (app *netOwnfoilApp) name() string {
	if app.appType == "DLC" && app.file.dlcName != "" {
		return app.file.dlcName
	}
	return app.game.name
}

// appsConnection answers apps(...): the apps asked, of the given ones, a page of them, with how
// many there are. top tells the query of the top, which is paged; the apps of a title are not.
func (shop *netOwnfoilShop) appsConnection(field gqlField, apps []*netOwnfoilApp, top bool) any {
	types, typed := gqlStrings(field.args["appType"])
	filter, _ := field.args["filter"].(map[string]any)
	search := strings.ToLower(strings.TrimSpace(gqlString(field.args["search"])))
	chosen := []*netOwnfoilApp{}
	for _, app := range apps {
		if typed && !containsFold(types, app.appType) {
			continue
		}
		if !ownfoilMatch(filter["appId"], app.appId) || !ownfoilMatch(filter["titleId"], app.titleId) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(app.name()), search) && !strings.Contains(strings.ToLower(app.appId), search) &&
			!strings.Contains(strings.ToLower(app.titleId), search) {
			continue
		}
		chosen = append(chosen, app)
	}
	// one app per app ID: its newest version, the first file of it
	if grouped, _ := field.args["groupByAppId"].(bool); grouped {
		newest := map[string]*netOwnfoilApp{}
		order := []string{}
		for _, app := range chosen {
			kept, ok := newest[app.appId]
			if !ok {
				order = append(order, app.appId)
			}
			if !ok || app.file.version > kept.file.version {
				newest[app.appId] = app
			}
		}
		chosen = chosen[:0]
		for _, id := range order {
			chosen = append(chosen, newest[id])
		}
	}
	orderBy, _ := field.args["orderBy"].(map[string]any)
	sortField, descending := gqlString(orderBy["field"]), strings.EqualFold(gqlString(orderBy["direction"]), "DESC")
	sort.SliceStable(chosen, func(i, j int) bool {
		a, b := chosen[i], chosen[j]
		switch sortField {
		case "ADDED_AT":
			if !a.file.modTime.Equal(b.file.modTime) {
				return a.file.modTime.Before(b.file.modTime) != descending
			}
		case "RELEASE_DATE":
			if ra, rb := a.game.releaseDate(), b.game.releaseDate(); ra != rb {
				return (ra < rb) != descending
			}
		}
		if na, nb := strings.ToLower(a.name()), strings.ToLower(b.name()); na != nb {
			return (na < nb) != (descending && (sortField == "" || sortField == "NAME"))
		}
		if a.appId != b.appId {
			return a.appId < b.appId
		}
		return a.file.version < b.file.version
	})
	total := len(chosen)
	if top || field.args["page"] != nil {
		pageSize := gqlInt(field.args["pageSize"], 50)
		if pageSize < 1 {
			pageSize = 1
		}
		if pageSize > netOwnfoilMaxPage {
			pageSize = netOwnfoilMaxPage
		}
		page := gqlInt(field.args["page"], 1)
		if page < 1 {
			page = 1
		}
		start := (page - 1) * pageSize
		if start > int64(len(chosen)) {
			start = int64(len(chosen))
		}
		end := start + pageSize
		if end > int64(len(chosen)) {
			end = int64(len(chosen))
		}
		chosen = chosen[start:end]
	}
	items := func(sel []gqlField) []any {
		out := make([]any, 0, len(chosen))
		for _, app := range chosen {
			out = append(out, shop.appFields(app, sel))
		}
		return out
	}
	// a list of a title is the apps themselves; the query of the top gives total and items
	if !top {
		return items(field.sel)
	}
	answer := map[string]any{}
	for _, f := range field.sel {
		switch f.name {
		case "total":
			answer[f.key()] = total
		case "items":
			answer[f.key()] = items(f.sel)
		case "page":
			answer[f.key()] = gqlInt(field.args["page"], 1)
		case "pageSize":
			answer[f.key()] = gqlInt(field.args["pageSize"], 50)
		default:
			answer[f.key()] = nil
		}
	}
	return answer
}

// newestUpdate is the newest update of the game the library holds.
func (game *netOwnfoilGame) newestUpdate() *netOwnfoilApp {
	var newest *netOwnfoilApp
	for _, app := range game.apps {
		if app.appType == "UPDATE" && (newest == nil || app.file.version > newest.file.version) {
			newest = app
		}
	}
	return newest
}

func (game *netOwnfoilGame) releaseDate() int {
	if game.known == nil {
		return 0
	}
	return game.known.Attributes.ReleaseDate
}

// ownfoilDate writes a date of the titles database (20230317) as 2023-03-17.
func ownfoilDate(date int) string {
	if date <= 0 {
		return ""
	}
	text := strconv.Itoa(date)
	if len(text) != 8 {
		return text
	}
	return text[:4] + "-" + text[4:6] + "-" + text[6:]
}

// image answers an image: url and whether it is one of the shop, which Sphaira asks with the key.
func ownfoilImage(sel []gqlField, address string, local bool) any {
	if address == "" {
		return nil
	}
	answer := map[string]any{}
	for _, f := range sel {
		switch f.name {
		case "url":
			answer[f.key()] = address
		case "local":
			answer[f.key()] = local
		default:
			answer[f.key()] = nil
		}
	}
	return answer
}

func (shop *netOwnfoilShop) appFields(app *netOwnfoilApp, sel []gqlField) any {
	answer := map[string]any{}
	file := app.file
	for _, f := range sel {
		var value any
		switch f.name {
		case "appId":
			value = app.appId
		case "titleId":
			value = app.titleId
		case "appType":
			value = app.appType
		case "appVersion", "version":
			value = file.version
		case "displayVersion":
			if file.displayVersion != "" {
				value = file.displayVersion
			}
		case "downloadUrl":
			value = shop.origin + netInstallFiles + file.id + "/" + url.PathEscape(file.name)
		case "downloadExtension":
			value = strings.TrimPrefix(strings.ToLower(filepath.Ext(file.name)), ".")
		case "downloadSize", "size":
			value = file.size
		case "fileName", "filename":
			value = file.name
		case "addedAt":
			value = file.modTime.UTC().Format(time.RFC3339)
		case "latestOwnedVersion":
			if app.appType == "BASE" {
				if newest := app.game.newestUpdate(); newest != nil {
					value = shop.versionFields(newest, f.sel)
				}
			}
		case "titledb":
			if app.appType == "DLC" {
				value = shop.dlcFields(app, f.sel)
			} else {
				value = shop.gameFields(app.game, f.sel)
			}
		case "title":
			value = shop.gameFields(app.game, f.sel)
		case "__typename":
			value = "App"
		}
		answer[f.key()] = value
	}
	return answer
}

// versionFields answers a version held: its number and the version it shows.
func (shop *netOwnfoilShop) versionFields(app *netOwnfoilApp, sel []gqlField) any {
	answer := map[string]any{}
	for _, f := range sel {
		switch f.name {
		case "version", "appVersion":
			answer[f.key()] = app.file.version
		case "displayVersion":
			if app.file.displayVersion != "" {
				answer[f.key()] = app.file.displayVersion
			} else {
				answer[f.key()] = nil
			}
		default:
			answer[f.key()] = nil
		}
	}
	return answer
}

// dlcFields answers what the titles database knows of a DLC: its name, and its game's publisher
// and cover.
func (shop *netOwnfoilShop) dlcFields(app *netOwnfoilApp, sel []gqlField) any {
	var known db.TitleAttributes
	if app.game.known != nil {
		known = app.game.known.Dlc[app.appId]
	}
	answer := map[string]any{}
	for _, f := range sel {
		var value any
		switch f.name {
		case "name":
			value = app.name()
		case "publisher":
			if known.Publisher != "" {
				value = known.Publisher
			} else if app.game.known != nil {
				value = app.game.known.Attributes.Publisher
			}
		case "intro":
			if known.Intro != "" {
				value = known.Intro
			}
		case "description":
			if known.Description != "" {
				value = known.Description
			}
		case "icon":
			if known.IconUrl != "" {
				value = ownfoilImage(f.sel, known.IconUrl, false)
			} else {
				value = shop.icon(app.game, f.sel)
			}
		case "banner":
			if known.BannerUrl != "" {
				value = ownfoilImage(f.sel, known.BannerUrl, false)
			} else {
				value = shop.banner(app.game, f.sel)
			}
		}
		answer[f.key()] = value
	}
	return answer
}

// icon is the cover of the game the library shows, given by the shop; else the one of the titles
// database.
func (shop *netOwnfoilShop) icon(game *netOwnfoilGame, sel []gqlField) any {
	if game.cover != "" {
		return ownfoilImage(sel, shop.origin+netInstallCover+url.PathEscape(game.cover), true)
	}
	if game.known != nil && game.known.Attributes.IconUrl != "" {
		return ownfoilImage(sel, game.known.Attributes.IconUrl, false)
	}
	return nil
}

func (shop *netOwnfoilShop) banner(game *netOwnfoilGame, sel []gqlField) any {
	if game.known != nil && game.known.Attributes.BannerUrl != "" {
		return ownfoilImage(sel, game.known.Attributes.BannerUrl, false)
	}
	return nil
}

// gameFields answers a game: what the titles database knows of it, the versions published and
// the apps of it the library holds.
func (shop *netOwnfoilShop) gameFields(game *netOwnfoilGame, sel []gqlField) any {
	var known db.TitleAttributes
	if game.known != nil {
		known = game.known.Attributes
	}
	answer := map[string]any{}
	for _, f := range sel {
		var value any
		switch f.name {
		case "titleId", "id":
			value = game.titleId
		case "name":
			value = game.name
		case "publisher":
			if known.Publisher != "" {
				value = known.Publisher
			}
		case "intro":
			if known.Intro != "" {
				value = known.Intro
			}
		case "description":
			if known.Description != "" {
				value = known.Description
			}
		case "releaseDate":
			if date := ownfoilDate(known.ReleaseDate); date != "" {
				value = date
			}
		case "category":
			value = append([]string{}, known.Genres...)
		case "numberOfPlayers":
			if known.Players > 0 {
				value = strconv.Itoa(known.Players)
			}
		case "size":
			if known.Size > 0 {
				value = strconv.Itoa(known.Size)
			}
		case "rating":
			if known.AgeRating > 0 {
				value = strconv.Itoa(known.AgeRating)
			}
		case "region":
			if known.Region != "" {
				value = known.Region
			}
		case "icon":
			value = shop.icon(game, f.sel)
		case "banner":
			value = shop.banner(game, f.sel)
		case "screenshots":
			images := []any{}
			for _, address := range known.Screenshots {
				if image := ownfoilImage(f.sel, address, false); image != nil {
					images = append(images, image)
				}
			}
			value = images
		case "availableVersions":
			value = shop.versionsPublished(game, f.sel)
		case "apps":
			value = shop.appsConnection(f, game.apps, false)
		case "__typename":
			value = "Title"
		}
		answer[f.key()] = value
	}
	return answer
}

// versionsPublished are the versions of a game the titles database knows, the oldest first, with
// the date of each; with none known, the ones the library holds.
func (shop *netOwnfoilShop) versionsPublished(game *netOwnfoilGame, sel []gqlField) []any {
	dates := map[int]string{}
	if game.known != nil {
		for version, date := range game.known.Updates {
			dates[version] = date
		}
	}
	for _, app := range game.apps {
		if app.appType == "UPDATE" {
			if _, ok := dates[app.file.version]; !ok {
				dates[app.file.version] = ""
			}
		}
	}
	versions := make([]int, 0, len(dates))
	for version := range dates {
		versions = append(versions, version)
	}
	sort.Ints(versions)
	out := make([]any, 0, len(versions))
	for _, version := range versions {
		answer := map[string]any{}
		for _, f := range sel {
			switch f.name {
			case "version":
				answer[f.key()] = version
			case "releaseDate":
				if dates[version] != "" {
					answer[f.key()] = dates[version]
				} else {
					answer[f.key()] = nil
				}
			default:
				answer[f.key()] = nil
			}
		}
		out = append(out, answer)
	}
	return out
}
