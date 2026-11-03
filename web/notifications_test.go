package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/notify"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The test says how each channel went: green when every one took it, orange when some did not,
// red when none did. Why one failed holds neither its address nor its token.
// what a fake server of channels got
type receivedRequest struct {
	path   string
	header http.Header
	body   string
}

// channelServer answers as ntfy, Gotify, Pushover and a webhook would, with the status that
// status gives for a path, and records what it got.
func channelServer(t *testing.T, status func(path string) int) (*httptest.Server, func() []receivedRequest) {
	t.Helper()
	var mutex sync.Mutex
	received := []receivedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mutex.Lock()
		received = append(received, receivedRequest{path: r.URL.Path, header: r.Header.Clone(), body: string(body)})
		mutex.Unlock()
		if status != nil {
			w.WriteHeader(status(r.URL.Path))
		}
	}))
	t.Cleanup(server.Close)
	return server, func() []receivedRequest {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]receivedRequest{}, received...)
	}
}

const (
	testPushoverUser  = "uQiRzpo4DXghDmr9QzzfQu27cmVRsG"
	testPushoverToken = "azGDORePK8gMaC0QOYAMyEEuzJnyUi"
)

func TestNotificationTestSaysEachChannel(t *testing.T) {
	gotifyStatus := http.StatusOK
	server, _ := channelServer(t, func(path string) int {
		if strings.HasPrefix(path, "/gotify") {
			return gotifyStatus
		}
		return http.StatusOK
	})
	web := newTestWeb(t)
	web.HandleNotifications()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Notifications = settings.NotificationOptions{NtfyUrl: server.URL + "/games", GotifyUrl: server.URL + "/gotify", GotifyToken: "SecretToken123"}
	})
	test := func() (int, NotificationTestResponse, string) {
		recorder := postForm(web, "/notifications/test", url.Values{})
		var response NotificationTestResponse
		json.Unmarshal(recorder.Body.Bytes(), &response)
		return recorder.Code, response, recorder.Body.String()
	}

	code, response, _ := test()
	if code != http.StatusOK || response.StrongMessage != "Sent!" || response.Message != "To ntfy and Gotify. Check your notification channels." || len(response.Failed) != 0 {
		t.Fatalf("every channel: %d %+v", code, response)
	}
	gotifyStatus = http.StatusUnauthorized
	code, response, body := test()
	if code != http.StatusOK || response.StrongMessage != "Sent to ntfy." || len(response.Failed) != 1 || response.Failed[0] != "Gotify: the server answered 401" {
		t.Fatalf("one channel failed: %d %+v", code, response)
	}
	if strings.Contains(body, "SecretToken123") || strings.Contains(body, server.URL) {
		t.Fatalf("the answer holds a secret or an address: %s", body)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Notifications.NtfyUrl = "http://127.0.0.1:1/games"
	})
	code, response, _ = test()
	if code != http.StatusBadGateway || response.GlobalError == nil || response.GlobalError.Message != "It reached no channel." ||
		len(response.Failed) != 2 || response.Failed[0] != "ntfy: no answer from the server" {
		t.Fatalf("no channel: %d %+v", code, response)
	}
}

