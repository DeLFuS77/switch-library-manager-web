package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The queries Sphaira sends, written as it writes them (sphaira/source/utils/ownfoil_api.cpp).
const (
	sphairaBaseFields   = "appId displayVersion latestOwnedVersion{displayVersion} titledb{name publisher icon(size:THUMB){url local} banner(size:THUMB){url local}}"
	sphairaUpdateFields = "appId titleId displayVersion title{name publisher icon(size:THUMB){url local} banner(size:THUMB){url local}}"
	sphairaDlcFields    = "appId titleId displayVersion downloadUrl downloadExtension titledb{name publisher banner(size:THUMB){url local}} title{name icon(size:THUMB){url local}}"
	sphairaTitleQuery   = "query($id:ID!){title(titleId:$id){name publisher intro description releaseDate category numberOfPlayers size rating region banner(size:CLIENT){url local} fullBanner:banner(size:SCREEN){url local} availableVersions{version releaseDate} screenshots(size:CLIENT){url local} full:screenshots(size:SCREEN){url local} base:apps(owned:true,appType:[BASE]){displayVersion downloadSize downloadUrl downloadExtension latestOwnedVersion{version displayVersion}} updates:apps(owned:true,appType:[UPDATE]){appVersion displayVersion downloadUrl downloadExtension} dlc:apps(owned:true,appType:[DLC]){appId appVersion downloadUrl downloadExtension titledb{name intro banner(size:THUMB){url local}}}}}"
	sphairaUpdatesQuery = "query($page:Int!,$pageSize:Int!,$titleIds:[String!]){apps(owned:true,appType:[UPDATE],groupByAppId:true,filter:{titleId:{in:$titleIds}},page:$page,pageSize:$pageSize){total items{appId titleId appVersion}}}"
)

// sphairaPage is the query of a page of the catalog, as BuildPageQuery makes it.
func sphairaPage(category string, order string, descending bool) string {
	appType, fields, params, args := "BASE", sphairaBaseFields, "$page:Int!,$pageSize:Int!", ""
	switch category {
	case "new":
		params += ",$appIds:[String!]"
		args = ",filter:{appId:{notIn:$appIds}}"
	case "updates":
		appType, fields = "UPDATE", sphairaUpdateFields
		params += ",$appIds:[String!]"
		args = ",filter:{appId:{in:$appIds}}"
	case "dlc":
		appType, fields = "DLC", sphairaDlcFields
		params += ",$titleIds:[String!],$appIds:[String!]"
		args = ",filter:{titleId:{in:$titleIds},appId:{notIn:$appIds}}"
	case "search":
		params += ",$search:String"
		args = ",search:$search"
	}
	direction := "ASC"
	if descending {
		direction = "DESC"
	}
	return "query(" + params + "){apps(owned:true,appType:[" + appType + "],groupByAppId:true" + args +
		",orderBy:{field:" + order + ",direction:" + direction + "},page:$page,pageSize:$pageSize){total items{" + fields + "}}}"
}

// ownfoilWeb is the library of netWeb with what the titles database knows of its game.
func ownfoilWeb(t *testing.T) (*Web, *client, string) {
	t.Helper()
	web, admin, key := netWeb(t)
	switchDB, localDB := web.state.get()
	switchDB.TitlesMap["0100000000010"] = &db.SwitchTitle{
		Attributes: db.TitleAttributes{Id: "0100000000010000", Name: "Game", Publisher: "Lumen Works", ReleaseDate: 20230317, Intro: "Sail between the stars.",
			BannerUrl: "https://cdn.example/banner.jpg", Screenshots: []string{"https://cdn.example/1.jpg"}, Genres: []string{"Adventure"}, Players: 2},
		Updates: map[int]string{65536: "2023-05-01", 131072: "2023-08-01", 196608: "2024-01-09"},
		Dlc:     map[string]db.TitleAttributes{"0100000000011001": {Name: "Game Extra", Intro: "More stars."}},
	}
	localDB.TitlesMap["0100000000010"].Icon = "0100000000010000.jpg"
	web.netFilesForget()
	return web, &client{t: t, handler: admin.handler}, basic("sphaira", key)
}

