package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

type Issue struct {
	File   string
	Reason string
	// on the shown page only: the size of the file (-1 when it cannot be known; an empty file
	// shows 0 B), and why it could not be read in plain words
	Size  int64
	Cause string
	// the file also holds content the library keeps (the game, its latest update, a DLC), as a
	// file with several titles does: it is not deleted as old or duplicate
	Kept bool
	// on the shown page only: the games the file holds, when it holds several (a collection on
	// one cartridge), and whether the copy can be chosen to be kept (a duplicate)
	GamesInFile []string
	CanKeep     bool
}

type IssuesPageData struct {
	GlobalPageData
	// the shown page of the issues that match the search
	Issues     []Issue
	Total      int
	Filter     *TitleItemFilter
	Pagination pagination.Pagination
	// when the files were last checked for damage
	LastVerified time.Time
	// the types of the files that are not games and fill the list, most files first
	UnsupportedTypes []NamedCount
	// games of which the library has updates or DLC but not the game
	Orphans int
	// the issues by kind, and the kind the list shows
	Summaries []IssueSummary
	Kind      string
}

// unsupportedTypes counts the files of the issues that are of a type the app does not read,
// by extension, the most frequent first, at most limit.
func unsupportedTypes(issues []Issue, limit int) []NamedCount {
	counts := map[string]int{}
	for _, issue := range issues {
		if issue.Reason == "file type is not supported" {
			if extension := strings.ToLower(filepath.Ext(issue.File)); extension != "" {
				counts[extension]++
			}
		}
	}
	return topCounts(counts, limit)
}

func (web *Web) HandleIssues() {
	fsPatterns := []string{
		"resources/layout.html",
		"resources/partials/pagination.html",
		"resources/pages/issues.html",
	}

	web.HandleFiltered("/issues.html", func(filter *TitleItemFilter, lang string) any {
		all := web.getIssues()
		matching := all
		kind := ""
		if filter.IssueKind != "" {
			for _, known := range issueKinds {
				if known == filter.IssueKind {
					kind = known
				}
			}
			filter.IssueKind = kind
		}
		if kind != "" {
			matching = []Issue{}
			for _, issue := range all {
				if issueKind(issue.Reason) == kind {
					matching = append(matching, issue)
				}
			}
		}
		if filter.Keyword != "" {
			byKind := matching
			keyword := strings.ToLower(filter.Keyword)
			matching = []Issue{}
			for _, issue := range byKind {
				if strings.Contains(strings.ToLower(issue.File), keyword) || strings.Contains(strings.ToLower(translateIssue(lang, issue.Reason)), keyword) {
					matching = append(matching, issue)
				}
			}
		}
		p := pagination.Calculate(filter.Page, filter.PerPage, len(matching))
		shown := append([]Issue(nil), matching[p.Start:p.End]...)
		gamesInFiles := web.gamesInFiles()
		for i := range shown {
			shown[i].Size = -1
			if info, err := os.Stat(shown[i].File); err == nil && !info.IsDir() {
				shown[i].Size = info.Size()
			}
			shown[i].Cause = issueCause(shown[i].Reason, lang)
			shown[i].GamesInFile = gamesInFiles[shown[i].File]
			shown[i].CanKeep = issueKind(shown[i].Reason) == ISSUE_DUPLICATE
		}
		return IssuesPageData{
			GlobalPageData:   web.globalPageData("issues"),
			Issues:           shown,
			Total:            len(all),
			Filter:           filter,
			Pagination:       p,
			LastVerified:     web.verifications().lastRun(),
			Orphans:          web.orphanCount(),
			Summaries:        web.issueSummaries(all, lang),
			Kind:             kind,
			UnsupportedTypes: web.derived("unsupportedTypes", func() any { return unsupportedTypes(all, 5) }).([]NamedCount),
		}
	}, web.embedFS, fsPatterns...)

	// delete the files of a kind of issue that are of no use (damaged, cut short), then scan
	// the library again
	web.router.HandleFunc("/issues/delete", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		kind := r.FormValue("kind")
		if !deletableIssueKinds[kind] {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		files := issueFiles(web.getIssues(), kind)
		if kind == ISSUE_TRUNCATED {
			files = settledFiles(files, 10*time.Minute)
		}
		reasons := make([]string, len(files))
		for i := range reasons {
			reasons[i] = "deleted from Issues: " + kind
		}
		deleted, freed, err := web.removeFilesAsTask(files, reasons)
		if err != nil {
			writeGlobalError(w, http.StatusConflict, lang, err.Error())
			return
		}
		// the issues are listed again at once, without the files deleted
		web.invalidateDerived()
		web.Rescan(TRIGGER_MANUAL)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "freed": freed, "failed": len(files) - deleted})
	}).Methods("POST")

	// keep this copy of a duplicate: the other one becomes the duplicate, and the library is
	// scanned again
	web.router.HandleFunc("/issues/keep", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		requested := r.FormValue("path")
		// the path of the issue the library lists, never the one of the request
		path, other := "", ""
		for _, issue := range web.getIssues() {
			if requested != "" && issue.File == requested && issueKind(issue.Reason) == ISSUE_DUPLICATE {
				path, other = issue.File, keptBy(issue.Reason)
			}
		}
		if path == "" || other == "" {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid request")
			return
		}
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			s.OrganizeOptions.KeepFiles = keptChoices(s.OrganizeOptions.KeepFiles, path, other)
		})
		web.Rescan(TRIGGER_SETTINGS)
		writeJSON(w, http.StatusAccepted, map[string]any{"kept": path})
	}).Methods("POST")

	// ignore a type of file: its files are no longer reported, and the library is scanned again
	web.router.HandleFunc("/issues/ignore-type", func(w http.ResponseWriter, r *http.Request) {
		extension := strings.ToLower(strings.TrimSpace(r.FormValue("type")))
		if !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		if len(extension) < 2 || len(extension) > 12 || strings.ContainsAny(extension[1:], `./\ `) || db.IsGameFileType(extension) {
			writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), "Invalid request")
			return
		}
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			for _, known := range s.IgnoreFileTypes {
				if strings.EqualFold(strings.TrimPrefix(known, "."), extension[1:]) {
					return
				}
			}
			s.IgnoreFileTypes = append(s.IgnoreFileTypes, extension[1:])
		})
		web.Rescan(TRIGGER_SETTINGS)
		writeJSON(w, http.StatusAccepted, map[string]any{"ignored": extension})
	}).Methods("POST")
}

