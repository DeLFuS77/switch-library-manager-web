package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// The SD card planner proposes the games of the library to put on a card of a size: the
// favorites and the genres the user likes first, or as many games as fit. The list can be
// changed by hand, downloaded or copied to a folder (the card, or a USB drive), which only
// writes there and never deletes anything.

const (
	TASK_COPY = "copy"
	// a card of "1000 GB" holds about this share of it once formatted
	sdFormattedShare = 0.93
	maxSdGenres      = 8
	// the rows of the page: a big library has thousands of games, which the page would
	// take megabytes to show
	maxSdSelectedRows = 400
	maxSdOtherRows    = 100
	// the genres taken from the favorites when none is chosen
	sdAutoGenres = 3
)

var (
	allowedSdCapacities = map[int]bool{32: true, 64: true, 128: true, 256: true, 400: true, 512: true, 1000: true, 1500: true, 2000: true}
	sdUnsafeName        = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
)

// SdOptions are the choices of the planner, read from the query string.
type SdOptions struct {
	Capacity    int
	ReserveGB   int
	Genres      []string
	AutoGenres  bool
	Multiplayer bool
	Favorites   bool
	Collection  string
	Dlc         bool
	MoreGames   bool
	// a search among the games that are not chosen
	Search string
}

// SdReason is why a game is proposed; Key is translated, Value shown as is or translated.
type SdReason struct {
	Key   string
	Value string
}

// SdGame is a game of the library with the space it takes on the card.
type SdGame struct {
	Id       string
	Name     string
	ImageUrl string
	Size     int64
	Score    int
	Reasons  []SdReason
	Selected bool
}

// SdPlan is the proposed list.
type SdPlan struct {
	Options  SdOptions
	Usable   int64
	Total    int64
	Selected []SdGame
	Others   []SdGame
	// the games chosen and the others, with the ones the page does not list: their
	// identifiers, number and size count in the totals and in the copy
	SelectedCount int
	OthersTotal   int
	HiddenIds     []string
	HiddenSize    int64
	// the genres of the library and the collections, to choose from
	Genres      []NamedCount
	Collections []string
}

type SdPageData struct {
	GlobalPageData
	Plan       SdPlan
	Capacities []int
}

func readSdOptions(query url.Values) SdOptions {
	options := SdOptions{Capacity: 256, ReserveGB: 10, Favorites: true, Dlc: true}
	if capacity, err := strconv.Atoi(query.Get("capacity")); err == nil && allowedSdCapacities[capacity] {
		options.Capacity = capacity
	}
	if reserve, err := strconv.Atoi(query.Get("reserve")); err == nil && reserve >= 0 && reserve <= 200 {
		options.ReserveGB = reserve
	}
	for _, genre := range query["genre"] {
		if knownGenreSet[genre] && len(options.Genres) < maxSdGenres && !hasValue(options.Genres, genre) {
			options.Genres = append(options.Genres, genre)
		}
	}
	// the form was sent: unchecked boxes are off
	if query.Get("planned") == "1" {
		options.Favorites = query.Get("favorites") == "1"
		options.Dlc = query.Get("dlc") == "1"
	}
	options.Multiplayer = query.Get("multiplayer") == "1"
	options.MoreGames = query.Get("strategy") == "more"
	options.Collection = cleanCollectionName(query.Get("collection"))
	if search := strings.TrimSpace(query.Get("q")); len(search) <= 80 {
		options.Search = search
	}
	return options
}

// sdUsable is the space of the card for games.
func sdUsable(options SdOptions) int64 {
	usable := int64(float64(options.Capacity)*1e9*sdFormattedShare) - int64(options.ReserveGB)*1e9
	return max(usable, 0)
}

// sdGameFiles returns the files of a game of the library to put on a card: the base game,
// its newest update and, if asked, its DLC.
func sdGameFiles(local *db.SwitchGameFiles, dlc bool) []db.ExtendedFileInfo {
	files := []db.ExtendedFileInfo{local.File.ExtendedInfo}
	if update, ok := local.Updates[local.LatestUpdate]; ok && local.LatestUpdate > 0 {
		files = append(files, update.ExtendedInfo)
	}
	if dlc {
		ids := make([]string, 0, len(local.Dlc))
		for id := range local.Dlc {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			files = append(files, local.Dlc[id].ExtendedInfo)
		}
	}
	return files
}

