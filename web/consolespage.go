package web

import (
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The page Consoles (/consoles.html): the consoles of the user who looks, as NXDock tells them, one
// card each: the system (emuMMC or sysMMC, the signature patches, the firmware, Atmosphère and the
// free space), when it told it last, and what the library has newer for it, with Bring up to date
// (see handleNetConsoleUpdate). ?console= shows what is installed on one of them, game by game,
// sorted with ?sort=, with how much each was played and Remove from console (see netremove.go).
// The menu has it once the user has paired a console (AuthInfo.Consoles). No path is shown.

const (
	consolesPage = "/consoles.html"
	// the games out of date named on the card of a console
	consoleCardGames = 2
	// the orders of what is installed on a console
	consoleSortName     = "name"
	consoleSortPlayed   = "played"
	consoleSortRecent   = "recent"
	consoleSortOutdated = "outdated"
)

// ConsolesPageData is the page Consoles.
type ConsolesPageData struct {
	GlobalPageData
	// the network install is on: the consoles may be brought up to date and asked to remove a game
	Enabled bool
	// the list of files of the library is being made: what it has newer is not counted yet, and
	// the page asks again in a moment
	Counting bool
	Consoles []ConsoleCard
	// what is installed on the console chosen (?console=), or nil
	Installed *ConsoleInstalled
}

// ConsoleCard is a console on the page Consoles.
type ConsoleCard struct {
	Id   string
	Name string
	// it told what is installed on it, when ("Seen 2 h ago"), and how many games
	Told   bool
	Seen   string
	Titles int
	// its system, when it told it, and whether it has the signature patches
	System     *netConsoleFacts
	Sigpatches bool
	Sd         ConsoleSpace
	Nand       ConsoleSpace
	// the files sent that wait for it
	Waiting int
	// what the library has newer for it is counted: the games out of date, the files that bring
	// them up to date and their size, and whether they fit on its SD card (nil when not known)
	Counted      bool
	Outdated     int
	Updates      int
	Dlc          int
	ToUpdateSize int64
	Fits         *bool
	// the first games out of date, and how many more there are
	Games []ConsoleOutdated
	More  int
	// the reports it sent (#205), for an administrator: how many, and the newest
	Reports    int
	LastReport *netReportRow
}

// ConsoleSpace is the free space of a storage of a console.
type ConsoleSpace struct {
	Free  int64
	Total int64
	// the share used, in percent
	Used int
}

// Known reports whether the console told the space.
func (s ConsoleSpace) Known() bool {
	return s.Total > 0
}

// ConsoleOutdated is a game out of date on a console: from the version installed to the newest
// update of the library, and the DLC it lacks.
type ConsoleOutdated struct {
	Id    string
	Name  string
	Cover string
	// the version installed and the newest the library has (0 without a newer update)
	From int
	To   int
	// the DLC of the library it lacks
	MissingDlc int
	// the size of the files that bring it up to date
	Size int64
}

// ConsoleInstalled is what is installed on a console, game by game.
type ConsoleInstalled struct {
	Id   string
	Name string
	Sort string
	// the library was compared: what is out of date is known
	Compared bool
	Games    []ConsoleGameRow
	// how much the console was played, summed
	Minutes  int64
	Launches int64
	Played   int
	Last     time.Time
	LastText string
}

// ConsoleGameRow is a game installed on a console, as the page lists it.
type ConsoleGameRow struct {
	// the title ID of the game, and the one of its page when the library or the titles database
	// knows it
	Id    string
	Link  string
	Name  string
	Cover string
	// the version installed, and the newest the library has when it is newer
	Version int
	Newer   int
	Storage string
	// the DLC installed, and the ones of the library it lacks
	Dlc        int
	MissingDlc int
	Outdated   bool
	// it was asked to remove it, and has not answered
	Removing bool
	// how much it was played on the console ("34 h · 57 times · last played 3 days ago")
	Minutes    int64
	LastPlayed int64
	PlayText   string
}

// consoleSorts are the orders of what is installed on a console.
var consoleSorts = map[string]bool{consoleSortName: true, consoleSortPlayed: true, consoleSortRecent: true, consoleSortOutdated: true}

// ConsoleSortChoice is an order of what is installed on a console, for its page.
type ConsoleSortChoice struct {
	Value string
	Name  string
	Icon  string
}

// consoleSortChoices are the orders of what is installed on a console, as the page offers them.
func consoleSortChoices() []ConsoleSortChoice {
	return []ConsoleSortChoice{{consoleSortName, "Name", "bi-sort-alpha-down"}, {consoleSortPlayed, "Most played", "bi-joystick"},
		{consoleSortRecent, "Played lately", "bi-clock-history"}, {consoleSortOutdated, "Out of date first", "bi-arrow-up-circle"}}
}

// consoleFilesText tells the files that bring a console up to date: "5 updates and 3 DLC".
func consoleFilesText(lang string, updates int, dlc int) string {
	parts := []string{}
	switch {
	case updates == 1:
		parts = append(parts, i18n.Translate(lang, "1 update"))
	case updates > 1:
		parts = append(parts, i18n.Translatef(lang, "%v updates", i18n.FormatNumber(lang, updates)))
	}
	if dlc > 0 {
		parts = append(parts, i18n.Translatef(lang, "%v DLC", i18n.FormatNumber(lang, dlc)))
	}
	if len(parts) == 2 {
		return i18n.Translatef(lang, "%v and %v", parts[0], parts[1])
	}
	return strings.Join(parts, "")
}

// agoText tells how long ago something was, in the language of the page: "just now", "5 min ago",
// "2 h ago", "yesterday", "3 days ago", and the date past a month.
func agoText(lang string, when time.Time, now time.Time) string {
	since := now.Sub(when)
	switch {
	case since < 2*time.Minute:
		return i18n.Translate(lang, "just now")
	case since < time.Hour:
		return i18n.Translatef(lang, "%v min ago", int(since.Minutes()))
	case since < 24*time.Hour:
		return i18n.Translatef(lang, "%v h ago", int(since.Hours()))
	case since < 48*time.Hour:
		return i18n.Translate(lang, "yesterday")
	case since < 31*24*time.Hour:
		return i18n.Translatef(lang, "%v days ago", int(since.Hours()/24))
	}
	return i18n.Translatef(lang, "on %v", i18n.FormatDate(lang, when.Local()))
}

// playedTime is a time played, in minutes, as the pages tell it: "45 min", "34 h".
func playedTime(lang string, minutes int64) string {
	if minutes < 60 {
		return i18n.Translatef(lang, "%v min", minutes)
	}
	return i18n.Translatef(lang, "%v h", i18n.FormatNumber(lang, int(math.Round(float64(minutes)/60))))
}

// launchesText is how many times a game was started: "once", "57 times".
func launchesText(lang string, launches int64) string {
	if launches == 1 {
		return i18n.Translate(lang, "once")
	}
	return i18n.Translatef(lang, "%v times", i18n.FormatNumber(lang, int(launches)))
}

// playText is how much a game was played: "34 h · 57 times · last played 3 days ago", what of it
// is known; "" when it was not played.
func playText(lang string, stats netPlayStats, now time.Time) string {
	parts := []string{}
	if stats.Minutes > 0 {
		parts = append(parts, playedTime(lang, stats.Minutes))
	}
	if stats.Launches > 0 {
		parts = append(parts, launchesText(lang, stats.Launches))
	}
	if stats.LastPlayed > 0 {
		parts = append(parts, i18n.Translatef(lang, "last played %v", agoText(lang, time.Unix(stats.LastPlayed, 0), now)))
	}
	return strings.Join(parts, " · ")
}

// consoleSpace is a storage of a console, with the share used.
func consoleSpace(free int64, total int64) ConsoleSpace {
	space := ConsoleSpace{Free: free, Total: total}
	if total > 0 {
		space.Used = int((total - free) * 100 / total)
	}
	return space
}

// hasSigpatches tells whether a console told it has the signature patches: their name, not "none".
func hasSigpatches(system *netConsoleFacts) bool {
	return system != nil && system.Sigpatches != "" && !strings.EqualFold(system.Sigpatches, "none")
}

// consoleLook is the name, the cover and the page of a game installed on a console, as the
// Library shows them.
func consoleLook(looks map[string]TitleItem, game netConsoleGame) (name string, cover string, link string) {
	name = game.Name
	if look, ok := looks[netGamePrefix(game.TitleId)]; ok {
		if look.Name != "" {
			name = look.Name
		}
		if look.ImageUrl != "" {
			cover = thumbUrl(look.ImageUrl)
		}
		link = "/title/" + look.Id + ".html"
	}
	if name == "" {
		name = game.TitleId
	}
	return name, cover, link
}

// countConsoleCard adds to the card of a console what the library has newer for it.
func countConsoleCard(card *ConsoleCard, games []netConsoleGame, files []netUpdateFile, system *netConsoleFacts, looks map[string]TitleItem) {
	card.Counted = true
	sizes, newest := map[string]int64{}, map[string]int{}
	for _, file := range files {
		prefix := netGamePrefix(file.TitleId)
		sizes[prefix] += file.Size
		card.ToUpdateSize += file.Size
		if file.Kind == "update" {
			card.Updates++
			newest[prefix] = max(newest[prefix], file.Version)
		} else {
			card.Dlc++
		}
	}
	for _, game := range games {
		if !game.Outdated {
			continue
		}
		card.Outdated++
		if len(card.Games) == consoleCardGames {
			card.More++
			continue
		}
		prefix := netGamePrefix(game.TitleId)
		name, cover, _ := consoleLook(looks, game)
		card.Games = append(card.Games, ConsoleOutdated{Id: game.TitleId, Name: name, Cover: cover, From: game.Version, To: newest[prefix],
			MissingDlc: len(game.MissingDlc), Size: sizes[prefix]})
	}
	if system != nil && system.SdTotal > 0 {
		fits := card.ToUpdateSize <= system.SdFree
		card.Fits = &fits
	}
}

// consoleInstalled is what is installed on a console, game by game, in an order.
func consoleInstalled(key KeyRow, inventory *netInventory, games []netConsoleGame, compared bool, removals map[string]bool,
	looks map[string]TitleItem, order string, lang string, now time.Time) *ConsoleInstalled {
	installed := &ConsoleInstalled{Id: key.ID, Name: key.Name, Sort: order, Compared: compared, Games: []ConsoleGameRow{}}
	for _, game := range games {
		name, cover, link := consoleLook(looks, game)
		row := ConsoleGameRow{Id: game.TitleId, Link: link, Name: name, Cover: cover, Version: game.Version, Storage: game.Storage,
			Dlc: game.Dlc, MissingDlc: len(game.MissingDlc), Outdated: game.Outdated, Removing: removals[game.TitleId]}
		if game.Newest > game.Version {
			row.Newer = game.Newest
		}
		if game.Play != nil {
			row.Minutes, row.LastPlayed = game.Play.Minutes, game.Play.LastPlayed
			row.PlayText = playText(lang, *game.Play, now)
		}
		installed.Games = append(installed.Games, row)
	}
	// the time played of every title the console told, also those no longer installed
	for _, played := range netPlayByGame(inventory.Play) {
		installed.Minutes += played.Minutes
		installed.Launches += played.Launches
		if played.Minutes > 0 || played.Launches > 0 {
			installed.Played++
		}
		if last := time.Unix(played.LastPlayed, 0); played.LastPlayed > 0 && last.After(installed.Last) {
			installed.Last = last
		}
	}
	if !installed.Last.IsZero() {
		installed.LastText = agoText(lang, installed.Last, now)
	}
	sortConsoleGames(installed.Games, order)
	return installed
}

// sortConsoleGames puts what is installed on a console in an order: by name (as it comes), the
// most played first, the played lately first, or the ones out of date first; the others by name.
func sortConsoleGames(games []ConsoleGameRow, order string) {
	sort.SliceStable(games, func(i, j int) bool {
		a, b := games[i], games[j]
		switch order {
		case consoleSortPlayed:
			return a.Minutes > b.Minutes
		case consoleSortRecent:
			return a.LastPlayed > b.LastPlayed
		case consoleSortOutdated:
			return a.Outdated && !b.Outdated
		}
		return false
	})
}

// consolesPage is the page Consoles of the user of a request.
func (web *Web) consolesPage(r *http.Request, lang string, now time.Time) ConsolesPageData {
	appSettings := settings.ReadSettings(web.dataFolder)
	data := ConsolesPageData{GlobalPageData: web.globalPageData("consoles"), Enabled: netInstallEnabled(appSettings), Consoles: []ConsoleCard{}}
	principal, ok := netPairer(r)
	if !ok {
		return data
	}
	keys := web.netUserConsoles(principal.Name)
	sort.SliceStable(keys, func(i, j int) bool { return strings.ToLower(keys[i].Name) < strings.ToLower(keys[j].Name) })
	chosen := r.URL.Query().Get("console")
	if _, found := web.netUserConsole(principal.Name, chosen); !found {
		chosen = ""
	}
	order := r.URL.Query().Get("sort")
	if !consoleSorts[order] {
		order = consoleSortName
	}
	switchDB, _ := web.state.get()
	var looks map[string]TitleItem
	var kept []netFile
	listed, ready := false, false
	for _, key := range keys {
		if chosen != "" && key.ID != chosen {
			continue
		}
		card := ConsoleCard{Id: key.ID, Name: key.Name, Waiting: web.netPushes.waiting(key.ID)}
		if principal.IsAdmin() && !isDemoMode() {
			card.Reports, card.LastReport = web.netConsoleReports(key.ID)
		}
		inventory, told := web.readNetInventory(key.ID)
		if told {
			card.Told, card.Titles = true, summarizeNetInventory(inventory).Titles
			card.Seen = i18n.Translatef(lang, "Seen %v", agoText(lang, inventory.Updated, now))
			if system := inventory.System; system != nil {
				card.System, card.Sigpatches = system, hasSigpatches(system)
				card.Sd, card.Nand = consoleSpace(system.SdFree, system.SdTotal), consoleSpace(system.NandFree, system.NandTotal)
			}
			if looks == nil {
				looks = web.netGameLooks(lang)
			}
			if !listed {
				listed = true
				kept, _, ready = web.netFilesReady(appSettings.NetInstall, netConsolesCountWait)
			}
			var games []netConsoleGame
			if ready {
				var files []netUpdateFile
				games, files = compareNetConsole(inventory, kept, switchDB)
				countConsoleCard(&card, games, files, inventory.System, looks)
			} else {
				// the games all the same, without what the library has of them
				data.Counting = true
				games, _ = compareNetConsole(inventory, nil, switchDB)
			}
			if key.ID == chosen {
				data.Installed = consoleInstalled(key, inventory, games, ready, web.netPushes.removals(key.ID), looks, order, lang, now)
			}
		}
		data.Consoles = append(data.Consoles, card)
	}
	return data
}

// HandleConsoles adds the page Consoles.
func (web *Web) HandleConsoles() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/consoles.html")
	web.router.HandleFunc(consolesPage, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		web.render(w, r, templates, web.consolesPage(r, web.requestLanguage(r), time.Now()))
	}).Methods(http.MethodGet)
}