// keptChoices are the copies chosen to be kept with path added: the other copy of the same
// duplicate is no longer chosen, and the files that are gone are left out.
func keptChoices(choices []string, path string, other string) []string {
	result := []string{}
	for _, choice := range choices {
		if choice == path || filepath.Clean(choice) == filepath.Clean(other) {
			continue
		}
		if _, err := os.Stat(choice); err == nil {
			result = append(result, choice)
		}
	}
	return append(result, path)
}

// gamesInFiles are the names of the games of each file that holds several games, by path,
// sorted: a collection on one cartridge, or a pack of several games.
func (web *Web) gamesInFiles() map[string][]string {
	return web.derived("gamesInFiles", func() any {
		switchDB, localDB := web.state.get()
		games := map[string]map[string]bool{}
		if localDB == nil {
			return map[string][]string{}
		}
		for _, local := range localDB.TitlesMap {
			files := []db.ExtendedFileInfo{}
			if local.BaseExist {
				files = append(files, local.File.ExtendedInfo)
			}
			for _, update := range local.Updates {
				files = append(files, update.ExtendedInfo)
			}
			for _, dlc := range local.Dlc {
				files = append(files, dlc.ExtendedInfo)
			}
			for _, file := range files {
				path := filepath.Join(file.BaseFolder, file.FileName)
				name := web.gameName(switchDB, local, "")
				if name == "" {
					name = local.File.ExtendedInfo.FileName
				}
				if name == "" {
					continue
				}
				if games[path] == nil {
					games[path] = map[string]bool{}
				}
				games[path][name] = true
			}
		}
		result := map[string][]string{}
		for path, names := range games {
			if len(names) > 1 {
				result[path] = sortedKeysOf(names)
			}
		}
		return result
	}).(map[string][]string)
}

func sortedKeysOf(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// getIssues lists the problems of the library, sorted by file. The result is shared, it
// must not be modified.
func (web *Web) getIssues() []Issue {
	return web.derived("issues", func() any { return web.buildIssues() }).([]Issue)
}

func (web *Web) buildIssues() []Issue {
	issues := []Issue{}
	_, localDB := web.state.get()

	if localDB == nil {
		return issues
	}

	for _, v := range localDB.TitlesMap {
		if !v.BaseExist {
			for _, update := range v.Updates {
				issues = append(issues, Issue{File: filepath.Join(update.ExtendedInfo.BaseFolder, update.ExtendedInfo.FileName), Reason: "base file is missing"})
			}

			for _, dlc := range v.Dlc {
				issues = append(issues, Issue{File: filepath.Join(dlc.ExtendedInfo.BaseFolder, dlc.ExtendedInfo.FileName), Reason: "base file is missing"})
			}
		}
	}

	// an old update in a file that also holds content the library keeps (the game, its
	// latest update, a DLC) is that content, not an issue
	inUse := process.FilesInUse(localDB)
	for k, v := range localDB.Skipped {
		path := filepath.Join(k.BaseFolder, k.FileName)
		if v.ReasonCode == db.REASON_OLD_UPDATE && inUse[path] {
			continue
		}
		issues = append(issues, Issue{File: path, Reason: v.ReasonText, Kept: inUse[path]})
	}

	// files a verification found damaged, while they did not change
	// only those of the library folders: a folder taken out of the settings is no longer
	// the library, and its files are not deleted from Issues
	scanned := web.scannedPaths()
	for path, reason := range web.verifications().damaged() {
		if scanned[filepath.Clean(path)] {
			issues = append(issues, Issue{File: path, Reason: reason})
		}
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].File == issues[j].File {
			return issues[i].Reason < issues[j].Reason
		}
		return issues[i].File < issues[j].File
	})

	return issues
}

// issueIcon returns the icon and color of an issue, by the kind of problem.
func issueIcon(reason string) string {
	switch {
	case strings.HasPrefix(reason, "duplicate"):
		return "bi-files text-info"
	case strings.HasPrefix(reason, "old "):
		return "bi-clock-history text-warning"
	case strings.HasPrefix(reason, "base file is missing"):
		return "bi-question-diamond text-warning"
	case strings.HasPrefix(reason, "file type is not supported"):
		return "bi-file-earmark-x text-secondary"
	case strings.HasPrefix(reason, "identified by file name only"):
		return "bi-tag text-info"
	case strings.HasPrefix(reason, "damaged file"):
		return "bi-heartbreak text-danger"
	default:
		return "bi-exclamation-octagon text-danger"
	}
}
