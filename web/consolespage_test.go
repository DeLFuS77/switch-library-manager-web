package web

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// consolesWeb is pushWeb with the page Consoles, and what bob's console has installed told.
func consolesWeb(t *testing.T) (web *Web, admin *client, bob *client, console *client, auth string, keyId string) {
	t.Helper()
	web, admin, bob, console, auth, keyId, _ = pushWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleConsoles()
	if code := sendJSON(console, "POST", "/install/inventory", consoleInventory, "Authorization", auth).Code; code != http.StatusOK {
		t.Fatalf("stored: %d", code)
	}
	return web, admin, bob, console, auth, keyId
}

// The page Consoles shows a card for each console of the user: its system, its free space and what
// the library has newer for it, with Bring up to date; it is in the menu of the users with a
// console, and shows no path.
func TestConsolesPage(t *testing.T) {
	web, admin, bob, _, _, keyId := consolesWeb(t)
	createKey(t, bob, "Switch Lite", ROLE_CONSOLE)
	page := bob.do("GET", "/consoles.html", nil)
	body := page.Body.String()
	if page.Code != http.StatusOK {
		t.Fatalf("the page: %d", page.Code)
	}
	for _, want := range []string{`href="/consoles.html"`, "emuMMC", "Sigpatches", "19.0.1", "Atmosphère 1.8.0", "Seen just now", `data-console-update="` + keyId + `"`,
		"game out of date", "1 update and 1 DLC", "they fit", `href="/consoles.html?console=` + keyId + `"`, "v65536 → v131072", "1 DLC missing",
		"Switch Lite", "has not told what is installed on it yet", `aria-valuetext="976.6 KB free"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page has no %s", want)
		}
	}
	if strings.Contains(body, web.dataFolder) || strings.Contains(body, ".nsp") {
		t.Error("the page shows a path or a file")
	}
	// Alice has no console: no link, and nothing of bob's
	other := admin.do("GET", "/consoles.html", nil).Body.String()
	if strings.Contains(other, `href="/consoles.html"`) || !strings.Contains(other, "No console yet") || strings.Contains(other, "emuMMC") {
		t.Error("another user sees none of them")
	}

	// the network install off: told, without Bring up to date
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	off := bob.do("GET", "/consoles.html", nil).Body.String()
	if strings.Contains(off, "data-console-update") || !strings.Contains(off, "Install over the network is off") {
		t.Error("turned off")
	}
}

// What is installed on a console: each game with its version, where it is, its DLC and how much it
// was played, in the order chosen, with Remove from console.
func TestConsolesPageInstalled(t *testing.T) {
	_, _, bob, _, _, keyId := consolesWeb(t)
	page := bob.do("GET", "/consoles.html?console="+keyId, nil)
	body := page.Body.String()
	if page.Code != http.StatusOK {
		t.Fatalf("the page: %d", page.Code)
	}
	for _, want := range []string{"Installed on Switch", `data-console-list="` + keyId + `"`, `data-console-game="0100000000010000"`, `href="/title/0100000000010000.html">Game</a>`,
		"Out of date", "34 h · 57 times", "Internal memory", "Remove from console", "Played on this console", "35 h", `id="consoleRemove"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page has no %s", want)
		}
	}
	if strings.Contains(body, "See what is installed") || strings.Contains(body, "0 DLC") {
		t.Error("the list is shown already, without the DLC it has none of")
	}
	order := func(sort string) []string {
		body := bob.do("GET", "/consoles.html?console="+keyId+"&sort="+sort, nil).Body.String()
		return []string{body[strings.Index(body, `data-console-game="`):][19:35], body[strings.LastIndex(body, `data-console-game="`):][19:35]}
	}
	if got := order("name"); got[0] != "0100000000010000" {
		t.Errorf("by name, the games without a name last: %v", got)
	}
	if got := order("played"); got[0] != "0100000000010000" {
		t.Errorf("the most played first: %v", got)
	}
	if got := order("recent"); got[0] != "0100000000010000" {
		t.Errorf("the played lately first: %v", got)
	}
	if got := order("nonsense"); got[0] != "0100000000010000" {
		t.Errorf("an order that is not one: %v", got)
	}
	// a console of another user is not shown
	if body := bob.do("GET", "/consoles.html?console=0000000000000000", nil).Body.String(); strings.Contains(body, "data-console-list") {
		t.Error("no such console")
	}
}

// How long ago, how long played and the files to send, as the pages tell them.
func TestConsoleTexts(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for since, want := range map[time.Duration]string{time.Minute: "just now", 5 * time.Minute: "5 min ago", 3 * time.Hour: "3 h ago", 30 * time.Hour: "yesterday",
		3 * 24 * time.Hour: "3 days ago"} {
		if got := agoText("en", now.Add(-since), now); got != want {
			t.Errorf("%v: %q, want %q", since, got, want)
		}
	}
	if got := agoText("es", now.AddDate(0, -2, 0), now); !strings.HasPrefix(got, "el 18 sep") {
		t.Errorf("long ago: %q", got)
	}
	for minutes, want := range map[int64]string{0: "0 min", 45: "45 min", 90: "2 h", 2040: "34 h", 120000: "2,000 h"} {
		if got := playedTime("en", minutes); got != want {
			t.Errorf("%d minutes: %q, want %q", minutes, got, want)
		}
	}
	for files, want := range map[[2]int]string{{1, 0}: "1 update", {5, 3}: "5 updates and 3 DLC", {0, 2}: "2 DLC", {0, 0}: ""} {
		if got := consoleFilesText("en", files[0], files[1]); got != want {
			t.Errorf("%v: %q, want %q", files, got, want)
		}
	}
	if got := consoleFilesText("es", 5, 3); got != "5 actualizaciones y 3 DLC" {
		t.Errorf("in Spanish: %q", got)
	}
	if got := playText("en", netPlayStats{Minutes: 2040, Launches: 1, LastPlayed: now.Add(-50 * time.Hour).Unix()}, now); got != "34 h · once · last played 2 days ago" {
		t.Errorf("played: %q", got)
	}
}
