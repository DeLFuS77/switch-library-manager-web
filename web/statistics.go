package web

import (
	"strconv"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// SizeShare is a part of a total, e.g. the space used by updates or by NSZ files.
type SizeShare struct {
	Label   string
	Count   int
	Size    int64
	Percent int
}

type LargestGame struct {
	Id   string
	Name string
	Size int64
}

type Statistics struct {
	Games             int
	Updates           int
	Dlc               int
	TotalSize         int64
	ByContent         []SizeShare
	ByFormat          []SizeShare
	GamesUpToDate     int
	GamesUpToDatePct  int
	GamesWithUpdate   int
	MissingDlc        int
	GamesMissingDlc   int
	MissingGames      int
	Issues            int
	Largest           []LargestGame
	HasTitlesDatabase bool
	// the games of the library by genre, publisher and year of release, the most first
	ByGenre     []NamedCount
	ByPublisher []NamedCount
	ByYear      []NamedCount
	// how long the games with a time to beat (IGDB) take, in seconds, and how many they are
	TimeToBeat      int
	TimeToBeatGames int
	// the highest count of each list, for the bars
	MaxGenre     int
	MaxPublisher int
	MaxYear      int
}

type StatisticsPageData struct {
	GlobalPageData
	Stats Statistics
	// what changed in the folders, newest first, and the number of games over time
	History []HistoryEvent
	Chart   *HistoryChart
}

func percent(part int64, total int64) int {
	if total <= 0 {
		return 0
	}
	return int(part * 100 / total)
}

// getStatistics summarizes the library. Ignored updates and DLC are not counted as missing.
func (web *Web) getStatistics(lang string) Statistics {
	return web.derived("statistics:"+lang, func() any { return web.buildStatistics(lang) }).(Statistics)
}

func (web *Web) buildStatistics(lang string) Statistics {
	stats := Statistics{}
	switchDB, localDB := web.state.get()
	if localDB == nil {
		return stats
	}
	stats.HasTitlesDatabase = switchDB != nil

	var baseSize, updateSize, dlcSize int64
	formats := map[string]*SizeShare{}
	addFile := func(fileName string, size int64) {
		format := strings.ToUpper(strings.TrimPrefix(filepath.Ext(fileName), "."))
		if format == "" || len(format) > 4 {
			// parts of a split file have no format extension
			format = "?"
		}
		share, ok := formats[format]
		if !ok {
			share = &SizeShare{Label: format}
			formats[format] = share
		}
		share.Count++
		share.Size += size
	}

	for id, local := range localDB.TitlesMap {
		var gameSize int64
		if local.BaseExist {
			stats.Games++
			baseSize += local.File.ExtendedInfo.Size
			gameSize += local.File.ExtendedInfo.Size
			addFile(local.File.ExtendedInfo.FileName, local.File.ExtendedInfo.Size)
		}
		for _, update := range local.Updates {
			stats.Updates++
			updateSize += update.ExtendedInfo.Size
			gameSize += update.ExtendedInfo.Size
			addFile(update.ExtendedInfo.FileName, update.ExtendedInfo.Size)
		}
		for _, dlc := range local.Dlc {
			stats.Dlc++
			dlcSize += dlc.ExtendedInfo.Size
			gameSize += dlc.ExtendedInfo.Size
			addFile(dlc.ExtendedInfo.FileName, dlc.ExtendedInfo.Size)
		}

		if local.BaseExist && local.File.Metadata != nil {
			var original string
			if switchDB != nil {
				original = getLocalTitleName(switchDB.TitlesMap[id], local)
			} else {
				original = getLocalTitleName(nil, local)
			}
			stats.Largest = append(stats.Largest, LargestGame{
				Id:   strings.ToUpper(local.File.Metadata.TitleId),
				Name: titleName(switchDB, lang, local.File.Metadata.TitleId, original),
				Size: gameSize,
			})
		}
	}

	if switchDB != nil {
		genres, publishers, years := map[string]int{}, map[string]int{}, map[string]int{}
		for id, local := range localDB.TitlesMap {
			title := switchDB.TitlesMap[id]
			if !local.BaseExist || title == nil {
				continue
			}
			for _, genre := range title.Attributes.Genres {
				genres[genre]++
			}
			if title.Attributes.Publisher != "" {
				publishers[title.Attributes.Publisher]++
			}
			if title.Attributes.ReleaseDate >= 10000000 {
				years[strconv.Itoa(title.Attributes.ReleaseDate/10000)]++
			}
		}
		stats.ByGenre = topCounts(genres, 10)
		stats.ByPublisher = topCounts(publishers, 10)
		stats.ByYear = sortedCounts(years)
		sort.Slice(stats.ByYear, func(i, j int) bool { return stats.ByYear[i].Name < stats.ByYear[j].Name })
		stats.MaxGenre, stats.MaxPublisher, stats.MaxYear = maxCount(stats.ByGenre), maxCount(stats.ByPublisher), maxCount(stats.ByYear)
	}

	stats.TotalSize = baseSize + updateSize + dlcSize
	stats.ByContent = []SizeShare{
		{Label: "Games", Count: stats.Games, Size: baseSize, Percent: percent(baseSize, stats.TotalSize)},
		{Label: "Updates", Count: stats.Updates, Size: updateSize, Percent: percent(updateSize, stats.TotalSize)},
		{Label: "DLC", Count: stats.Dlc, Size: dlcSize, Percent: percent(dlcSize, stats.TotalSize)},
	}
	for _, share := range formats {
		share.Percent = percent(share.Size, stats.TotalSize)
		stats.ByFormat = append(stats.ByFormat, *share)
	}
	sort.Slice(stats.ByFormat, func(i, j int) bool { return stats.ByFormat[i].Size > stats.ByFormat[j].Size })

	sort.Slice(stats.Largest, func(i, j int) bool { return stats.Largest[i].Size > stats.Largest[j].Size })
	if len(stats.Largest) > 10 {
		stats.Largest = stats.Largest[:10]
	}

	stats.Issues = len(web.getIssues())

	if switchDB != nil {
		settingsObj := settings.ReadSettings(web.dataFolder)
		// the same games as the "Update available" filter of the library, so both numbers match
		hideDemos := settingsObj.HideDemoGames
		library := web.derived("library:"+lang, func() any { return web.buildLibrary(lang) }).([]TitleItem)
		for _, item := range library {
			if item.UpdateAvailable && !(hideDemos && item.Demo) {
				stats.GamesWithUpdate++
			}
			if item.TimeToBeat > 0 {
				stats.TimeToBeat += item.TimeToBeat
				stats.TimeToBeatGames++
			}
		}
		stats.GamesUpToDate = stats.Games - stats.GamesWithUpdate
		stats.GamesUpToDatePct = percent(int64(stats.GamesUpToDate), int64(stats.Games))

		for _, title := range web.missingDLC() {
			stats.GamesMissingDlc++
			stats.MissingDlc += len(title.MissingDLCItems)
		}

		for id, title := range switchDB.TitlesMap {
			if title.Attributes.Name == "" || title.Attributes.Id == "" {
				continue
			}
			if settingsObj.HideDemoGames && isDemo(title, title.Attributes.Name) {
				continue
			}
			if local, ok := localDB.TitlesMap[id]; !ok || !local.BaseExist {
				stats.MissingGames++
			}
		}
	}

	return stats
}

func (web *Web) HandleStatistics() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/statistics.html")

	web.router.HandleFunc("/statistics.html", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		web.render(w, r, templates, StatisticsPageData{GlobalPageData: web.globalPageData("statistics"), Stats: web.getStatistics(lang),
			History: web.history().recent(50), Chart: historyChart(web.history().days())})
	}).Methods("GET")
}

