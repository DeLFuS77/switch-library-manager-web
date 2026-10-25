package db

import (
	"regexp"
	"strings"
)

// keptPath is the file a skipped file is a duplicate or an older version of, at the end of
// its reason.
var keptPath = regexp.MustCompile(`\((.*[/\\].*)\)$`)

// resolveKeptChains names in the reason of each duplicate and older version the file the
// library keeps. A file is compared with the one kept at that moment: when a better one comes
// later, the one kept before is skipped too, and the files compared with it named a file that
// is deleted with them, so a cleanup that deleted it first no longer found the copy that stays.
// A copy of an older version is an older version too.
func resolveKeptChains(skipped map[ExtendedFileInfo]SkippedFile) {
	byPath := map[string]SkippedFile{}
	for file, entry := range skipped {
		if entry.ReasonCode == REASON_DUPLICATE || entry.ReasonCode == REASON_OLD_UPDATE {
			byPath[fullPath(file)] = entry
		}
	}
	for file, entry := range skipped {
		if entry.ReasonCode != REASON_DUPLICATE && entry.ReasonCode != REASON_OLD_UPDATE {
			continue
		}
		match := keptPath.FindStringSubmatch(entry.ReasonText)
		if match == nil {
			continue
		}
		kept, older := match[1], ""
		seen := map[string]bool{fullPath(file): true}
		for {
			next, ok := byPath[kept]
			if !ok || seen[kept] {
				break
			}
			seen[kept] = true
			nextMatch := keptPath.FindStringSubmatch(next.ReasonText)
			if nextMatch == nil {
				break
			}
			if next.ReasonCode == REASON_OLD_UPDATE && older == "" {
				older = strings.TrimSuffix(next.ReasonText, "("+nextMatch[1]+")")
			}
			kept = nextMatch[1]
		}
		if kept == match[1] {
			continue
		}
		text := strings.TrimSuffix(entry.ReasonText, "("+match[1]+")")
		if entry.ReasonCode == REASON_DUPLICATE && older != "" {
			entry.ReasonCode, text = REASON_OLD_UPDATE, older
		}
		entry.ReasonText = text + "(" + kept + ")"
		skipped[file] = entry
	}
}