// ownfoilQuery posts a query as Sphaira does and gives the data of the answer.
func ownfoilQuery(t *testing.T, console *client, auth string, path string, query string, variables string) map[string]any {
	t.Helper()
	body := `{"query":` + strconv.Quote(query) + `,"variables":{` + variables + `}}`
	got := sendBody(console, "POST", path, []byte(body), "Authorization", auth, "Content-Type", "application/json")
	var answer struct {
		Data   map[string]any `json:"data"`
		Errors []struct{ Message string }
	}
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &answer) != nil || len(answer.Errors) != 0 {
		t.Fatalf("%s: %d %s", query, got.Code, got.Body.String())
	}
	return answer.Data
}

// at reads a value of a JSON answer by its path: keys of objects and indexes of lists.
func at(t *testing.T, value any, path ...string) any {
	t.Helper()
	for _, step := range path {
		switch v := value.(type) {
		case map[string]any:
			value = v[step]
		case []any:
			i, err := strconv.Atoi(step)
			if err != nil || i >= len(v) {
				t.Fatalf("no %s in %v", step, v)
			}
			value = v[i]
		default:
			t.Fatalf("no %s in %v", step, value)
		}
	}
	return value
}

// Sphaira says hello to the server at its address, with a console key, and is told it is a shop;
// at the address of the index of Tinfoil too.
func TestOwnfoilHello(t *testing.T) {
	_, console, auth := ownfoilWeb(t)
	for _, path := range []string{"/", "/install/tinfoil/", "/install/ownfoil"} {
		got := console.do("OPTIONS", path, nil, "Authorization", auth)
		var hello netOwnfoilHello
		if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &hello) != nil || hello.Name == "" || !hello.Features.Shop || hello.Motd != "Hello" || len(hello.Uid) != 36 {
			t.Fatalf("%s: %d %s", path, got.Code, got.Body.String())
		}
	}
	if got := console.do("OPTIONS", "/", nil, "Authorization", basic("sphaira", "slm_wrong")); got.Code == http.StatusOK {
		t.Fatalf("a wrong key: %d", got.Code)
	}
}

