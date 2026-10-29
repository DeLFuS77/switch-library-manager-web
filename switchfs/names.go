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
