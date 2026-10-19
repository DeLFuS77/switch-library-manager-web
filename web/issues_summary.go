package web

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// the kinds of issues, for the summary at the top of the Issues page and its filter
const (
	ISSUE_DAMAGED     = "damaged"
	ISSUE_TRUNCATED   = "truncated"
	ISSUE_KEYS        = "keys"
	ISSUE_UNREADABLE  = "unreadable"
	ISSUE_ORPHAN      = "orphan"
	ISSUE_DUPLICATE   = "duplicate"
	ISSUE_OLD         = "old"
	ISSUE_FILENAME    = "filename"
	ISSUE_UNSUPPORTED = "unsupported"
)

// issueKinds are the kinds in the order of the summary: what needs doing first.
var issueKinds = []string{ISSUE_DAMAGED, ISSUE_TRUNCATED, ISSUE_KEYS, ISSUE_UNREADABLE, ISSUE_ORPHAN, ISSUE_DUPLICATE, ISSUE_OLD, ISSUE_FILENAME, ISSUE_UNSUPPORTED}

// dirOf is the folder of a path.
func dirOf(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[:i]
	}
	return "."
}

// IssueKindLook is how a kind of issue shows: its icon, its tone (the color of its card) and
// its title, to translate.
type IssueKindLook struct {
	Icon  string
	Tone  string
	Title string
}

var issueKindLooks = map[string]IssueKindLook{
	ISSUE_DAMAGED:     {"bi-heartbreak", "danger", "Damaged files"},
	ISSUE_TRUNCATED:   {"bi-scissors", "danger", "Files cut short"},
	ISSUE_KEYS:        {"bi-key", "danger", "Keys too old"},
	ISSUE_UNREADABLE:  {"bi-exclamation-octagon", "danger", "Files that cannot be read"},
	ISSUE_ORPHAN:      {"bi-question-diamond", "warning", "Updates and DLC without their game"},
	ISSUE_DUPLICATE:   {"bi-files", "info", "Duplicates"},
	ISSUE_OLD:         {"bi-clock-history", "warning", "Old updates and DLC"},
	ISSUE_FILENAME:    {"bi-tag", "info", "Recognized by their name only"},
	ISSUE_UNSUPPORTED: {"bi-file-earmark-x", "muted", "Files that are not games"},
}

// IssueKindCount is a kind of issue and how many files it has, for the header.
type IssueKindCount struct {
	IssueKindLook
	Kind  string
	Count int
}

// issueKindCounts counts the issues by kind, in the order of the summary.
func (web *Web) issueKindCounts() []IssueKindCount {
	return web.derived("issueKindCounts", func() any {
		counts := map[string]int{}
		for _, issue := range web.getIssues() {
			counts[issueKind(issue.Reason)]++
		}
		result := []IssueKindCount{}
		for _, kind := range issueKinds {
			if counts[kind] > 0 {
				result = append(result, IssueKindCount{IssueKindLook: issueKindLooks[kind], Kind: kind, Count: counts[kind]})
			}
		}
		return result
	}).([]IssueKindCount)
}

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
	case isCutShort(reason):
		return ISSUE_TRUNCATED
	}
	return ISSUE_UNREADABLE
}

// isCutShort tells a file that could not be read because it ends before its contents do: the
// reader met the end of the file (EOF) while the headers still asked for more. A passing error
// of the disk or of the network is an error of reading, never an end of file.
func isCutShort(reason string) bool {
	return strings.Contains(reason, "EOF")
}

// issueCause says in plain words why a file could not be read, from the error of its reader;
// empty when the error itself is the clearest thing to show.
func issueCause(reason, lang string) string {
	switch kind := issueKind(reason); {
	case kind == ISSUE_TRUNCATED:
		return translatef(lang, "Cut short: the file ends before its contents do.")
	case kind == ISSUE_UNREADABLE && strings.Contains(reason, "invalid NCA magic"):
		return translatef(lang, "Its contents cannot be decrypted: a damaged copy, or encrypted with keys other than yours.")
	}
	return ""
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
	// its files are of no use and can be deleted from the summary, with their size
	Deletable bool
	Size      int64
}

// deletableIssueKinds are the kinds whose files are of no use as they are and can go at
// once: the damaged ones, found so by a check of their contents while they did not change, and
// the ones cut short, which end before their contents do. The other files that could not be read
// are not: missing keys or a format the app does not know also leave a file unread.
var deletableIssueKinds = map[string]bool{ISSUE_DAMAGED: true, ISSUE_TRUNCATED: true}

// issueFiles lists the files of the issues of a kind as they are now on the disk; files
// gone or no longer files are left out.
func issueFiles(issues []Issue, kind string) []db.ExtendedFileInfo {
	files := []db.ExtendedFileInfo{}
	seen := map[string]bool{}
	for _, issue := range issues {
		if issueKind(issue.Reason) != kind || seen[issue.File] {
			continue
		}
		seen[issue.File] = true
		info, err := os.Stat(issue.File)
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, db.ExtendedFileInfo{FileName: info.Name(), BaseFolder: dirOf(issue.File), Size: info.Size()})
	}
	return files
}

// settledFiles leaves out the files changed in the last while: a file still being downloaded or
// copied is cut short too, until it is finished.
func settledFiles(files []db.ExtendedFileInfo, quiet time.Duration) []db.ExtendedFileInfo {
	settled := []db.ExtendedFileInfo{}
	for _, file := range files {
		info, err := os.Stat(filepath.Join(file.BaseFolder, file.FileName))
		if err == nil && time.Since(info.ModTime()) >= quiet {
			settled = append(settled, file)
		}
	}
	return settled
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
		look := issueKindLooks[kind]
		summary := IssueSummary{Kind: kind, Count: count, Icon: look.Icon, Tone: look.Tone, Title: t(look.Title)}
		if deletableIssueKinds[kind] {
			summary.Deletable = true
			summary.Size = web.derived("issueSize:"+kind, func() any {
				size := int64(0)
				for _, file := range issueFiles(issues, kind) {
					size += file.Size
				}
				return size
			}).(int64)
		}
		switch kind {
		case ISSUE_DAMAGED:
			summary.Text = t("Their data does not match what they should hold: they will not install or play well. Get them again.")
		case ISSUE_KEYS:
			summary.Text = t("They need keys of a newer firmware than your prod.keys. Dump the keys of your console again, updated.")
			summary.Href, summary.Action = "/settings.html#library", t("Keys")
		case ISSUE_TRUNCATED:
			summary.Text = t("They end before their contents do: a download or a copy that did not finish. They are of no use: delete them and get them again.")
		case ISSUE_UNREADABLE:
			summary.Text = t("Their contents cannot be read: a damaged copy, a format the app does not know, or keys other than your prod.keys. Check them on your computer before deleting them.")
		case ISSUE_ORPHAN:
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
			summary.Games = games[:min(len(games), 3)]
		case ISSUE_DUPLICATE:
			summary.Text = t("The same content more than once. The Space page frees them, keeping the copy that is checked.")
			summary.Href, summary.Action = "/space.html", t("Free them")
		case ISSUE_OLD:
			summary.Text = t("A newer version of them is in the library. The Space page frees them.")
			summary.Href, summary.Action = "/space.html", t("Free them")
		case ISSUE_FILENAME:
			summary.Text = t("The app could not read them inside and trusts their file name. With your prod.keys they are read whole.")
		case ISSUE_UNSUPPORTED:
			summary.Text = t("Images, notes, archives... in your folders. Ignore their types below to stop listing them.")
		}
		summaries = append(summaries, summary)
	}
	return summaries
}
