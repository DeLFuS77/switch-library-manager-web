package web

import (
	"time"

	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"path/filepath"
	"sort"
	"strings"
)

type Issue struct {
	File   string
	Reason string
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
}

func (web *Web) HandleIssues() {
	fsPatterns := []string {
		"resources/layout.html",
		"resources/partials/pagination.html",
		"resources/pages/issues.html",
	}

	web.HandleFiltered("/issues.html", func(filter *TitleItemFilter, lang string) any {
		all := web.getIssues()
		matching := all
		if filter.Keyword != "" {
			keyword := strings.ToLower(filter.Keyword)
			matching = []Issue{}
			for _, issue := range all {
				if strings.Contains(strings.ToLower(issue.File), keyword) || strings.Contains(strings.ToLower(translateIssue(lang, issue.Reason)), keyword) {
					matching = append(matching, issue)
				}
			}
		}
		p := pagination.Calculate(filter.Page, filter.PerPage, len(matching))
		return IssuesPageData {
			GlobalPageData: web.globalPageData("issues"),
			Issues: matching[p.Start:p.End],
			Total: len(all),
			Filter: filter,
			Pagination: p,
			LastVerified: web.verifications().lastRun(),
		}
	}, web.embedFS, fsPatterns...)
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

	for k, v := range localDB.Skipped {
		issues = append(issues, Issue{File: filepath.Join(k.BaseFolder, k.FileName), Reason: v.ReasonText})
	}

	// files a verification found damaged, while they did not change
	for path, reason := range web.verifications().damaged() {
		issues = append(issues, Issue{File: path, Reason: reason})
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
