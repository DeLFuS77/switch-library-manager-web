package web

import (
	"os"
	"strings"
	"testing"
)

// The menus of the header: the full bar from 1200px with a mouse, the dot of the menu button,
// the account menu with only what is the user's own, and the pages of Settings in its index.
func TestMenus(t *testing.T) {
	set, err := parseTemplates(os.DirFS(".."), "resources/layout.html", "resources/partials/*.html", "resources/pages/disclaimer.html")
	if err != nil {
		t.Fatal(err)
	}
	render := func(auth AuthInfo, counts NavCounts) string {
		t.Helper()
		var out strings.Builder
		data := GlobalPageData{Auth: auth, Counts: counts, IsKeysFileAvailable: true, Page: "disclaimer"}
		if err := set["en"].ExecuteTemplate(&out, "layout", data); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	part := func(page, start, end string) string {
		t.Helper()
		i := strings.Index(page, start)
		if i < 0 {
			t.Fatalf("no %s", start)
		}
		j := strings.Index(page[i:], end)
		return page[i : i+j]
	}
	admin := AuthInfo{Enabled: true, User: "alice", IsAdmin: true, Role: ROLE_ADMIN, CanEdit: true, CanDownload: true}
	nobody := AuthInfo{IsAdmin: true, Role: ROLE_ADMIN, CanEdit: true, CanDownload: true}

	page := render(admin, NavCounts{Issues: 2})
	if !strings.Contains(page, `class="navbar navbar-expand-full"`) || strings.Contains(page, "xxl-") {
		t.Error("the bar follows the screens of the full header, not Bootstrap's xxl")
	}
	if !strings.Contains(page, `class="toggler-dot is-problem"`) {
		t.Error("the menu button warns of the issues inside")
	}
	// the account menu: what is the user's; Settings has its gear, Users, Activity and
	// Diagnostics are in its index
	account := part(page, `<div class="dropdown account-menu">`, "</ul>")
	for _, want := range []string{`href="/account.html"`, "Log out", "data-couch-toggle"} {
		if !strings.Contains(account, want) {
			t.Errorf("the account menu has no %s", want)
		}
	}
	for _, unwanted := range []string{`href="/settings.html"`, `href="/users.html"`, `href="/diagnostics.html"`} {
		if strings.Contains(account, unwanted) {
			t.Errorf("the account menu still has %s", unwanted)
		}
	}
	if !strings.Contains(page, "settings-shortcut full-header-only-flex") {
		t.Error("the gear of Settings on the full header")
	}
	// the folded menu: Settings for the administrator, and the sections of the bar at the bottom
	// marked to be left out of More
	panel := part(page, `<div class="offcanvas offcanvas-end"`, `<div class="full-header-hide nav-account-section">`)
	for _, want := range []string{`href="/settings.html"`, `<li class="nav-item in-bottom-bar">`, `<li class="nav-item in-bottom-bar-wide">`, "app-nav-compact me-full-2 in-bottom-bar-wide"} {
		if !strings.Contains(panel, want) {
			t.Errorf("the panel has no %s", want)
		}
	}

	// without sign-in the gear is the account menu: Settings and the preferences of this browser
	page = render(nobody, NavCounts{TuneUp: true})
	account = part(page, `<div class="dropdown account-menu">`, "</ul>")
	if !strings.Contains(account, `href="/settings.html"`) || strings.Contains(account, `href="/users.html"`) || strings.Contains(account, "Log out") {
		t.Errorf("the gear menu without sign-in: %s", account)
	}
	if !strings.Contains(page, `<span class="toggler-dot" aria-hidden="true">`) {
		t.Error("the orange dot of the tune-up")
	}
	if page = render(nobody, NavCounts{}); strings.Contains(page, "toggler-dot") {
		t.Error("a dot with nothing waiting")
	}

	// the index of Settings: the sections renamed and the pages that belong to it
	var nav strings.Builder
	if err := set["en"].ExecuteTemplate(&nav, "settingsNav", ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"What is shown", `href="#gamedata"`, "Sync and check", `href="/users.html"`, `href="/activity.html"`, `href="/diagnostics.html"`} {
		if !strings.Contains(nav.String(), want) {
			t.Errorf("the index of Settings has no %s", want)
		}
	}
}
