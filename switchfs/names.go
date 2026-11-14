package switchfs

import (
	"fmt"
	"strconv"
	"strings"
)

// The fields of a name that tell what a file holds, written the same by Switch Library
// Manager Web and by NXPress.

// PackageContents tells the titles of each kind a file holds: 1G+1U+2D for a game, its update
// and two DLC; the kinds it has none of are left out.
func PackageContents(games int, updates int, dlcs int) string {
	parts := []string{}
	for _, kind := range []struct {
		count  int
		letter string
	}{{games, "G"}, {updates, "U"}, {dlcs, "D"}} {
		if kind.count > 0 {
			parts = append(parts, strconv.Itoa(kind.count)+kind.letter)
		}
	}
	return strings.Join(parts, "+")
}

// SizeGB is a size in decimal GB with one decimal, rounded half up: 2.7GB; nothing for a size
// not known.
func SizeGB(size int64) string {
	if size <= 0 {
		return ""
	}
	tenths := (size + 50_000_000) / 100_000_000
	return fmt.Sprintf("%d.%dGB", tenths/10, tenths%10)
}

// SizeMB is a size in decimal MB, rounded half up: 2650MB; nothing for a size not known.
func SizeMB(size int64) string {
	if size <= 0 {
		return ""
	}
	return fmt.Sprintf("%dMB", (size+500_000)/1_000_000)
}

// GameTitleID is the title ID of the game a title belongs to, by its kind as
// ContentMetaAttributes gives it: an update is the game plus 0x800, a DLC the game plus 0x1000
// and its number.
func GameTitleID(titleID string, kind string) (uint64, error) {
	id, err := strconv.ParseUint(titleID, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid title ID %q", titleID)
	}
	if kind == "DLC" {
		id -= 0x1000
	}
	return id &^ 0xFFF, nil
}

// noIntroLanguages are the languages of games as No-Intro writes them, in its order, by the
// first part of their tags: the variants of a language (en-US, en-GB) are one.
var noIntroLanguages = []struct{ tag, code string }{
	{"en", "En"}, {"ja", "Ja"}, {"fr", "Fr"}, {"de", "De"}, {"es", "Es"}, {"it", "It"},
	{"nl", "Nl"}, {"pt", "Pt"}, {"ru", "Ru"}, {"zh", "Zh"}, {"ko", "Ko"},
}

// LanguageCodes are languages, as tags of the control data (en-US, zh-Hant) or of the database
// of titles (en, zh), written as No-Intro writes them: En,Ja,Fr,De; "" for none.
func LanguageCodes(tags []string) string {
	has := map[string]bool{}
	for _, tag := range tags {
		primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		has[primary] = true
	}
	codes := []string{}
	for _, language := range noIntroLanguages {
		if has[language.tag] {
			codes = append(codes, language.code)
		}
	}
	return strings.Join(codes, ",")
}

// LanguageTags are the languages a NACP tells its game comes in, tagged en-US, ja, zh-Hant.
func (n *Nacp) LanguageTags() []string {
	if n == nil {
		return nil
	}
	var tags []string
	for language := Language(0); language < 16; language++ {
		if n.SupportedLanguageFlag&(1<<uint(language)) != 0 {
			tags = append(tags, language.ToLanguageTag())
		}
	}
	return tags
}
