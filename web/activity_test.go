package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestActivityAndLanguagePerUser(t *testing.T) {
	web := usersWeb(t)
	c := &client{t: t, handler: web.auth.middleware(web.withActivityLog(web.router))}

	// the first user enables the login and is logged in
	if response := c.do(http.MethodPost, "/users/create", url.Values{"name": {"ana"}, "password": {"password1"}}); response.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", response.Code)
	}
	c.do(http.MethodPost, "/users/create", url.Values{"name": {"luis"}, "password": {"password2"}, "role": {ROLE_VIEWER}})
	// a failure is not recorded
	c.do(http.MethodPost, "/users/create", url.Values{"name": {"bad name!"}, "password": {"password3"}})

	entries := web.activities().list()
	if len(entries) != 2 || entries[0].Action != ACTION_USER_NEW || entries[0].Detail != "luis" || entries[0].User != "ana" {
		t.Fatalf("activity: %+v", entries)
	}

	// the language of the user comes before the language of the browser
	if response := c.do(http.MethodPost, "/account/language", url.Values{"language": {"es"}}); response.Code != http.StatusSeeOther {
		t.Fatalf("language: %d", response.Code)
	}
	page := c.do(http.MethodGet, "/users.html", nil, "Accept-Language", "en")
	if !strings.Contains(page.Body.String(), "Actividad") || !strings.Contains(page.Body.String(), "Creó un usuario") {
		t.Fatal("the page is shown in the language of the user, with the activity")
	}
	c.do(http.MethodPost, "/account/language", url.Values{"language": {""}})
	if page := c.do(http.MethodGet, "/users.html", nil, "Accept-Language", "en"); !strings.Contains(page.Body.String(), "Activity") {
		t.Fatal("without a language of their own, users see the language of the app or browser")
	}
}

func TestActivityTextsAreTranslated(t *testing.T) {
	for _, action := range activityActions {
		if _, ok := translations["es"][action]; !ok {
			t.Errorf("no Spanish translation for %q", action)
		}
	}
}
