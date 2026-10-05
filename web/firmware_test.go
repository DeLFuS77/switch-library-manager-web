package web

import (
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestFirmwareVersions(t *testing.T) {
	if got := firmwareVersion(0x30000000); got != "12.0.0" {
		t.Fatalf("12.0.0: %q", got)
	}
	if got := firmwareVersion(18<<26 | 1<<20); got != "18.1.0" {
		t.Fatalf("18.1.0: %q", got)
	}
	if firmwareVersion(0) != "" {
		t.Fatal("no requirement")
	}
	for text, ok := range map[string]bool{"18.1.0": true, "9.2.0": true, " 17.0.1 ": true, "18": false, "18.1": false, "a.b.c": false, "99.0.0": false} {
		if code, valid := parseFirmware(text); valid != ok || (ok && firmwareVersion(code) == "") {
			t.Errorf("parseFirmware(%q) = %v, %v", text, code, valid)
		}
	}
}

func TestGamesNeedingNewerFirmwareAreMarked(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	game := localDB.TitlesMap["0100000000010"]
	update := game.Updates[65536]
	update.Metadata.RequiredTitleVersion = 18 << 26
	web.state.set(switchDB, localDB)

	original := settings.ReadSettings(web.dataFolder).ConsoleFirmware
	defer settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.ConsoleFirmware = original })

	detail, _ := web.getTitleDetail("0100000000010000", "en")
	if detail.RequiredFirmware != "18.0.0" || detail.FirmwareTooNew {
		t.Fatalf("without the console firmware nothing is too new: %+v", detail.RequiredFirmware)
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.ConsoleFirmware = "17.0.1" })
	if detail, _ := web.getTitleDetail("0100000000010000", "en"); !detail.FirmwareTooNew {
		t.Fatal("the game page must warn")
	}
	library, _ := web.getLibrary(defaultFilter(), "en")
	marked := false
	for _, item := range library {
		if item.Name == "Known Game" && item.FirmwareTooNew && item.RequiredFirmware == "18.0.0" {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("the card must warn: %+v", library)
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.ConsoleFirmware = "18.1.0" })
	if detail, _ := web.getTitleDetail("0100000000010000", "en"); detail.FirmwareTooNew {
		t.Fatal("a newer console is fine")
	}
}
