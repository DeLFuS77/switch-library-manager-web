package web

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
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
	Games            int
	Updates          int
	Dlc              int
	TotalSize        int64
	ByContent        []SizeShare
	ByFormat         []SizeShare
	GamesUpToDate    int
	GamesUpToDatePct int
	// the games with a file in the library: the ones marked as owned without one have no version
	GamesWithFile   int
	GamesWithUpdate int
	// the updates the Updates page lists: of games, and of DLC
	UpdatesPending    int
	DlcWithUpdate     int
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
	// the scores of the games on IGDB: the averages of the critics and of the users with how
	// many games they come from, the games with a score by band, and the best ones
	CriticAverage int
	CriticGames   int
	UserAverage   int
	UserGames     int
	ScoredGames   int
	ScoreBands    []ScoreBand
	MaxScoreBand  int
	TopScored     []ScoredGame
	// the folders of the library, in the order of the settings, with their games and the size of
	// the files in them; without their paths, added for the administrators (see folderSpaces)
	Folders []FolderSpace
	// the highest count of each list, for the bars
	MaxGenre     int
	MaxPublisher int
	MaxYear      int
}

// ScoreBand is how many games have a score of a band, of the colours of the scores (see
// scoreLevel): 90 or more, 75 to 89, 50 to 74 and under 50.
type ScoreBand struct {
	Label string
	Level string
	Count int
}

// ScoredGame is a game with its score, and for the comparison with the user's rating, that
// rating in stars and as a score (a star is 20).
type ScoredGame struct {
	Id    string
	Name  string
	Score int
	Stars int
	Mine  int
}

// ScoreComparison sets the ratings of a user ("My rating") against the scores of the same
// games: their averages, and the games the user liked more or less than IGDB, the most first.
type ScoreComparison struct {
	Rated  int
	Mine   int
	Theirs int
	More   []ScoredGame
	Less   []ScoredGame
}

// a rating is told apart from the score of IGDB from this difference on
const scoreDisagreement = 15

type StatisticsPageData struct {
	GlobalPageData
	Stats Statistics
	// the ratings of the user who looks against the scores of IGDB
	Mine ScoreComparison
	// what changed in the folders, newest first, and the library over time
	History []HistoryEvent
	Charts  *HistoryCharts
	// the folders of the library as the user who looks sees them
	Folders []FolderSpace
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
	// every file once in the sizes: a game card can hold the game, its update and DLC
	counted := map[string]bool{}
	// the files of each folder of the library, and its games
	folders := libraryFolders(settings.ReadSettings(web.dataFolder))
	findFolder := folderFinder(folders)
	folderSizes, folderGames := map[string]int64{}, map[string]int{}
	firstTime := func(file db.ExtendedFileInfo) bool {
		path := filepath.Join(file.BaseFolder, file.FileName)
		if counted[path] {
			return false
		}
		counted[path] = true
		if folder := findFolder(file.BaseFolder); folder != nil {
			folderSizes[folder.Id] += file.Size
		}
		return true
	}
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
			if folder := findFolder(local.File.ExtendedInfo.BaseFolder); folder != nil {
				folderGames[folder.Id]++
			}
			if firstTime(local.File.ExtendedInfo) {
				baseSize += local.File.ExtendedInfo.Size
				gameSize += local.File.ExtendedInfo.Size
				addFile(local.File.ExtendedInfo.FileName, local.File.ExtendedInfo.Size)
			}
		}
		for _, update := range local.Updates {
			stats.Updates++
			if firstTime(update.ExtendedInfo) {
				updateSize += update.ExtendedInfo.Size
				gameSize += update.ExtendedInfo.Size
				addFile(update.ExtendedInfo.FileName, update.ExtendedInfo.Size)
			}
		}
		for _, dlc := range local.Dlc {
			stats.Dlc++
			if firstTime(dlc.ExtendedInfo) {
				dlcSize += dlc.ExtendedInfo.Size
				gameSize += dlc.ExtendedInfo.Size
				addFile(dlc.ExtendedInfo.FileName, dlc.ExtendedInfo.Size)
			}
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
		// the games count as the library counts them: without the demos it hides, and up to
		// date as its "Up to date" filter, with every update and DLC
		games, withFile := 0, 0
		// the games of each folder too
		folderGames = map[string]int{}
		leftOut, _ := web.regionCopies(lang)
		merged := web.mergedStatus(lang)
		scores := web.timesToBeat().snapshot()
		critics, users := 0, 0
		bands := map[string]int{}
		for _, item := range library {
			if item.TimeToBeat > 0 {
				stats.TimeToBeat += item.TimeToBeat
				stats.TimeToBeatGames++
			}
			if item.Missing || (hideDemos && item.Demo) || leftOut[item.Id] {
				continue
			}
			games++
			if item.folder != "" && !item.NoFile {
				folderGames[item.folder]++
			}
			if item.Score > 0 {
				value := scores[item.Id]
				if score := value.Critics(); score > 0 {
					critics += score
					stats.CriticGames++
				}
				if score := value.Users(); score > 0 {
					users += score
					stats.UserGames++
				}
				stats.ScoredGames++
				bands[scoreLevel(item.Score)]++
				stats.TopScored = append(stats.TopScored, ScoredGame{Id: item.Id, Name: item.Name, Score: item.Score})
			}
			// a game marked as owned without a file has no version: neither up to date nor behind
			if item.NoFile {
				continue
			}
			// the same for a game whose updates no list knows: its version is not known to be the newest
			if item.VersionsUnknown {
				continue
			}
			withFile++
			if status, ok := merged[item.Id]; ok {
				status.apply(&item)
			}
			if item.UpdateAvailable {
				stats.GamesWithUpdate++
			}
			if item.Known && !item.UpdateAvailable && item.MissingDlcCount == 0 {
				stats.GamesUpToDate++
			}
		}
		stats.Games = games
		if stats.CriticGames > 0 {
			stats.CriticAverage = int(math.Round(float64(critics) / float64(stats.CriticGames)))
		}
		if stats.UserGames > 0 {
			stats.UserAverage = int(math.Round(float64(users) / float64(stats.UserGames)))
		}
		if stats.ScoredGames > 0 {
			for _, band := range []ScoreBand{{"90 or more", "top", 0}, {"75 to 89", "high", 0}, {"50 to 74", "mid", 0}, {"Under 50", "low", 0}} {
				band.Count = bands[band.Level]
				stats.ScoreBands = append(stats.ScoreBands, band)
				stats.MaxScoreBand = max(stats.MaxScoreBand, band.Count)
			}
		}
		sort.Slice(stats.TopScored, func(i, j int) bool {
			if stats.TopScored[i].Score != stats.TopScored[j].Score {
				return stats.TopScored[i].Score > stats.TopScored[j].Score
			}
			return sortName(stats.TopScored[i].Name) < sortName(stats.TopScored[j].Name)
		})
		if len(stats.TopScored) > 10 {
			stats.TopScored = stats.TopScored[:10]
		}
		// the share of the games counts them as the library does, next to its count
		stats.ByContent[0].Count = games
		stats.GamesWithFile = withFile
		stats.GamesUpToDatePct = percent(int64(stats.GamesUpToDate), int64(withFile))
		stats.UpdatesPending = len(web.shownMissingUpdates(lang))
		stats.DlcWithUpdate = max(0, stats.UpdatesPending-stats.GamesWithUpdate)

		for _, title := range web.missingDLC() {
			stats.GamesMissingDlc++
			stats.MissingDlc += len(title.MissingDLCItems)
		}

		stats.MissingGames = web.missingGamesCount()
	}

	for _, folder := range folders {
		stats.Folders = append(stats.Folders, FolderSpace{Id: folder.Id, Name: folder.Name, Games: folderGames[folder.Id], Size: folderSizes[folder.Id]})
	}

	return stats
}

