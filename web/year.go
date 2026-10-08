package web

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// "Your year on Switch" sums up what came to the library in a year: the games, updates and
// DLC found in the folders, the busiest month, the favorite genre, the biggest and the oldest
// game. It only uses the history the app keeps, so the contents found by the very first scan
// (the library that was already there) are not counted as added that year.

const yearNewGames = 12

// YearReview is the summary of a year of the library.
type YearReview struct {
	Year int
	// the years with a summary, newest first
	Years []int
	// the first scan, and whether it was this year
	Since     time.Time
	FirstYear bool

	GamesAdded   int
	UpdatesAdded int
	DlcAdded     int
	// the size of the games added, with their updates and DLC
	SizeAdded int64

	// games added each month, and the month with the most
	Months       []YearMonth
	MaxMonth     int
	BusiestMonth string

	TopGenre          string
	TopGenreGames     int
	TopPublisher      string
	TopPublisherGames int

	Biggest *TitleItem
	Oldest  *TitleItem
	Newest  *TitleItem
	// the latest games added, newest first
	NewGames []TitleItem

	Favorites int
	// the most complete series with more than one game
	Saga *SagaProgress

	// the library at the end of the year (or now), and how many games it grew by
	LibraryGames int
	LibrarySize  int64
	Growth       int
}

// YearMonth is the number of games added in a month.
type YearMonth struct {
	Label string
	Games int
}

// Empty reports whether nothing was added that year.
func (y YearReview) Empty() bool {
	return y.GamesAdded == 0 && y.UpdatesAdded == 0 && y.DlcAdded == 0
}

type YearPageData struct {
	GlobalPageData
	Review YearReview
}

// monthLabel is the short name of a month in a language, for the bars.
func monthLabel(lang string, month time.Month) string {
	if months, ok := monthAbbreviations[lang]; ok {
		return months[month-1]
	}
	if signs, ok := dateSigns[lang]; ok {
		return fmt.Sprintf("%d%s", month, strings.TrimSpace(signs[1]))
	}
	return month.String()[:3]
}

// historySince is when the history started: the first scan. Older history files do not keep
// it, so it is then the earliest time a content was found.
func (h *libraryHistory) since() time.Time {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if !h.data.Since.IsZero() {
		return h.data.Since
	}
	var since time.Time
	for _, entry := range h.data.Contents {
		if since.IsZero() || entry.Time.Before(since) {
			since = entry.Time
		}
	}
	return since
}

// addedIn reports whether a content was found in the year, after the first scan.
func addedIn(added time.Time, since time.Time, year int) bool {
	if added.IsZero() || added.Year() != year {
		return false
	}
	// the contents of the first scan were already there
	return since.IsZero() || added.Sub(since) > time.Minute || added.Sub(since) < -time.Minute
}

// yearsOfHistory are the years from the first scan to now, newest first.
func yearsOfHistory(since time.Time, now time.Time) []int {
	years := []int{}
	first := now.Year()
	if !since.IsZero() && since.Year() < first {
		first = since.Year()
	}
	for year := now.Year(); year >= first; year-- {
		years = append(years, year)
	}
	return years
}

func (web *Web) yearReview(year int, lang string) YearReview {
	return web.derived("year:"+strconv.Itoa(year)+":"+lang, func() any { return web.buildYearReview(year, lang, time.Now()) }).(YearReview)
}