// conicGradient draws the shares as a donut chart: a CSS conic gradient with one color
// per share, the colors of the chart legend.
func conicGradient(shares []SizeShare) template.CSS {
	if len(shares) == 0 {
		return template.CSS("--donut-fill: var(--slm-surface-muted)")
	}
	parts := []string{}
	start := 0.0
	total := int64(0)
	for _, share := range shares {
		total += share.Size
	}
	if total == 0 {
		return template.CSS("--donut-fill: var(--slm-surface-muted)")
	}
	for i, share := range shares {
		end := start + float64(share.Size)*100/float64(total)
		parts = append(parts, fmt.Sprintf("var(--slm-chart-%d) %.2f%% %.2f%%", i%6+1, start, end))
		start = end
	}
	return template.CSS("--donut-fill: conic-gradient(" + strings.Join(parts, ", ") + ")")
}

// topCounts returns the most frequent values, at most limit.
func topCounts(counts map[string]int, limit int) []NamedCount {
	sorted := sortedCounts(counts)
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}

func maxCount(counts []NamedCount) int {
	highest := 0
	for _, count := range counts {
		highest = max(highest, count.Count)
	}
	return highest
}

// OlderHistory is the number of changes shown only on request, after the latest ten.
func (d StatisticsPageData) OlderHistory() int {
	return max(len(d.History)-10, 0)
}
