package web

import (
	"regexp"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/internal/search"
)

// The eShop writes the names of its games with marks and words IGDB does not have in its own
// (LEGO® Voyagers, WWE 2K25 Standard Edition, EARTH DEFENSE FORCE5 for Nintendo Switch™ 2,
// Yooka-Replaylee DEMO), so a game is searched on IGDB by its name without them, then, while it
// is not found, by shorter forms of it.

var (
	// the marks, and the typographic quotes and dashes, which IGDB writes plain
	igdbMarks = strings.NewReplacer("™", "", "®", "", "©", "", "’", "'", "‘", "'", "“", `"`, "”", `"`, "–", "-", "—", "-")
	// the console, in the names of the games made for the Switch 2
	igdbConsoleWords = regexp.MustCompile(`(?i)\s*\bfor nintendo switch\s*2\b`)
	// the standard edition, the one IGDB names without it
	igdbStandardEdition = regexp.MustCompile(`(?i)\s*[-:]?\s*\bstandard edition\b`)
	// a demo, a trial or a pass to play with a friend, and all that follows: the game is the same
	igdbTrial = regexp.MustCompile(`(?i)\s*[-:(]?\s*\b(?:free trial|trial version|demo version|demo|(?:global )?friend's pass)\b.*$`)
	// a DLC sold with the game: KINGDOM HEARTS III + Re Mind (DLC)
	igdbDlc = regexp.MustCompile(`(?i)\s*\(dlc\)`)
	// a dash stuck to a word on one side only, a decoration: KINGDOM HEARTS -HD 1.5+2.5 ReMIX-
	igdbLeadingDash  = regexp.MustCompile(`(^|\s)-(\S)`)
	igdbTrailingDash = regexp.MustCompile(`(\S)-($|\s)`)
	// an edition at the end, with the word before it: Encore Edition, Special Edition
	igdbEdition = regexp.MustCompile(`(?i)\s*[-:]?\s*(?:[^\s:-]+\s+)?edition$`)
	// a number stuck to the word before it: FORCE5, Archives2 (not 2K25)
	igdbStuckNumber = regexp.MustCompile(`(\pL{3,})(\d+)\b`)
	igdbSpaces      = regexp.MustCompile(`\s+`)
)

// igdbSearchNames are the names a game is searched by on IGDB, in turn while it is not found:
// its name without the marks and the words IGDB does not have, the same with a number stuck to a
// word set apart, without its edition, and the part before its subtitle.
func igdbSearchNames(name string) []string {
	clean := igdbMarks.Replace(name)
	for _, words := range []*regexp.Regexp{igdbConsoleWords, igdbStandardEdition, igdbTrial, igdbDlc} {
		clean = words.ReplaceAllString(clean, "")
	}
	clean = igdbTrailingDash.ReplaceAllString(igdbLeadingDash.ReplaceAllString(clean, "$1$2"), "$1$2")
	tidy := func(text string) string {
		return strings.Trim(igdbSpaces.ReplaceAllString(text, " "), " -:")
	}
	clean = tidy(clean)
	head := clean
	for _, separator := range []string{": ", " - ", " + "} {
		if at := strings.Index(head, separator); at > 0 {
			head = head[:at]
		}
	}
	var names []string
	seen := map[string]bool{}
	for _, candidate := range []string{clean, igdbStuckNumber.ReplaceAllString(clean, "$1 $2"), tidy(igdbEdition.ReplaceAllString(clean, "")), tidy(head)} {
		key := search.Text(candidate)
		if len(key) < 3 || seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, candidate)
	}
	if len(names) == 0 {
		// nothing left of it to search by (a name of marks alone): the name as it is
		names = append(names, name)
	}
	return names
}

// igdbPick is the game of an answer of IGDB to a search: the one of the same name, else, for the
// name of the game itself (first), the best match of IGDB; for a shorter form of the name, one
// whose name starts with it, as a short name finds other games too. -1 when none fits.
func igdbPick(names []string, wanted string, first bool) int {
	want := search.Text(wanted)
	for i, name := range names {
		if search.Text(name) == want {
			return i
		}
	}
	if first {
		if len(names) > 0 {
			return 0
		}
		return -1
	}
	for i, name := range names {
		if strings.HasPrefix(search.Text(name), want+" ") {
			return i
		}
	}
	return -1
}
