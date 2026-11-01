package web

import (
	"os"
	"strings"
	"testing"
)

// The bar at the bottom of phones and tablets: More in the place of the menu button, its dot for
// what waits inside, and the account menu at the top for every signed-in user.
func TestBottomNav(t *testing.T) {
	set, err := parseTemplates(os.DirFS(".."), "resources/layout.html", "resources/partials/*.html", "resources/pages/disclaimer.html")
	if err != nil {
		t.Fatal(err)
	}
	render := func(auth AuthInfo, counts NavCounts) (page, bar string) {
		t.Helper()
		var out strings.Builder
		data := GlobalPageData{Auth: auth, Counts: counts, IsKeysFileAvailable: true, Page: "disclaimer"}
		if err := set["en"].ExecuteTemplate(&out, "layout", data); err != nil {
			t.Fatal(err)
		}
		page = out.String()
		start := strings.Index(page, `<nav class="bottom-nav"`)
		if start < 0 {
			t.Fatal("no bar at the bottom")
		}
		end := strings.Index(page[start:], "</nav>")
		return page, page[start : start+end]
	}
	admin := AuthInfo{Enabled: true, User: "alice", IsAdmin: true, Role: ROLE_ADMIN, CanEdit: true, CanDownload: true}
	player := AuthInfo{Enabled: true, User: "bob", Role: ROLE_VIEWER, CanDownload: true}

	// a read-only user: the account menu at the top; Issues and Tasks in the bar of a tablet, and
	// in the red dot of More on a phone. The tune-up is not theirs.
	page, bar := render(player, NavCounts{Issues: 3, FailedTasks: 1, TuneUp: true})
	if !strings.Contains(page, `class="dropdown account-menu"`) || !strings.Contains(page, "nav-account-section") {
		t.Error("the account menu, at the top and in the panel")
	}
	for _, want := range []string{`href="/dlc.html"`, `href="/issues.html"`, `href="/tasks.html"`, ">More<", "bottom-dot is-problem bottom-narrow-only", "Failed tasks: 1"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the bar of a read-only user has no %s", want)
		}
	}
	if strings.Contains(bar, "bottom-dot bottom-wide-only") || strings.Contains(bar, "tune-up") {
		t.Error("the tune-up is for the administrators")
	}

	// an administrator with a step of the tune-up waiting, and nothing else: the orange dot
	_, bar = render(admin, NavCounts{TuneUp: true})
	for _, want := range []string{`"bottom-dot bottom-narrow-only"`, `"bottom-dot bottom-wide-only"`, "A step of the tune-up is waiting"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the tune-up waits: no %s", want)
		}
	}
	if strings.Contains(bar, "is-problem") {
		t.Error("a step of the tune-up is not a problem")
	}

	// nothing waits: no dot
	if _, bar = render(admin, NavCounts{}); strings.Contains(bar, "bottom-dot") {
		t.Error("a dot with nothing waiting")
	}
}
