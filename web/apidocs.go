package web

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

type ApiShare struct {
	Label string `json:"label"`
	Count int    `json:"count"`
	Size  int64  `json:"size"`
}

type ApiLargestGame struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type ApiStatistics struct {
	Games           int              `json:"games"`
	Updates         int              `json:"updates"`
	Dlc             int              `json:"dlc"`
	TotalSize       int64            `json:"totalSize"`
	GamesUpToDate   int              `json:"gamesUpToDate"`
	GamesWithUpdate int              `json:"gamesWithUpdate"`
	MissingDlc      int              `json:"missingDlc"`
	GamesMissingDlc int              `json:"gamesMissingDlc"`
	MissingGames    int              `json:"missingGames"`
	Issues          int              `json:"issues"`
	ByContent       []ApiShare       `json:"byContent"`
	ByFormat        []ApiShare       `json:"byFormat"`
	Largest         []ApiLargestGame `json:"largest"`
	Synchronizing   bool             `json:"synchronizing"`
	LastSync        *time.Time       `json:"lastSync,omitempty"`
}

func apiShares(shares []SizeShare) []ApiShare {
	result := []ApiShare{}
	for _, share := range shares {
		result = append(result, ApiShare{Label: share.Label, Count: share.Count, Size: share.Size})
	}
	return result
}

func (web *Web) HandleApiDocs() {
	web.router.HandleFunc("/api/statistics", func(w http.ResponseWriter, r *http.Request) {
		lang := r.URL.Query().Get("lang")
		if !isSupportedLanguage(lang) {
			lang = DEFAULT_LANGUAGE
		}
		stats := web.getStatistics(lang)
		response := ApiStatistics{
			Games:           stats.Games,
			Updates:         stats.Updates,
			Dlc:             stats.Dlc,
			TotalSize:       stats.TotalSize,
			GamesUpToDate:   stats.GamesUpToDate,
			GamesWithUpdate: stats.GamesWithUpdate,
			MissingDlc:      stats.MissingDlc,
			GamesMissingDlc: stats.GamesMissingDlc,
			MissingGames:    stats.MissingGames,
			Issues:          stats.Issues,
			ByContent:       apiShares(stats.ByContent),
			ByFormat:        apiShares(stats.ByFormat),
			Largest:         []ApiLargestGame{},
			Synchronizing:   web.state.IsSynchronizing(),
		}
		for _, game := range stats.Largest {
			response.Largest = append(response.Largest, ApiLargestGame(game))
		}
		if last := settings.ReadSettings(web.dataFolder).LastSyncTime; !last.IsZero() {
			response.LastSync = &last
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, response)
	}).Methods("GET")

	web.router.HandleFunc("/api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		spec, err := fs.ReadFile(web.embedFS, "resources/static/openapi.json")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(spec)
	}).Methods("GET")
}
