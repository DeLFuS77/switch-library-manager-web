package web

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// The quick search (Ctrl+K, or the magnifier in the header) finds games, series, collections
// and pages from any page, so a page is reached without going through the menus.

const (
	quickSearchGames       = 8
	quickSearchSeries      = 4
	quickSearchCollections = 4
	quickSearchPages       = 6
	// the length of a search, like the filters of the lists
	maxQuickSearchLength = 80
)

// QuickResult is one result of the quick search.
type QuickResult struct {
	Label string `json:"label"`
	Href  string `json:"href"`
	// a cover, or an icon (Bootstrap Icons) when there is none
	Image string `json:"image,omitempty"`
	Icon  string `json:"icon,omitempty"`
	Meta  string `json:"meta,omitempty"`
}

// QuickResults are the results, by kind, in the order they are shown.
type QuickResults struct {
	Games       []QuickResult `json:"games"`
	Series      []QuickResult `json:"series"`
	Collections []QuickResult `json:"collections"`
	Pages       []QuickResult `json:"pages"`
}

// quickPage is a page or a section of Settings that the quick search finds by its name or by
// other words for it.
type quickPage struct {
	label string
	href  string
	icon  string
	admin bool
	// other words for the page, in English (the label is also searched in English)
	words string
	// shown when nothing is typed yet
	suggested bool
}

var quickPages = []quickPage{
	{label: "Library", href: "/index.html", icon: "bi-collection", words: "games home", suggested: true},
	{label: "Updates", href: "/updates.html", icon: "bi-arrow-up-circle", words: "versions patches", suggested: true},
	{label: "DLC", href: "/dlc.html", icon: "bi-puzzle", words: "add-ons content"},
	{label: "Missing Games", href: "/missing.html", icon: "bi-search-heart", words: "wishlist", suggested: true},
	{label: "Upcoming", href: "/upcoming.html", icon: "bi-calendar-event", words: "releases calendar coming"},
	{label: "Series", href: "/sagas.html", icon: "bi-stack", words: "sagas franchises"},
	{label: "Statistics", href: "/statistics.html", icon: "bi-bar-chart", words: "charts numbers", suggested: true},
	{label: "Your year on Switch", href: "/year.html", icon: "bi-stars", words: "year review wrapped summary"},
	{label: "Issues", href: "/issues.html", icon: "bi-exclamation-triangle", words: "problems errors"},
	{label: "Tasks", href: "/tasks.html", icon: "bi-list-task", words: "jobs progress"},
	{label: "Organize", href: "/organize.html", icon: "bi-folder-symlink", admin: true, words: "rename move folders files"},
	{label: "Compress", href: "/compress.html", icon: "bi-file-zip", admin: true, words: "nsz xcz decompress"},
	{label: "Space", href: "/space.html", icon: "bi-hdd", admin: true, words: "disk clean storage"},
	{label: "SD card planner", href: "/sd.html", icon: "bi-sd-card", admin: true, words: "microsd"},
	{label: "Settings", href: "/settings.html#library", icon: "bi-gear", admin: true, words: "options preferences folder keys", suggested: true},
	{label: "Ignored items", href: "/settings.html#ignored", icon: "bi-eye-slash", admin: true, words: "settings hidden"},
	{label: "General", href: "/settings.html#general", icon: "bi-sliders", admin: true, words: "settings language port"},
	{label: "Background work", href: "/settings.html#background", icon: "bi-moon-stars", admin: true, words: "settings schedule automatic"},
	{label: "Compression", href: "/settings.html#compression", icon: "bi-file-zip", admin: true, words: "settings nsz"},
	{label: "Setup wizard", href: "/settings.html#wizard", icon: "bi-signpost-split", admin: true, words: "setup assistant first start configure guide"},
	{label: "Save backups", href: "/saves.html", icon: "bi-safe", admin: true, words: "saves jksv backup vault partidas webdav"},
	{label: "Save vault", href: "/settings.html#vault", icon: "bi-safe", admin: true, words: "settings saves jksv webdav backup"},
	{label: "Automations", href: "/settings.html#automation", icon: "bi-magic", admin: true, words: "settings automatic rules auto organize compress"},
	{label: "Notifications", href: "/settings.html#notifications", icon: "bi-bell", admin: true, words: "settings telegram discord webhook"},
	{label: "Backup", href: "/settings.html#backup", icon: "bi-life-preserver", admin: true, words: "settings restore copy"},
	{label: "Diagnostics", href: "/diagnostics.html", icon: "bi-heart-pulse", admin: true, words: "health status logs"},
	{label: "Users", href: "/users.html", icon: "bi-people", admin: true, words: "accounts login password"},
	{label: "My account", href: "/account.html", icon: "bi-person-gear", words: "password language profile"},
}

// quickEntry is a game or a series prepared for the search.
type quickEntry struct {
	result QuickResult
	// search text of the name, and of the name with the other words
	name  string
	key   string
	words []string
}

// quickRank orders the matches: the name itself, then names that start with the search, then
// a word that starts with it, then the rest; -1 when it does not match.
func quickRank(query *searchQuery, entry *quickEntry) int {
	if query.text == "" || !query.matches(entry.key, entry.words) {
		return -1
	}
	// a Title ID is found as it is written, not one that differs in a few digits
	if titleIdPattern.MatchString(strings.ToUpper(query.text)) && !strings.Contains(entry.key, query.text) {
		return -1
	}
	switch {
	case entry.name == query.text:
		return 0
	case strings.HasPrefix(entry.name, query.text):
		return 1
	case strings.Contains(" "+entry.name, " "+query.text):
		return 2
	case strings.Contains(entry.name, query.text):
		return 3
	}
	return 4
}

