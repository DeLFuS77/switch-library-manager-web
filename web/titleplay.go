package web

import (
	"math"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
)

// TitlePlayed is how much the user played a game on their consoles, as they tell it, for its page:
// "34 h · 57 times · last played 3 days ago", on how many consoles, and, with the time of the story
// (IGDB), the share played and what is left ("34 of 48 h · about 14 h left").
type TitlePlayed struct {
	Text     string
	Consoles int
	Percent  int
	Story    string
}

// titlePlayed is how much a game was played, for its page; without a time of the story, no share.
func titlePlayed(lang string, stats netPlayStats, consoles int, beat TimeToBeat, now time.Time) *TitlePlayed {
	played := &TitlePlayed{Text: playText(lang, stats, now), Consoles: consoles}
	story := int64(beat.Normally / 60)
	if !beat.Found || story <= 0 || stats.Minutes <= 0 {
		return played
	}
	played.Percent = int(min(stats.Minutes*100/story, 100))
	if stats.Minutes >= story {
		played.Story = i18n.Translate(lang, "Played longer than the story")
		return played
	}
	hours := func(minutes int64) string {
		return i18n.FormatNumber(lang, int(math.Round(float64(minutes)/60)))
	}
	left := max(int64(math.Round(float64(story-stats.Minutes)/60)), 1)
	played.Story = i18n.Translatef(lang, "%v of %v h", hours(stats.Minutes), hours(story)) + " · " + i18n.Translatef(lang, "about %v h left", left)
	return played
}