func (web *Web) buildYearReview(year int, lang string, now time.Time) YearReview {
	history := web.history()
	since := history.since()
	review := YearReview{Year: year, Years: yearsOfHistory(since, now), Since: since, FirstYear: !since.IsZero() && since.Year() == year}

	months := make([]int, 12)
	for key, added := range history.addedTimes() {
		if !addedIn(added, since, year) {
			continue
		}
		switch kind, _, _ := historyKind(key); kind {
		case HISTORY_GAME:
			months[added.Month()-1]++
		case HISTORY_UPDATE:
			review.UpdatesAdded++
		case HISTORY_DLC:
			review.DlcAdded++
		}
	}

	switchDB, _ := web.state.get()
	library := web.derived("library:"+lang, func() any { return web.buildLibrary(lang) }).([]TitleItem)
	genres, publishers := map[string]int{}, map[string]int{}
	added := []TitleItem{}
	for _, item := range library {
		if !addedIn(item.Added, since, year) {
			continue
		}
		added = append(added, item)
		review.SizeAdded += item.Size
		if switchDB != nil && len(item.Id) >= 13 {
			if title := switchDB.TitlesMap[strings.ToLower(item.Id[:13])]; title != nil {
				for _, genre := range title.Attributes.Genres {
					genres[genre]++
				}
				if title.Attributes.Publisher != "" {
					publishers[title.Attributes.Publisher]++
				}
			}
		}
	}
	// the months count every game found, also those removed since
	for _, count := range months {
		review.GamesAdded += count
	}
	if review.GamesAdded < len(added) {
		review.GamesAdded = len(added)
	}

	busiest := -1
	for i, count := range months {
		review.Months = append(review.Months, YearMonth{Label: monthLabel(lang, time.Month(i+1)), Games: count})
		if count > review.MaxMonth {
			review.MaxMonth, busiest = count, i
		}
	}
	if busiest >= 0 {
		review.BusiestMonth = formatMonth(lang, time.Date(year, time.Month(busiest+1), 1, 0, 0, 0, 0, time.UTC))
	}

	if top := topCounts(genres, 1); len(top) > 0 {
		review.TopGenre, review.TopGenreGames = top[0].Name, top[0].Count
	}
	if top := topCounts(publishers, 1); len(top) > 0 {
		review.TopPublisher, review.TopPublisherGames = top[0].Name, top[0].Count
	}

	// copies: the list is sorted below
	for _, item := range added {
		item := item
		if review.Biggest == nil || item.Size > review.Biggest.Size {
			review.Biggest = &item
		}
		if !item.ReleaseDate.IsZero() {
			if review.Oldest == nil || item.ReleaseDate.Before(review.Oldest.ReleaseDate) {
				review.Oldest = &item
			}
			if review.Newest == nil || item.ReleaseDate.After(review.Newest.ReleaseDate) {
				review.Newest = &item
			}
		}
	}
	// the oldest and the newest are worth showing only when they differ
	if review.Oldest != nil && review.Newest != nil && review.Oldest.Id == review.Newest.Id {
		review.Newest = nil
	}
	sort.SliceStable(added, func(i, j int) bool { return added[i].Added.After(added[j].Added) })
	if len(added) > yearNewGames {
		added = added[:yearNewGames]
	}
	review.NewGames = added

	for _, marked := range web.favorites().snapshotTimes() {
		if marked.Year() == year {
			review.Favorites++
		}
	}

	for _, saga := range web.sagaProgress(lang) {
		if saga.Owned < 2 {
			continue
		}
		if review.Saga == nil || saga.Percent() > review.Saga.Percent() || (saga.Percent() == review.Saga.Percent() && saga.Owned > review.Saga.Owned) {
			copied := saga
			review.Saga = &copied
		}
	}

	// the size of the library at the end of the year, and at its start
	var first, last *HistoryDay
	days := history.days()
	for i := range days {
		date, err := time.Parse("2006-01-02", days[i].Date)
		if err != nil {
			continue
		}
		if date.Year() < year {
			first = &days[i]
		}
		if date.Year() == year {
			if first == nil {
				first = &days[i]
			}
			last = &days[i]
		}
	}
	if last != nil {
		review.LibraryGames, review.LibrarySize = last.Games, last.Size
		if first != nil {
			review.Growth = last.Games - first.Games
		}
	}
	return review
}

func (web *Web) HandleYear() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/year.html")
	web.router.HandleFunc("/year.html", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		now := time.Now()
		year := now.Year()
		if value, err := strconv.Atoi(r.URL.Query().Get("y")); err == nil && value >= 2017 && value <= now.Year() {
			year = value
		}
		web.render(w, r, templates, YearPageData{GlobalPageData: web.globalPageData("statistics"), Review: web.yearReview(year, lang)})
	}).Methods("GET")
}
