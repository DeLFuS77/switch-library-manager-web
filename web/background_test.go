package web

import (
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestBackgroundHours(t *testing.T) {
	at := func(hour int) time.Time { return time.Date(2026, 10, 6, hour, 30, 0, 0, time.Local) }
	for hours, open := range map[string][]int{
		"":     {0, 3, 12, 23},
		"1-7":  {1, 3, 6},
		"22-6": {22, 23, 0, 5},
	} {
		s := &settings.AppSettings{BackgroundHours: hours}
		for _, hour := range open {
			if !inBackgroundHours(s, at(hour)) {
				t.Errorf("%q: %d:30 is inside", hours, hour)
			}
		}
	}
	for hours, closed := range map[string][]int{"1-7": {0, 7, 12, 23}, "22-6": {6, 12, 21}} {
		s := &settings.AppSettings{BackgroundHours: hours}
		for _, hour := range closed {
			if inBackgroundHours(s, at(hour)) {
				t.Errorf("%q: %d:30 is outside", hours, hour)
			}
		}
	}
}

func TestCoverWorkWaitsForTheBackgroundHours(t *testing.T) {
	web := newTestWeb(t)
	// hours that are never now
	hour := time.Now().Hour()
	closed := map[bool]string{true: "0-6", false: "22-6"}[hour >= 6 && hour < 22]
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.BackgroundHours = closed })
	t.Cleanup(func() {
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.BackgroundHours = "" })
	})
	if inBackgroundHours(settings.ReadSettings(web.dataFolder), time.Now()) {
		t.Skip("the test hours are open now")
	}
	web.state.set(largeDatabases(10, 20))
	web.downloadMissingCovers()
	if !web.background.covers {
		t.Fatal("the cover work waits for the background hours")
	}
	// asked by hand, it runs anyway
	web.background.forced = true
	if !web.backgroundAllowed() {
		t.Fatal("a forced run ignores the hours")
	}
}