// quickBest returns the best matches of the entries, at most limit.
func quickBest(query *searchQuery, entries []quickEntry, limit int) []QuickResult {
	type match struct {
		rank  int
		index int
	}
	matches := []match{}
	for i := range entries {
		if rank := quickRank(query, &entries[i]); rank >= 0 {
			matches = append(matches, match{rank, i})
		}
	}
	// the entries are sorted by name, so equal ranks keep that order
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].rank < matches[j].rank })
	results := []QuickResult{}
	for _, m := range matches {
		if len(results) == limit {
			break
		}
		results = append(results, entries[m.index].result)
	}
	return results
}

func newQuickEntry(result QuickResult, name string, other string) quickEntry {
	key := searchText(name + " " + other)
	return quickEntry{result: result, name: searchText(name), key: key, words: strings.Fields(key)}
}

// quickGames are the games of the library prepared for the search, sorted by name.
func (web *Web) quickGames(lang string) []quickEntry {
	return web.derived("quick-games:"+lang, func() any {
		items := web.derived("library:"+lang, func() any { return web.buildLibrary(lang) }).([]TitleItem)
		entries := make([]quickEntry, 0, len(items))
		for _, item := range items {
			image := ""
			if item.ImageUrl != "" {
				image = thumbUrl(item.ImageUrl)
			}
			meta := item.Id
			if item.Version != "" {
				meta += " · v" + item.Version
			}
			entries = append(entries, newQuickEntry(QuickResult{Label: item.Name, Href: "/title/" + item.Id + ".html", Image: image, Icon: "bi-controller", Meta: meta}, item.Name, item.Id+" "+item.OriginalName))
		}
		sort.SliceStable(entries, func(i, j int) bool { return sortName(entries[i].result.Label) < sortName(entries[j].result.Label) })
		return entries
	}).([]quickEntry)
}

// quickSeries are the series prepared for the search, sorted by name.
func (web *Web) quickSeries(lang string) []quickEntry {
	return web.derived("quick-series:"+lang, func() any {
		sagas := web.sagaProgress(lang)
		entries := make([]quickEntry, 0, len(sagas))
		for _, saga := range sagas {
			values := url.Values{"q": {saga.Name}}
			if saga.Complete() {
				values.Set("status", STATUS_COMPLETE)
			}
			image := ""
			if saga.ImageUrl != "" {
				image = thumbUrl(saga.ImageUrl)
			}
			entries = append(entries, newQuickEntry(QuickResult{Label: saga.Name, Href: "/sagas.html?" + values.Encode(), Image: image, Icon: "bi-stack", Meta: translatef(lang, "%v of %v", saga.Owned, saga.Total)}, saga.Name, ""))
		}
		sort.SliceStable(entries, func(i, j int) bool { return sortName(entries[i].result.Label) < sortName(entries[j].result.Label) })
		return entries
	}).([]quickEntry)
}

// quickCollections are the collections with the number of their games, sorted by name.
func (web *Web) quickCollections(lang string) []quickEntry {
	counts := map[string]int{}
	for _, names := range web.collections().snapshot() {
		for _, name := range names {
			counts[name]++
		}
	}
	entries := []quickEntry{}
	for _, name := range web.collections().names() {
		href := "/index.html?" + url.Values{"collection": {name}}.Encode()
		entries = append(entries, newQuickEntry(QuickResult{Label: name, Href: href, Icon: "bi-bookmark", Meta: translatef(lang, "%v games", counts[name])}, name, ""))
	}
	return entries
}

// quickPagesFor are the pages the user may open, found by the search, or the suggested ones
// when nothing is typed.
func quickPagesFor(query *searchQuery, lang string, admin bool) []QuickResult {
	entries := []quickEntry{}
	for _, page := range quickPages {
		if page.admin && !admin {
			continue
		}
		label := translate(lang, page.label)
		result := QuickResult{Label: label, Href: page.href, Icon: page.icon}
		if query.text == "" {
			if page.suggested {
				entries = append(entries, quickEntry{result: result})
			}
			continue
		}
		entries = append(entries, newQuickEntry(result, label, page.label+" "+page.words))
	}
	if query.text == "" {
		results := []QuickResult{}
		for _, entry := range entries {
			results = append(results, entry.result)
		}
		return results
	}
	return quickBest(query, entries, quickSearchPages)
}

// quickSearch finds what matches a search, for a user who is an administrator or not.
func (web *Web) quickSearch(keyword string, lang string, admin bool) QuickResults {
	query := newSearchQuery(keyword)
	results := QuickResults{Games: []QuickResult{}, Series: []QuickResult{}, Collections: []QuickResult{}}
	results.Pages = quickPagesFor(query, lang, admin)
	if query.text == "" {
		return results
	}
	results.Games = quickBest(query, web.quickGames(lang), quickSearchGames)
	results.Series = quickBest(query, web.quickSeries(lang), quickSearchSeries)
	results.Collections = quickBest(query, web.quickCollections(lang), quickSearchCollections)
	return results
}

func (web *Web) HandleQuickSearch() {
	web.router.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		keyword := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(keyword) > maxQuickSearchLength {
			keyword = keyword[:maxQuickSearchLength]
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, web.quickSearch(keyword, web.requestLanguage(r), principalFrom(r).IsAdmin()))
	}).Methods("GET")
}