// The tokens and passwords of the new channels, and a password or a token written in an address,
// are shown masked, kept when sent back as shown and replaced by a new value.
func TestNewChannelSecretsAreNotShown(t *testing.T) {
	web, _ := compressWeb(t)
	web.HandleSettings()
	web.HandleWizard()
	saved := settings.NotificationOptions{
		NtfyUrl: "https://reader:NTFYPASS@ntfy.example.com/games", NtfyToken: "tk_NTFYTOKEN",
		GotifyUrl: "https://gotify.example.com", GotifyToken: "GOTIFYTOKEN1",
		PushoverUser: testPushoverUser, PushoverToken: testPushoverToken,
		SmtpHost: "smtp.example.com", SmtpPort: 587, SmtpUser: "library@example.com", SmtpPassword: "SMTPPASSWORD", SmtpTo: "me@example.com",
		WebhookUrl: "https://hooks.example.com/notify?token=HOOKTOKEN&room=7",
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Notifications = saved })
	for _, path := range []string{"/settings.html", "/wizard"} {
		page := httptest.NewRecorder()
		withSecurityHeaders(web.router).ServeHTTP(page, httptest.NewRequest(http.MethodGet, path, nil))
		body := page.Body.String()
		if page.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, page.Code)
		}
		for _, secret := range []string{"NTFYPASS", "tk_NTFYTOKEN", "GOTIFYTOKEN1", testPushoverToken, "SMTPPASSWORD", "HOOKTOKEN"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s shows %s", path, secret)
			}
		}
		for _, shown := range []string{`value="https://reader:` + notify.SecretMask + `@ntfy.example.com/games"`, `value="https://hooks.example.com/notify?token=` + notify.SecretMask + `&amp;room=7"`, `name="smtp_password" value="` + notify.SecretMask + `"`} {
			if !strings.Contains(body, shown) {
				t.Errorf("%s does not show %s", path, shown)
			}
		}
	}
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/settings.html", nil))
	if body := page.Body.String(); !strings.Contains(body, "ntfy.example.com/games") || !strings.Contains(body, "hooks.example.com") || !strings.Contains(body, "me@example.com") {
		t.Fatal("the rows say where each channel sends")
	}

	folder := t.TempDir()
	shown := notify.Masked(saved)
	form := url.Values{
		"scan_folders": {folder},
		"ntfy_url":     {shown.NtfyUrl}, "ntfy_token": {shown.NtfyToken},
		"gotify_url": {shown.GotifyUrl}, "gotify_token": {shown.GotifyToken},
		"pushover_user": {shown.PushoverUser}, "pushover_token": {shown.PushoverToken},
		"smtp_host": {"smtp.example.com"}, "smtp_port": {"587"}, "smtp_security": {"starttls"}, "smtp_user": {"library@example.com"}, "smtp_password": {shown.SmtpPassword}, "smtp_to": {"me@example.com"},
		"webhook_url": {shown.WebhookUrl},
		// the card chosen in the wizard goes with its form, and is left aside
		"wizard_notify_service": {"ntfy"},
	}
	if response := postForm(web, "/settings.html", form); response.Code != http.StatusOK {
		t.Fatalf("saving: %d %s", response.Code, response.Body.String())
	}
	if got := settings.ReadSettings(web.dataFolder).Notifications; got.NtfyUrl != saved.NtfyUrl || got.NtfyToken != saved.NtfyToken || got.GotifyToken != saved.GotifyToken ||
		got.PushoverToken != saved.PushoverToken || got.SmtpPassword != saved.SmtpPassword || got.WebhookUrl != saved.WebhookUrl || got.SmtpSecurity != "" || got.SmtpPort != 587 {
		t.Fatalf("sent back as shown, the secrets are kept: %+v", got)
	}
	form.Set("smtp_password", "new password")
	form.Set("webhook_url", "https://hooks.example.com/notify?token=NEW&room=7")
	if response := postForm(web, "/settings.html", form); response.Code != http.StatusOK {
		t.Fatalf("saving: %d %s", response.Code, response.Body.String())
	}
	if got := settings.ReadSettings(web.dataFolder).Notifications; got.SmtpPassword != "new password" || got.WebhookUrl != "https://hooks.example.com/notify?token=NEW&room=7" {
		t.Fatalf("new secrets replace them: %+v", got)
	}
}

// The fields of the form of the channels are read as the settings keep them, and the
// channels of a test are listed as a sentence.
func TestNotificationFormValues(t *testing.T) {
	if formPort("") != 0 || formPort("587") != 587 || formPort("x") != -1 || formSecurity("starttls") != "" || formSecurity("tls") != "tls" {
		t.Fatal("unexpected form values")
	}
	if channelNames("es", []string{"Telegram", "ntfy", "Correo"}) != "Telegram, ntfy y Correo" || channelNames("en", []string{"ntfy"}) != "ntfy" {
		t.Fatal("unexpected list of channels")
	}
}
