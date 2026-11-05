package web

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

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
	// a plus between two words, which IGDB writes between spaces: 1.5+2.5
	igdbPlus = regexp.MustCompile(`(\S)\+(\S)`)
	// a title stuck to the name after it: Mr.Do
	igdbTitlePeriod = regexp.MustCompile(`\b(Mr|Mrs|Ms|Dr|St|Jr)\.(\S)`)
	// the brand in front of the name, which IGDB leaves out: EA SPORTS Madden NFL 27
	igdbBrand  = regexp.MustCompile(`(?i)^EA SPORTS\s+`)
	igdbSpaces = regexp.MustCompile(`\s+`)
)

// igdbSearchNames are the names a game is searched by on IGDB, in turn while it is not found:
// its name without the marks and the words IGDB does not have, the same with the spaces IGDB
// writes (1.5 + 2.5, Mr. Do), with a number stuck to a word set apart, without the brand in front
// (EA SPORTS), without its edition, and the part before its subtitle.
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
	spaced := igdbTitlePeriod.ReplaceAllString(igdbPlus.ReplaceAllString(clean, "$1 + $2"), "$1. $2")
	for _, candidate := range []string{clean, spaced, igdbStuckNumber.ReplaceAllString(clean, "$1 $2"), igdbBrand.ReplaceAllString(clean, ""),
		tidy(igdbEdition.ReplaceAllString(clean, "")), tidy(head)} {
		// the same text once; "1.5+2.5" and "1.5 + 2.5" are different searches for IGDB
		key := strings.ToLower(candidate)
		if len(search.Text(candidate)) < 3 || seen[key] {
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

// igdbNamed is a game of an answer of IGDB, with its name.
type igdbNamed interface {
	igdbName() string
}

// igdbFind finds a game on IGDB, with the fields asked: first the game Wikidata links it to (the
// slug of the list of the Switch 2), when that game is of its console or of the Switch and has
// its name (see igdbNameFits), then by its names on its console (see igdbSearchNames), and, for
// a game of the Switch 2 IGDB lists on the Switch only, by the same name there.
func igdbFind[T igdbNamed](ctx context.Context, c *igdbClient, fields string, name string, slug string, platform int) (T, bool, error) {
	var none T
	if slug = strings.TrimSpace(slug); slug != "" {
		var games []T
		query := fmt.Sprintf(`fields %s; where slug = "%s" & platforms = (%d,%d); limit 1;`, fields, apicalypseText(slug), platform, igdbSwitchPlatform)
		if err := c.query(ctx, "games", query, &games); err != nil {
			return none, false, err
		}
		// a link of Wikidata made by an alias another game shares is not taken: the game is
		// searched by its name instead
		if len(games) > 0 && igdbNameFits(games[0].igdbName(), name) {
			return games[0], true, nil
		}
	}
	consoles := []int{platform}
	if platform != igdbSwitchPlatform {
		consoles = append(consoles, igdbSwitchPlatform)
	}
	names := igdbSearchNames(name)
	for n, console := range consoles {
		for i, searched := range names {
			var games []T
			query := fmt.Sprintf(`search "%s"; fields %s; where platforms = (%d); limit 5;`, apicalypseText(searched), fields, console)
			if err := c.query(ctx, "games", query, &games); err != nil {
				return none, false, err
			}
			found := make([]string, len(games))
			for j, game := range games {
				found[j] = game.igdbName()
			}
			// on the Switch, for a game of the Switch 2, only the same name
			pick := igdbSameName(found, searched)
			if pick < 0 && n == 0 {
				pick = igdbPick(found, searched, i == 0)
			}
			if pick >= 0 {
				return games[pick], true, nil
			}
		}
	}
	return none, false, nil
}

// igdbNameFits tells whether a game of IGDB has the name of a title: the same, or one that starts
// with it or that it starts with (an edition, a subtitle), in any of the names the title is
// searched by. A name not in the Latin alphabet (Japanese) cannot be compared, and fits.
func igdbNameFits(found string, name string) bool {
	for _, r := range name {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	have := search.Text(found)
	for _, searched := range igdbSearchNames(name) {
		want := search.Text(searched)
		if want != "" && (want == have || strings.HasPrefix(have, want+" ") || strings.HasPrefix(want, have+" ")) {
			return true
		}
	}
	return false
}

// igdbSameName is the game of the same name, -1 for none.
func igdbSameName(names []string, wanted string) int {
	want := search.Text(wanted)
	for i, name := range names {
		if search.Text(name) == want {
			return i
		}
	}
	return -1
}

// igdbPick is the game of an answer of IGDB to a search: the one of the same name, else, for the
// name of the game itself (first), the best match of IGDB; for a shorter form of the name, one
// whose name starts with it, as a short name finds other games too. -1 when none fits.
func igdbPick(names []string, wanted string, first bool) int {
	if same := igdbSameName(names, wanted); same >= 0 {
		return same
	}
	want := search.Text(wanted)
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
