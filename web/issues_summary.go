package web

import (
	"sort"
	"strings"
)

// the kinds of issues, for the summary at the top of the Issues page and its filter
const (
	ISSUE_DAMAGED     = "damaged"
	ISSUE_KEYS        = "keys"
	ISSUE_UNREADABLE  = "unreadable"
	ISSUE_ORPHAN      = "orphan"
	ISSUE_DUPLICATE   = "duplicate"
	ISSUE_OLD         = "old"
	ISSUE_FILENAME    = "filename"
	ISSUE_UNSUPPORTED = "unsupported"
)

// issueKinds are the kinds in the order of the summary: what needs doing first.
var issueKinds = []string{ISSUE_DAMAGED, ISSUE_KEYS, ISSUE_UNREADABLE, ISSUE_ORPHAN, ISSUE_DUPLICATE, ISSUE_OLD, ISSUE_FILENAME, ISSUE_UNSUPPORTED}

// issueKind tells the kind of an issue from its reason.
func issueKind(reason string) string {
	switch {
	case strings.HasPrefix(reason, "damaged file"):
		return ISSUE_DAMAGED
	case strings.Contains(reason, "prod.keys has no"):
		return ISSUE_KEYS
	case strings.HasPrefix(reason, "base file is missing"):
		return ISSUE_ORPHAN
	case strings.HasPrefix(reason, "duplicate"):
		return ISSUE_DUPLICATE
	case strings.HasPrefix(reason, "old "):
		return ISSUE_OLD
	case strings.HasPrefix(reason, "identified by file name only"):
		return ISSUE_FILENAME
	case strings.HasPrefix(reason, "file type is not supported"):
		return ISSUE_UNSUPPORTED
	}
	return ISSUE_UNREADABLE
}

// IssueSummary is a kind of issue: how many files, what it means and what to do.
type IssueSummary struct {
	Kind  string
	Icon  string
	Tone  string
	Title string
	Text  string
	Count int
	// where it is solved, besides the list of its files
	Href   string
	Action string
	// the games without their base game, the ones with the most files first
	Games      []TitleItem
	GamesCount int
}

// issueSummaries counts the issues by kind; the kinds without issues are left out.
func (web *Web) issueSummaries(issues []Issue, lang string) []IssueSummary {
	t := func(text string, args ...any) string { return translatef(lang, text, args...) }
	counts := map[string]int{}
	for _, issue := range issues {
		counts[issueKind(issue.Reason)]++
	}
	summaries := []IssueSummary{}
	for _, kind := range issueKinds {
		count := counts[kind]
		if count == 0 {
			continue
		}
		summary := IssueSummary{Kind: kind, Count: count}
		switch kind {
		case ISSUE_DAMAGED:
			summary.Icon, summary.Tone, summary.Title = "bi-heartbreak", "danger", t("Damaged files")
			summary.Text = t("Their data does not match what they should hold: they will not install or play well. Get them again.")
		case ISSUE_KEYS:
			summary.Icon, summary.Tone, summary.Title = "bi-key", "danger", t("Keys too old")
			summary.Text = t("They need keys of a newer firmware than your prod.keys. Dump the keys of your console again, updated.")
			summary.Href, summary.Action = "/settings.html#library", t("Keys")
		case ISSUE_UNREADABLE:
			summary.Icon, summary.Tone, summary.Title = "bi-exclamation-octagon", "danger", t("Files that cannot be read")
			summary.Text = t("They are not complete NSP, NSZ, XCI or XCZ files: a download cut short or a damaged copy.")
		case ISSUE_ORPHAN:
			summary.Icon, summary.Tone, summary.Title = "bi-question-diamond", "warning", t("Updates and DLC without their game")
			summary.Text = t("The library has them, but not the game they belong to. Look for the game by its ID, or delete them if you do not want it.")
			summary.Href, summary.Action = "/orphans.html", t("See them by game")
			orphans := web.derived("orphans:"+lang, func() any { return web.buildOrphans(lang) }).([]TitleItem)
			summary.GamesCount = len(orphans)
			games := append([]TitleItem(nil), orphans...)
			sort.SliceStable(games, func(i, j int) bool {
				a, b := games[i].OrphanUpdates+games[i].OrphanDlc, games[j].OrphanUpdates+games[j].OrphanDlc
				if a != b {
					return a > b
				}
				return games[i].Name < games[j].Name
			})
			summary.Games = games[:min(len(games), 6)]
		case ISSUE_DUPLICATE:
			summary.Icon, summary.Tone, summary.Title = "bi-files", "info", t("Duplicates")
			summary.Text = t("The same content more than once. The Space page frees them, keeping the copy that is checked.")
			summary.Href, summary.Action = "/space.html", t("Free them")
		case ISSUE_OLD:
			summary.Icon, summary.Tone, summary.Title = "bi-clock-history", "warning", t("Old updates and DLC")
			summary.Text = t("A newer version of them is in the library. The Space page frees them.")
			summary.Href, summary.Action = "/space.html", t("Free them")
		case ISSUE_FILENAME:
			summary.Icon, summary.Tone, summary.Title = "bi-tag", "info", t("Recognized by their name only")
			summary.Text = t("The app could not read them inside and trusts their file name. With your prod.keys they are read whole.")
		case ISSUE_UNSUPPORTED:
			summary.Icon, summary.Tone, summary.Title = "bi-file-earmark-x", "muted", t("Files that are not games")
			summary.Text = t("Images, notes, archives... in your folders. Ignore their types below to stop listing them.")
		}
		summaries = append(summaries, summary)
	}
	return summaries
}
