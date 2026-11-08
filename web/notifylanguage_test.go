package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Without a language chosen in the settings, the notifications are written in the language the
// interface was last used in, kept across restarts; a script calling the API without telling a
// language changes nothing, and the language of the settings comes first.
func TestNotificationsFollowTheInterfaceLanguage(t *testing.T) {
	web := newTestWeb(t)
	if lang := web.notificationLanguage(""); lang != "en" {
		t.Fatalf("before any page: %s", lang)
	}

	page := httptest.NewRequest("GET", "/", nil)
	page.Header.Set("Accept-Language", "fr-FR,fr;q=0.9,en;q=0.8")
	if lang := web.requestLanguage(page); lang != "fr" {
		t.Fatalf("the page: %s", lang)
	}
	if lang := web.notificationLanguage(""); lang != "fr" {
		t.Errorf("after a page in French: %s", lang)
	}
	title, text := notificationText(web.notificationLanguage(""), []NotificationItem{{Kind: "new", Name: "Moon Runner"}})
	if strings.Contains(title, "new item") || !strings.Contains(text, "Moon Runner") || strings.Contains(text, "New in your library") {
		t.Errorf("written in French: %q %q", title, text)
	}

	// a script of the API tells no language
	web.requestLanguage(httptest.NewRequest("GET", "/api/titles", nil))
	if lang := web.notificationLanguage(""); lang != "fr" {
		t.Errorf("after the API: %s", lang)
	}

	// kept across a restart
	again := &Web{dataFolder: web.dataFolder, sugarLogger: zap.NewNop().Sugar()}
	if lang := again.notificationLanguage(""); lang != "fr" {
		t.Errorf("after a restart: %s", lang)
	}

	// the language chosen in the settings comes first
	if lang := again.notificationLanguage("de"); lang != "de" {
		t.Errorf("chosen in the settings: %s", lang)
	}
}