// The pages of the catalog Sphaira asks, each as it asks it: all the games, the new ones, the
// updates, the DLC and a search, and the updates of the games the console has.
func TestOwnfoilCatalog(t *testing.T) {
	_, console, auth := ownfoilWeb(t)
	all := ownfoilQuery(t, console, auth, "/api/graphql", sphairaPage("all", "NAME", false), `"page":1,"pageSize":24`)
	if total := at(t, all, "apps", "total"); total != float64(1) {
		t.Fatalf("the games: %v", all)
	}
	game := at(t, all, "apps", "items", "0")
	if at(t, game, "appId") != "0100000000010000" || at(t, game, "titledb", "name") != "Game" || at(t, game, "titledb", "publisher") != "Lumen Works" ||
		at(t, game, "titledb", "icon", "url") != "http://example.com/install/cover/0100000000010000.jpg" || at(t, game, "titledb", "icon", "local") != true ||
		at(t, game, "titledb", "banner", "url") != "https://cdn.example/banner.jpg" || at(t, game, "titledb", "banner", "local") != false {
		t.Fatalf("the game: %v", game)
	}
	for _, order := range []string{"ADDED_AT", "RELEASE_DATE"} {
		if page := ownfoilQuery(t, console, auth, "/api/graphql", sphairaPage("all", order, true), `"page":1,"pageSize":12`); at(t, page, "apps", "total") != float64(1) {
			t.Fatalf("ordered by %s: %v", order, page)
		}
	}
	if none := ownfoilQuery(t, console, auth, "/api/graphql", sphairaPage("new", "NAME", false), `"page":1,"pageSize":24,"appIds":["0100000000010000"]`); at(t, none, "apps", "total") != float64(0) {
		t.Fatalf("a game the console has is not new: %v", none)
	}
	updates := ownfoilQuery(t, console, auth, "/api/graphql", sphairaPage("updates", "NAME", false), `"page":1,"pageSize":24,"appIds":["0100000000010800"]`)
	if update := at(t, updates, "apps", "items", "0"); at(t, update, "appId") != "0100000000010800" || at(t, update, "titleId") != "0100000000010000" || at(t, update, "title", "name") != "Game" {
		t.Fatalf("the updates: %v", updates)
	}
	dlc := ownfoilQuery(t, console, auth, "/api/graphql", sphairaPage("dlc", "NAME", false), `"page":1,"pageSize":24,"titleIds":["0100000000010000"],"appIds":[]`)
	extra := at(t, dlc, "apps", "items", "0")
	if at(t, extra, "appId") != "0100000000011001" || at(t, extra, "titledb", "name") != "Game Extra" || at(t, extra, "downloadExtension") != "nsp" ||
		!strings.HasPrefix(at(t, extra, "downloadUrl").(string), "http://example.com/install/f/") {
		t.Fatalf("the DLC: %v", dlc)
	}
	for search, want := range map[string]float64{"gam": 1, "0100000000010000": 1, "nothing": 0} {
		found := ownfoilQuery(t, console, auth, "/api/graphql", sphairaPage("search", "NAME", false), `"page":1,"pageSize":24,"search":`+strconv.Quote(search))
		if at(t, found, "apps", "total") != want {
			t.Fatalf("search %q: %v", search, found)
		}
	}
	held := ownfoilQuery(t, console, auth, "/install/tinfoil/api/graphql", sphairaUpdatesQuery, `"page":1,"pageSize":1000,"titleIds":["0100000000010000"]`)
	if row := at(t, held, "apps", "items", "0"); at(t, row, "appId") != "0100000000010800" || at(t, row, "appVersion") != float64(131072) {
		t.Fatalf("the newest update held: %v", held)
	}
	// a page past the end is empty, with the total
	if past := ownfoilQuery(t, console, auth, "/api/graphql", sphairaPage("all", "NAME", false), `"page":5,"pageSize":24`); len(at(t, past, "apps", "items").([]any)) != 0 || at(t, past, "apps", "total") != float64(1) {
		t.Fatalf("a page past the end: %v", past)
	}
}

// The page of a game: what the titles database knows, the versions published, and the game, its
// update and its DLC to install, which download with the key, in pieces too.
func TestOwnfoilTitle(t *testing.T) {
	_, console, auth := ownfoilWeb(t)
	data := ownfoilQuery(t, console, auth, "/api/graphql", sphairaTitleQuery, `"id":"0100000000010000"`)
	title := at(t, data, "title")
	if at(t, title, "name") != "Game" || at(t, title, "releaseDate") != "2023-03-17" || at(t, title, "numberOfPlayers") != "2" ||
		at(t, title, "category", "0") != "Adventure" || at(t, title, "fullBanner", "url") != "https://cdn.example/banner.jpg" ||
		at(t, title, "full", "0", "url") != "https://cdn.example/1.jpg" || at(t, title, "availableVersions", "2", "version") != float64(196608) ||
		at(t, title, "availableVersions", "2", "releaseDate") != "2024-01-09" {
		t.Fatalf("the title: %v", title)
	}
	base := at(t, title, "base", "0")
	if at(t, base, "downloadExtension") != "nsp" || at(t, base, "latestOwnedVersion", "version") != float64(131072) {
		t.Fatalf("the game to install: %v", base)
	}
	if update := at(t, title, "updates", "0"); at(t, update, "appVersion") != float64(131072) || at(t, update, "downloadExtension") != "nsz" {
		t.Fatalf("the update: %v", title)
	}
	if extra := at(t, title, "dlc", "0"); at(t, extra, "appId") != "0100000000011001" || at(t, extra, "titledb", "intro") != "More stars." {
		t.Fatalf("the DLC: %v", title)
	}
	address := at(t, base, "downloadUrl").(string)
	path := strings.TrimPrefix(address, "http://example.com")
	whole := console.do("GET", path, nil, "Authorization", auth)
	piece := console.do("GET", path, nil, "Authorization", auth, "Range", "bytes=5-6")
	if whole.Code != http.StatusOK || whole.Body.String() != "data of Game [0100000000010000][v0].nsp" || piece.Code != http.StatusPartialContent || piece.Body.String() != "of" {
		t.Fatalf("the download: %d %q, %d %q", whole.Code, whole.Body.String(), piece.Code, piece.Body.String())
	}
	// the page of a DLC asks for its game
	if dlcPage := ownfoilQuery(t, console, auth, "/api/graphql", sphairaTitleQuery, `"id":"0100000000011001"`); at(t, dlcPage, "title", "name") != "Game" {
		t.Fatalf("the page of a DLC: %v", dlcPage)
	}
	if unknown := ownfoilQuery(t, console, auth, "/api/graphql", sphairaTitleQuery, `"id":"0100000000099000"`); at(t, unknown, "title") != nil {
		t.Fatalf("a game the library has not: %v", unknown)
	}
}

