package process

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// NameBytes is how many bytes a file name takes on disk, at most: ZFS datasets made with
// normalization=formD (Unraid makes them so) and the disks of macOS keep names decomposed, so
// a kana with its marks (ガ) or an accented letter takes more bytes than it is written with.
func NameBytes(name string) int {
	return max(len(name), len(norm.NFD.String(name)))
}

// identifiers is the end of a file name that tells the file: from the first group in brackets
// with a title ID ("[0100ABCD12345000]") to the end.
var identifiers = regexp.MustCompile(`\s*\[[0-9A-Fa-f]{16}\].*$`)

// FitFileName shortens a file name to budget bytes on disk (see NameBytes): the part before its
// title ID is cut on a character boundary, never the title ID, the version or the extension.
// A name without a title ID is cut before its extension. A bracket left open is closed.
func FitFileName(name string, budget int) string {
	if NameBytes(name) <= budget {
		return name
	}
	head, tail := name, ""
	if loc := identifiers.FindStringIndex(name); loc != nil && loc[0] > 0 {
		head, tail = name[:loc[0]], name[loc[0]:]
	} else if dot := strings.LastIndex(name, "."); dot > 0 {
		head, tail = name[:dot], name[dot:]
	}
	runes := []rune(head)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		shortened := strings.TrimSpace(string(runes))
		if strings.Count(shortened, "[") > strings.Count(shortened, "]") {
			shortened += "]"
		}
		if candidate := shortened + tail; NameBytes(candidate) <= budget {
			return candidate
		}
	}
	return name
}
