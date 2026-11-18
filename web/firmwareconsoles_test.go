package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A game needs a newer firmware than the consoles of the user that told theirs, each of them; with
// none, than the one written in Settings.
func TestFirmwareCheck(t *testing.T) {
	code := func(text string) int {
		value, _ := parseFirmware(text)
		return value
	}
	consoles := firmwareCheck{consoles: []netConsoleFirmware{{Name: "Living room", Code: code("19.0.1")}, {Name: "Bedroom", Code: code("18.1.0")}}, manual: code("21.0.0")}
	if !consoles.told() || consoles.tooNew(code("18.0.0")) || !consoles.tooNew(code("19.0.0")) || consoles.tooNew(0) {
		t.Error("too new for a console")
	}
	if got := strings.Join(consoles.short(code("19.0.0")), ","); got != "Bedroom" {
		t.Errorf("the console that falls short: %s", got)
	}
	if got := strings.Join(consoles.short(code("20.0.0")), ","); got != "Living room,Bedroom" {
		t.Errorf("both fall short: %s", got)
	}
	manual := firmwareCheck{manual: code("19.0.0")}
	if manual.told() || !manual.tooNew(code("20.0.0")) || manual.tooNew(code("19.0.0")) || len(manual.short(code("20.0.0"))) != 0 {
		t.Error("the one of Settings")
	}
	if (firmwareCheck{}).tooNew(code("20.0.0")) {
		t.Error("nothing known: never too new")
	}
}

// The Library marks the games too new for a console of the user who looks, as it tells its
// firmware, and the page of the game names the console; Settings shows the firmware of the
// consoles in place of the field.
func TestFirmwareOfTheConsoles(t *testing.T) {
	web, admin, bob, console, _, _ := consolesWeb(t)
	web.HandleTitle()
	switchDB, localDB := web.state.get()
	update := localDB.TitlesMap["0100000000010"].Updates[131072]
	update.Metadata.RequiredTitleVersion, _ = parseFirmware("20.0.0")
	localDB.TitlesMap["0100000000010"].Updates[131072] = update
	web.state.set(switchDB, localDB)

	library := func(user string) ([]TitleItem, LibraryFacets) {
		t.Helper()
		filter := &TitleItemFilter{PerPage: 96, SortBy: "name", SortOrder: "asc", Page: 1, Extra: EXTRA_FIRMWARE, player: user}
		filter.Normalize()
		items, _, facets := web.getLibraryWithFacets(filter, "en")
		return items, facets
	}
	items, facets := library("bob")
	if len(items) != 1 || !items[0].FirmwareTooNew || items[0].RequiredFirmware != "20.0.0" || facets.FirmwareTooNew != 1 || !facets.ConsoleFirmwares {
		t.Fatalf("too new for bob's console: %+v %+v", items, facets.FirmwareTooNew)
	}
	// Alice has no console: the firmware of Settings, none at first
	if items, facets := library("alice"); len(items) != 0 || facets.ConsoleFirmwares {
		t.Fatalf("nothing for alice: %+v", items)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.ConsoleFirmware = "19.0.0" })
	if items, _ := library("alice"); len(items) != 1 {
		t.Fatalf("the firmware of Settings for alice: %+v", items)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.ConsoleFirmware = "21.0.0" })
	if items, _ := library("bob"); len(items) != 1 {
		t.Fatal("bob's console counts, not Settings")
	}

	page := bob.do("GET", "/title/0100000000010000.html", nil).Body.String()
	if !strings.Contains(page, "Needs a newer firmware than Switch") {
		t.Error("the page of the game names the console")
	}
	if page := admin.do("GET", "/title/0100000000010000.html", nil).Body.String(); strings.Contains(page, "Needs a newer firmware than") || strings.Contains(page, "or newer for this game") {
		t.Error("alice's console of Settings is new enough")
	}

	// Settings: the field while alice's consoles tell nothing, their firmware once they do
	set, err := parseTemplates(os.DirFS(".."), "resources/layout.html", "resources/pages/settings.html", "resources/partials/notifications.html")
	if err != nil {
		t.Fatal(err)
	}
	settingsPage := func() string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/settings.html", nil)
		request = request.WithContext(context.WithValue(request.Context(), principalKey{}, &Principal{Name: "alice", Role: ROLE_ADMIN}))
		var out strings.Builder
		if err := set["en"].ExecuteTemplate(&out, "layout", web.settingsPageFor(request)); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if page := settingsPage(); !strings.Contains(page, `id="console_firmware"`) || strings.Contains(page, "Firmware of your consoles") {
		t.Error("the field to write it")
	}
	key := createKey(t, admin, "Lounge", ROLE_CONSOLE)
	told := `{"items":[],"console":{"firmware":"18.1.0"}}`
	if code := sendJSON(console, "POST", "/install/inventory", told, "Authorization", basic("alice", key.Key)).Code; code != http.StatusOK {
		t.Fatalf("told: %d", code)
	}
	page = settingsPage()
	if !strings.Contains(page, "Firmware of your consoles") || !strings.Contains(page, "<strong>18.1.0</strong>") || strings.Contains(page, `id="console_firmware"`) ||
		!strings.Contains(page, `<input type="hidden" name="console_firmware" value="21.0.0">`) || strings.Contains(page, "<strong>19.0.1</strong>") {
		t.Error("the firmware of alice's console, and the one of Settings kept")
	}
}