// What is not a query of the shop is told in errors, and a shop that is off says so in the words
// Sphaira shows.
func TestOwnfoilRefusals(t *testing.T) {
	web, console, auth := ownfoilWeb(t)
	for _, query := range []string{"mutation{deleteEverything}", "{apps{...on App{appId}}}", "{users{name}}", "{apps{total"} {
		body := `{"query":` + strconv.Quote(query) + `}`
		got := sendBody(console, "POST", "/api/graphql", []byte(body), "Authorization", auth, "Content-Type", "application/json")
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"errors"`) {
			t.Errorf("%s: %d %s", query, got.Code, got.Body.String())
		}
	}
	if got := sendBody(console, "POST", "/api/graphql", []byte("not json"), "Authorization", auth); got.Code != http.StatusBadRequest {
		t.Errorf("not JSON: %d", got.Code)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	got := console.do("OPTIONS", "/", nil, "Authorization", auth)
	var refusal struct{ Error string }
	if got.Code != http.StatusNotFound || json.Unmarshal(got.Body.Bytes(), &refusal) != nil || !strings.Contains(refusal.Error, "off") {
		t.Fatalf("off: %d %s", got.Code, got.Body.String())
	}
}

// The reader of queries: aliases, arguments of every kind with the variables put in, comments and
// strings with escapes.
func TestParseGraphql(t *testing.T) {
	fields, err := parseGraphql(`# a comment
query Page($ids: [String!] = ["x"], $n: Int!) {
  first: apps(ids: $ids, n: $n, on: true, off: null, kind: BASE, text: "a \"b\" é", deep: {a: [1, 2.5]}) { total items { appId } }
  title(titleId: "X") { name }
}`, map[string]any{"ids": []any{"A", "B"}, "n": json.Number("3")})
	if err != nil || len(fields) != 2 {
		t.Fatalf("%v %v", fields, err)
	}
	apps := fields[0]
	if apps.key() != "first" || apps.name != "apps" || gqlInt(apps.args["n"], 0) != 3 || apps.args["on"] != true || apps.args["off"] != nil ||
		apps.args["kind"] != gqlEnum("BASE") || apps.args["text"] != `a "b" é` || len(apps.sel) != 2 || apps.sel[1].sel[0].name != "appId" {
		t.Fatalf("the field: %+v", apps)
	}
	if ids, _ := gqlStrings(apps.args["ids"]); len(ids) != 2 || ids[1] != "B" {
		t.Fatalf("the variable: %v", apps.args["ids"])
	}
	if deep := apps.args["deep"].(map[string]any)["a"].([]any); deep[0] != int64(1) || deep[1] != 2.5 {
		t.Fatalf("an object: %v", deep)
	}
	deep := strings.Repeat("{a", 40) + strings.Repeat("}", 40)
	for _, bad := range []string{"", "mutation{a}", "{a{...f}}", "{a @skip(if: true)}", "{a(b: \"open)}", deep, "{a}{b}", "{}"} {
		if _, err := parseGraphql(bad, nil); err == nil {
			t.Errorf("%q was read", bad)
		}
	}
}