func (web *Web) sdPlan(options SdOptions, lang string) SdPlan {
	plan := SdPlan{Options: options, Usable: sdUsable(options), Collections: web.collections().names()}
	switchDB, localDB := web.state.get()
	if localDB == nil {
		return plan
	}
	favorites := web.favorites().snapshot()
	collections := web.collections().snapshot()
	added := web.history().addedTimes()
	recentSince := time.Now().AddDate(0, 0, -recentDays)

	type candidate struct {
		game   SdGame
		genres []string
		fav    bool
	}
	candidates := []candidate{}
	genreCounts := map[string]int{}
	favoriteGenres := map[string]int{}
	for prefix, local := range localDB.TitlesMap {
		if !local.BaseExist || local.File.Metadata == nil {
			continue
		}
		var title *db.SwitchTitle
		if switchDB != nil {
			title = switchDB.TitlesMap[prefix]
		}
		original := getLocalTitleName(title, local)
		if isDemo(title, original) {
			continue
		}
		id := strings.ToUpper(local.File.Metadata.TitleId)
		game := SdGame{Id: id, Name: titleName(switchDB, lang, id, original)}
		if local.Icon != "" {
			game.ImageUrl = "/i/" + local.Icon
		}
		for _, file := range sdGameFiles(local, options.Dlc) {
			game.Size += file.Size
		}
		c := candidate{game: game, fav: favorites[id]}
		if title != nil {
			c.genres = title.Attributes.Genres
			if options.Multiplayer && title.Attributes.Players >= 2 {
				c.game.Score += 20
				c.game.Reasons = append(c.game.Reasons, SdReason{Key: "Multiplayer"})
			}
		}
		for _, genre := range c.genres {
			genreCounts[genre]++
			if c.fav {
				favoriteGenres[genre]++
			}
		}
		if c.fav && options.Favorites {
			c.game.Score += 100
			c.game.Reasons = append([]SdReason{{Key: "Favorite"}}, c.game.Reasons...)
		}
		if options.Collection != "" {
			for _, name := range collections[id] {
				if strings.EqualFold(name, options.Collection) {
					c.game.Score += 60
					c.game.Reasons = append(c.game.Reasons, SdReason{Key: "Collection", Value: name})
				}
			}
		}
		if added["game:"+id].After(recentSince) {
			c.game.Score += 10
			c.game.Reasons = append(c.game.Reasons, SdReason{Key: "Recently added"})
		}
		candidates = append(candidates, c)
	}
	plan.Genres = sortedCounts(genreCounts)

	// without chosen genres, the genres of the favorites
	if len(options.Genres) == 0 && len(favoriteGenres) > 0 {
		for i, genre := range sortedCounts(favoriteGenres) {
			if i == sdAutoGenres {
				break
			}
			options.Genres = append(options.Genres, genre.Name)
		}
		options.AutoGenres = true
		plan.Options = options
	}
	for i := range candidates {
		for _, genre := range candidates[i].genres {
			if hasValue(options.Genres, genre) {
				candidates[i].game.Score += 25
				candidates[i].game.Reasons = append(candidates[i].game.Reasons, SdReason{Key: "Genre", Value: genre})
			}
		}
	}

	games := make([]SdGame, len(candidates))
	for i, c := range candidates {
		games[i] = c.game
	}
	if options.MoreGames {
		// the liked games first, the smallest of each first, so more of them fit
		sort.SliceStable(games, func(i, j int) bool {
			if (games[i].Score > 0) != (games[j].Score > 0) {
				return games[i].Score > 0
			}
			return games[i].Size < games[j].Size
		})
	} else {
		sort.SliceStable(games, func(i, j int) bool {
			if games[i].Score != games[j].Score {
				return games[i].Score > games[j].Score
			}
			return strings.ToLower(games[i].Name) < strings.ToLower(games[j].Name)
		})
	}
	for _, game := range games {
		if plan.Total+game.Size <= plan.Usable {
			game.Selected = true
			plan.Total += game.Size
			plan.Selected = append(plan.Selected, game)
		} else {
			plan.Others = append(plan.Others, game)
		}
	}

	// the page lists the first games only; the totals count all of them
	plan.SelectedCount = len(plan.Selected)
	if len(plan.Selected) > maxSdSelectedRows {
		for _, game := range plan.Selected[maxSdSelectedRows:] {
			plan.HiddenIds = append(plan.HiddenIds, game.Id)
			plan.HiddenSize += game.Size
		}
		plan.Selected = plan.Selected[:maxSdSelectedRows]
	}
	if options.Search != "" {
		search := newSearchQuery(options.Search)
		found := plan.Others[:0:0]
		for _, game := range plan.Others {
			text := searchText(game.Name)
			if search.matches(text, strings.Fields(text)) {
				found = append(found, game)
			}
		}
		plan.Others = found
	}
	plan.OthersTotal = len(plan.Others)
	if len(plan.Others) > maxSdOtherRows {
		plan.Others = plan.Others[:maxSdOtherRows]
	}
	return plan
}