// scoreComparison sets the ratings of a user against the scores of IGDB of the same games of
// the library.
func (web *Web) scoreComparison(user string, lang string) ScoreComparison {
	comparison := ScoreComparison{}
	plays := web.play().All(user)
	if len(plays) == 0 {
		return comparison
	}
	mine, theirs := 0, 0
	for _, item := range web.derived("library:"+lang, func() any { return web.buildLibrary(lang) }).([]TitleItem) {
		rating := plays[item.Id].Rating
		if item.Missing || item.Score == 0 || rating == 0 {
			continue
		}
		game := ScoredGame{Id: item.Id, Name: item.Name, Score: item.Score, Stars: rating, Mine: rating * 20}
		comparison.Rated++
		mine += game.Mine
		theirs += game.Score
		switch difference := game.Mine - game.Score; {
		case difference >= scoreDisagreement:
			comparison.More = append(comparison.More, game)
		case difference <= -scoreDisagreement:
			comparison.Less = append(comparison.Less, game)
		}
	}
	if comparison.Rated == 0 {
		return comparison
	}
	comparison.Mine = int(math.Round(float64(mine) / float64(comparison.Rated)))
	comparison.Theirs = int(math.Round(float64(theirs) / float64(comparison.Rated)))
	// the biggest differences first, three of each
	sort.Slice(comparison.More, func(i, j int) bool {
		return comparison.More[i].Mine-comparison.More[i].Score > comparison.More[j].Mine-comparison.More[j].Score
	})
	sort.Slice(comparison.Less, func(i, j int) bool {
		return comparison.Less[i].Score-comparison.Less[i].Mine > comparison.Less[j].Score-comparison.Less[j].Mine
	})
	comparison.More = comparison.More[:min(len(comparison.More), 3)]
	comparison.Less = comparison.Less[:min(len(comparison.Less), 3)]
	return comparison
}

func (web *Web) HandleStatistics() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/statistics.html")

	web.router.HandleFunc("/statistics.html", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		stats := web.getStatistics(lang)
		web.render(w, r, templates, StatisticsPageData{GlobalPageData: web.globalPageData("statistics"), Stats: stats,
			Mine: web.scoreComparison(playUser(r), lang), History: web.history().recent(50), Charts: historyCharts(web.history().days(), lang),
			Folders: web.folderSpaces(stats, isAdminRequest(r))})
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
