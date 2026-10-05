package web

import (
	"path/filepath"
	"sort"
)

type Issue struct {
	File   string
	Reason string
}

type IssuesPageData struct {
	GlobalPageData
	Issues []Issue
}

func (web *Web) HandleIssues() {
	fsPatterns := []string {
		"resources/layout.html",
		"resources/pages/issues.html",
	}

	web.Handle("/issues.html", func() any {
		return IssuesPageData {
			GlobalPageData: web.globalPageData("issues"),
			Issues: web.getIssues(),
		}
	}, web.embedFS, fsPatterns...)
}

func (web *Web) getIssues() []Issue {
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

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].File == issues[j].File {
			return issues[i].Reason < issues[j].Reason
		}
		return issues[i].File < issues[j].File
	})

	return issues
}
