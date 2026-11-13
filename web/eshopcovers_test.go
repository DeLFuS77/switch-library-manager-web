package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/eshop"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
)

func searchEshop(t *testing.T, web *Web, fields url.Values) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/eshop/search", strings.NewReader(fields.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	web.router.ServeHTTP(recorder, request)
	return recorder
}

// Edit searches the eShop of Europe by the name written, with the games of the Switch 2 only when
// asked, and gives the games with a square image and a search of the web for the same name.
func TestEshopSearch(t *testing.T) {
	var asked url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query()
		w.Write([]byte(`{"response":{"docs":[
			{"title":"Made-up Quest","system_names_txt":["Nintendo Switch 2"],"image_url_sq_s":"https://img-eu.nintendo.example/quest.jpg"},
			{"title":"Made-up Quest Demo","system_names_txt":["Nintendo Switch 2"],"image_url_sq_s":"http://elsewhere.example/demo.jpg"}]}}`))
	}))
	defer server.Close()
	address, hosts := eshop.SearchAddress, eshop.ImageHosts
	eshop.SearchAddress, eshop.ImageHosts = server.URL, append(eshop.ImageHosts, "nintendo.example")
	defer func() { eshop.SearchAddress, eshop.ImageHosts = address, hosts }()

	web := newTestWeb(t)
	web.HandleApi()
	recorder := searchEshop(t, web, url.Values{"q": {"Made-up Quest [0100000000010000][v0].nsp"}, "switch2": {"true"}})
	if recorder.Code != http.StatusOK {
		t.Fatalf("%d %s", recorder.Code, recorder.Body)
	}
	var answer EshopSearchAnswer
	if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if asked.Get("q") != "Made-up Quest" || !strings.Contains(asked.Get("fq"), "nintendoswitch2") {
		t.Errorf("asked %v", asked)
	}
	if len(answer.Games) != 1 || answer.Games[0].Image != "https://img-eu.nintendo.example/quest.jpg" {
		t.Errorf("games %+v", answer.Games)
	}
	if !strings.HasPrefix(answer.Web, "https://duckduckgo.com/") || !strings.Contains(answer.Web, "Switch+2") {
		t.Errorf("web %s", answer.Web)
	}

	// all the consoles, and a search of the web for the Switch
	recorder = searchEshop(t, web, url.Values{"q": {"Made-up Quest"}})
	json.Unmarshal(recorder.Body.Bytes(), &answer)
	if strings.Contains(asked.Get("fq"), "nintendoswitch2") || strings.Contains(answer.Web, "Switch+2") {
		t.Errorf("asked %v, web %s", asked, answer.Web)
	}
}

// No name, a name too long and an eShop that cannot be reached are told, in the language of the
// page; only administrators search.
func TestEshopSearchRefusals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	address := eshop.SearchAddress
	eshop.SearchAddress = server.URL
	defer func() { eshop.SearchAddress = address }()

	web := newTestWeb(t)
	web.HandleApi()
	for _, test := range []struct {
		name    string
		code    int
		message string
	}{
		{"  [v0] ", http.StatusBadRequest, ESHOP_NO_NAME},
		{strings.Repeat("long ", 60), http.StatusBadRequest, ESHOP_LONG_NAME},
		{"Made-up Quest", http.StatusBadGateway, ESHOP_UNREACHABLE},
	} {
		recorder := searchEshop(t, web, url.Values{"q": {test.name}})
		if recorder.Code != test.code || !strings.Contains(recorder.Body.String(), test.message) {
			t.Errorf("%q: %d %s", test.name, recorder.Code, recorder.Body)
		}
	}
	for _, role := range []string{ROLE_EDITOR, ROLE_VIEWER, ROLE_GUEST} {
		if roleAllows(role, httptest.NewRequest(http.MethodPost, "/api/eshop/search", nil)) {
			t.Errorf("%s searches", role)
		}
	}
	for _, lang := range i18n.SupportedLanguages {
		if lang == i18n.DEFAULT_LANGUAGE {
			continue
		}
		for _, text := range []string{ESHOP_NO_NAME, ESHOP_LONG_NAME, ESHOP_UNREACHABLE} {
			if i18n.Translate(lang, text) == text {
				t.Errorf("%s: %q", lang, text)
			}
		}
	}
}

// The page of a game has the search, with Switch 2 only chosen for a game of the Switch 2.
func TestEshopSearchOnTheGamePage(t *testing.T) {
	web, _ := compressWeb(t)
	noIconNupGame(t, web, true)
	web.HandleTitle()
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/title/0400486024DEC000.html", nil))
	body := recorder.Body.String()
	for _, part := range []string{"data-cover-search-open", `id="coverSearch"`, "data-cover-search-switch2 checked"} {
		if !strings.Contains(body, part) {
			t.Errorf("no %s", part)
		}
	}
}