// sdTarget checks the folder the games are copied to.
func sdTarget(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || strings.Contains(path, "..") || !filepath.IsAbs(path) {
		return "", fmt.Errorf("the folder must be an absolute path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("the folder does not exist")
	}
	return filepath.Clean(path), nil
}

// sdFolderName is a game name that can be a folder on a FAT32 or exFAT card.
func sdFolderName(name string) string {
	name = strings.TrimSpace(sdUnsafeName.ReplaceAllString(name, " "))
	name = strings.Trim(strings.Join(strings.Fields(name), " "), ". ")
	if name == "" {
		return "Game"
	}
	if len(name) > 100 {
		name = strings.TrimSpace(name[:100])
	}
	return name
}

type sdCopyFile struct {
	source string
	target string
	size   int64
}

// startSdCopy copies the files of the games to the folder in the background, a folder per
// game. Files already there with the same size are skipped. It returns false if a
// compression, verification or copy is already running.
func (web *Web) startSdCopy(ids []string, target string, dlc bool, lang string) bool {
	switchDB, localDB := web.state.get()
	if localDB == nil {
		return false
	}
	files := []sdCopyFile{}
	var total int64
	for _, id := range ids {
		prefix, err := db.TitleIDPrefix(id)
		if err != nil {
			continue
		}
		local, ok := localDB.TitlesMap[prefix]
		if !ok || !local.BaseExist || local.File.Metadata == nil {
			continue
		}
		var title *db.SwitchTitle
		if switchDB != nil {
			title = switchDB.TitlesMap[prefix]
		}
		folder := filepath.Join(target, sdFolderName(titleName(switchDB, lang, id, getLocalTitleName(title, local))))
		for _, file := range sdGameFiles(local, dlc) {
			files = append(files, sdCopyFile{
				source: filepath.Join(file.BaseFolder, file.FileName),
				target: filepath.Join(folder, file.FileName),
				size:   file.Size,
			})
			total += file.Size
		}
	}
	if len(files) == 0 {
		return false
	}

	web.compressor.mutex.Lock()
	if web.compressor.cancel != nil {
		web.compressor.mutex.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	web.compressor.cancel = cancel
	web.compressor.mutex.Unlock()

	taskId := web.taskLog().Start(TASK_COPY, TRIGGER_MANUAL)
	go func() {
		defer func() {
			web.compressor.mutex.Lock()
			web.compressor.cancel = nil
			web.compressor.mutex.Unlock()
			cancel()
		}()
		var done int64
		copied := 0
		var failure *TaskNote
		for i, file := range files {
			if ctx.Err() != nil {
				failure = &TaskNote{Text: NOTE_COMPRESS_CANCELED}
				break
			}
			name := filepath.Base(file.source)
			base := done
			err := copyFileTo(ctx, file.source, file.target, func(written int64) {
				web.taskLog().Progress(taskId, int((base+written)>>20), int(total>>20), fmt.Sprintf("%s (%d/%d)", name, i+1, len(files)))
			})
			done += file.size
			if err != nil {
				if ctx.Err() != nil {
					failure = &TaskNote{Text: NOTE_COMPRESS_CANCELED}
					break
				}
				failure = &TaskNote{Text: NOTE_SD_COPY_FAILED, Detail: name + ": " + err.Error()}
				break
			}
			copied++
			web.taskLog().SetResult(taskId, 0, copied)
		}
		web.taskLog().Finish(taskId, failure)
	}()
	return true
}

const NOTE_SD_COPY_FAILED = "The files could not be copied."

// copyFileTo copies a file through a temporary file; a file of the same size already there
// is kept.
func copyFileTo(ctx context.Context, source string, target string, progress func(int64)) error {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if info, err := os.Stat(target); err == nil && info.Size() == sourceInfo.Size() {
		progress(info.Size())
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temporary := target + ".part"
	output, err := switchfs.CreateNew(temporary)
	if err != nil {
		return err
	}
	buffer := make([]byte, 4<<20)
	var written int64
	for {
		if ctx.Err() != nil {
			output.Close()
			os.Remove(temporary)
			return ctx.Err()
		}
		n, readErr := input.Read(buffer)
		if n > 0 {
			if _, err := output.Write(buffer[:n]); err != nil {
				output.Close()
				os.Remove(temporary)
				return err
			}
			written += int64(n)
			progress(written)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			output.Close()
			os.Remove(temporary)
			return readErr
		}
	}
	if err := output.Close(); err != nil {
		os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, target)
}

func (web *Web) HandleSdCard() {
	// parsed when first shown, so the routes work without the page (tests)
	var templates templateSet
	var parse sync.Once

	web.router.HandleFunc("/sd.html", func(w http.ResponseWriter, r *http.Request) {
		parse.Do(func() {
			templates = web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/sd.html")
		})
		lang := web.requestLanguage(r)
		capacities := make([]int, 0, len(allowedSdCapacities))
		for capacity := range allowedSdCapacities {
			capacities = append(capacities, capacity)
		}
		sort.Ints(capacities)
		web.render(w, r, templates, SdPageData{GlobalPageData: web.globalPageData("sd"), Plan: web.sdPlan(readSdOptions(r.URL.Query()), lang), Capacities: capacities})
	}).Methods("GET")

	// the chosen games as a text file: one line per game with its files
	web.router.HandleFunc("/sd/list.txt", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switchDB, localDB := web.state.get()
		lang := web.requestLanguage(r)
		dlc := r.FormValue("dlc") == "1"
		var builder strings.Builder
		var total int64
		for _, id := range r.Form["id"] {
			prefix, err := db.TitleIDPrefix(id)
			if err != nil || localDB == nil {
				continue
			}
			local, ok := localDB.TitlesMap[prefix]
			if !ok || !local.BaseExist {
				continue
			}
			var title *db.SwitchTitle
			if switchDB != nil {
				title = switchDB.TitlesMap[prefix]
			}
			builder.WriteString(titleName(switchDB, lang, id, getLocalTitleName(title, local)) + " [" + strings.ToUpper(id) + "]\n")
			for _, file := range sdGameFiles(local, dlc) {
				builder.WriteString("  " + filepath.Join(file.BaseFolder, file.FileName) + "\n")
				total += file.Size
			}
		}
		builder.WriteString("\n" + formatSize(total) + "\n")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"sd-card.txt\"")
		io.WriteString(w, builder.String())
	}).Methods("POST")

	web.router.HandleFunc("/sd/copy", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		lang := web.requestLanguage(r)
		target, err := sdTarget(r.FormValue("target"))
		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, "The folder does not exist or is not an absolute path.")
			return
		}
		ids := []string{}
		for _, id := range r.Form["id"] {
			if titleIdPattern.MatchString(strings.ToUpper(id)) {
				ids = append(ids, strings.ToUpper(id))
			}
		}
		if len(ids) == 0 {
			writeGlobalError(w, http.StatusBadRequest, lang, "Choose at least one game.")
			return
		}
		if !web.startSdCopy(ids, target, r.FormValue("dlc") == "1", lang) {
			writeGlobalError(w, http.StatusConflict, lang, "A compression is already running.")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
	}).Methods("POST")
}
