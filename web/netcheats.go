package web

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

// The cheats of a game for the console: GET /install/cheats/{titleId}?version=N, or ?build= with
// the build ID the console reads from the game running. It answers the build ID, the names of the
// cheats and their text file, as Atmosphère reads it (contents/<title ID>/cheats/<build ID>.txt),
// or 404 {"error": "no cheats"}. Without a version or a build, the version the library has.

const netInstallCheats = NET_INSTALL_PREFIX + "cheats/"

var netBuildIdPattern = regexp.MustCompile(`^[0-9A-F]{16}$`)

// netCheats is the answer of the cheats of a game.
type netCheats struct {
	TitleId string `json:"title_id"`
	// the version of the build, null when the database does not know it
	Version *int     `json:"version"`
	BuildId string   `json:"build_id"`
	Names   []string `json:"names"`
	Text    string   `json:"text"`
}

// netCheatsGame is the title ID the cheats are kept under: the one of the game, also for its update.
func netCheatsGame(titleId string) string {
	titleId = strings.ToUpper(titleId)
	if strings.HasSuffix(titleId, "800") {
		return titleId[:13] + "000"
	}
	return titleId
}

// netCheatsVersion is the version of a build of a game, if the database knows it.
func netCheatsVersion(cheats *cheatsDB, titleId string, build string) *int {
	for _, known := range []map[string]map[int]string{cheats.builds, cheats.read} {
		for version, other := range known[titleId] {
			if strings.EqualFold(other, build) {
				found := version
				return &found
			}
		}
	}
	return nil
}

// handleNetCheats gives the cheats of a version, or of a build, of a game.
func (web *Web) handleNetCheats(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	titleId := netCheatsGame(mux.Vars(r)["titleId"])
	query := r.URL.Query()
	answer := netCheats{TitleId: titleId, Names: []string{}}
	cheats := web.cheats()
	switch {
	case query.Get("build") != "":
		answer.BuildId = strings.ToUpper(strings.TrimSpace(query.Get("build")))
		if !netBuildIdPattern.MatchString(answer.BuildId) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the build ID is 16 hexadecimal digits"})
			return
		}
		if cheats != nil {
			answer.Version = netCheatsVersion(cheats, titleId, answer.BuildId)
		}
	default:
		version := 0
		if value := strings.TrimSpace(query.Get("version")); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the version is a number"})
				return
			}
			version = parsed
		} else {
			// the version the library has, as its page shows the cheats
			_, localDB := web.state.get()
			if local := findLocalTitle(localDB, titleId); local != nil {
				version = local.LatestUpdate
			}
		}
		answer.Version = &version
		answer.BuildId = cheats.buildID(titleId, version)
	}
	text := ""
	if answer.BuildId != "" {
		text, _ = cheats.text(titleId, answer.BuildId)
	}
	w.Header().Set("Cache-Control", "no-store")
	if text == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no cheats"})
		return
	}
	answer.Text, answer.Names = text, cheatNames(text)
	writeJSON(w, http.StatusOK, answer)
}

// handleNetCheatsPaths adds the path of the cheats for the console.
func (web *Web) handleNetCheatsPaths() {
	web.router.HandleFunc(netInstallCheats+"{titleId:[0-9A-Fa-f]{16}}", web.handleNetCheats).Methods(http.MethodGet)
}
