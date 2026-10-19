package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// What each role may do: a guest looks, a player also downloads, an editor also looks after the
// library without deleting, an administrator does everything.
func TestRoles(t *testing.T) {
	type check struct {
		method, path                 string
		guest, player, editor, admin bool
	}
	checks := []check{
		{"GET", "/index.html", true, true, true, true},
		{"GET", "/title/0100000000010000.html", true, true, true, true},
		{"POST", "/play", true, true, true, true},
		{"GET", "/api/titles", true, true, true, true},
		{"GET", "/api/titles/0100000000010000", false, true, true, true},
		{"GET", "/api/titles/0100000000010000/updates/65536", false, true, true, true},
		{"GET", "/api/titles/0100000000010000/dlc/0100000000011001", false, true, true, true},
		{"GET", "/api/titles/0100000000010000/archive.zip", false, true, true, true},
		{"GET", "/api/titles/0100000000010000/cheats.zip", false, true, true, true},
		{"GET", "/api/cheats.zip", false, true, true, true},
		{"GET", "/export/library.csv", false, true, true, true},
		{"POST", "/sd/list.txt", false, true, true, true},
		{"POST", "/sync", false, false, true, true},
		{"POST", "/uploads/start", false, false, true, true},
		{"PUT", "/uploads/chunk", false, false, true, true},
		{"POST", "/favorites", false, false, true, true},
		{"POST", "/smart-collections", false, false, true, true},
		{"GET", "/compress.html", false, false, true, true},
		{"POST", "/compress/start", false, false, true, true},
		{"POST", "/api/tasks/7/dismiss", false, false, true, true},
		{"POST", "/space/clean", false, false, false, true},
		{"POST", "/issues/delete-file", false, false, false, true},
		{"POST", "/organize/run", false, false, false, true},
		{"GET", "/settings.html", false, false, false, true},
		{"POST", "/users/create", false, false, false, true},
		{"GET", "/backup/download", false, false, false, true},
	}
	for _, c := range checks {
		request := httptest.NewRequest(c.method, c.path, nil)
		for role, want := range map[string]bool{ROLE_GUEST: c.guest, ROLE_VIEWER: c.player, ROLE_EDITOR: c.editor, ROLE_ADMIN: c.admin} {
			if got := roleAllows(role, request); got != want {
				t.Errorf("%s %s by %s: %v, want %v", c.method, c.path, role, got, want)
			}
		}
	}
	// a role this version does not know is a guest
	if roleAllows("superuser", httptest.NewRequest(http.MethodGet, "/api/cheats.zip", nil)) {
		t.Error("an unknown role downloads nothing")
	}
	if !validRole(ROLE_GUEST) || !validRole(ROLE_EDITOR) || validRole("root") {
		t.Error("the roles a user can have")
	}
}
