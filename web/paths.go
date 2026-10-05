package web

import (
	"regexp"
	"strings"
)

// fileBase is the last element of a path written with / or \ (paths of other systems
// are shown too, e.g. in issues found by an older scan).
func fileBase(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// fileDir is the folder of a path written with / or \.
func fileDir(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[:i]
	}
	return ""
}

// a path in parentheses at the end of an issue, e.g. "duplicate base file (/games/x.nsp)"
var trailingPath = regexp.MustCompile(`\((.*[/\\].*)\)$`)

// shortPaths shows only the file name of the path at the end of an issue text.
func shortPaths(text string) string {
	return trailingPath.ReplaceAllStringFunc(text, func(match string) string {
		return "(" + fileBase(match[1:len(match)-1]) + ")"
	})
}
