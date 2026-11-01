package web

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

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

func requestTo(t *testing.T, received []receivedRequest, path string) receivedRequest {
	t.Helper()
	for _, request := range received {
		if request.path == path {
			return request
		}
	}
	t.Fatalf("nothing was sent to %s: %+v", path, received)
	return receivedRequest{}
}

const (
	testPushoverUser  = "uQiRzpo4DXghDmr9QzzfQu27cmVRsG"
	testPushoverToken = "azGDORePK8gMaC0QOYAMyEEuzJnyUi"
)

// ntfy gets the message in JSON at the root of its server, with the topic in it (sent to the
// topic, it would show the JSON as it is); Gotify at /message with the token of the application;
// Pushover as a form.
func TestNtfyGotifyAndPushover(t *testing.T) {
	server, received := channelServer(t, nil)
	savedPushover := pushoverApiUrl
	pushoverApiUrl = server.URL + "/1/messages.json"
	defer func() { pushoverApiUrl = savedPushover }()

	options := settings.NotificationOptions{
		NtfyUrl:       server.URL + "/ntfy/switch-games",
		NtfyToken:     "tk_testtoken",
		GotifyUrl:     server.URL + "/gotify/",
		GotifyToken:   "AbCdEf.12345",
		PushoverUser:  testPushoverUser,
		PushoverToken: testPushoverToken,
	}
	if err := sendNotification(options, "es", []NotificationItem{{Kind: "update", Name: "Juego", Detail: "v2"}}); err != nil {
		t.Fatal(err)
	}
	requests := received()
	if len(requests) != 3 {
		t.Fatalf("one message per channel: %+v", requests)
	}

	ntfy := requestTo(t, requests, "/ntfy/")
	var published map[string]any
	if err := json.Unmarshal([]byte(ntfy.body), &published); err != nil {
		t.Fatal(err)
	}
	if published["topic"] != "switch-games" || !strings.Contains(published["title"].(string), "Switch Library Manager") ||
		!strings.Contains(published["message"].(string), "Actualización v2 de Juego") || ntfy.header.Get("Authorization") != "Bearer tk_testtoken" {
		t.Fatalf("unexpected ntfy message: %+v %v", published, ntfy.header)
	}

	gotify := requestTo(t, requests, "/gotify/message")
	var message map[string]any
	if err := json.Unmarshal([]byte(gotify.body), &message); err != nil {
		t.Fatal(err)
	}
	if gotify.header.Get("X-Gotify-Key") != "AbCdEf.12345" || !strings.Contains(message["message"].(string), "Actualización v2 de Juego") || message["title"] == "" {
		t.Fatalf("unexpected Gotify message: %+v %v", message, gotify.header)
	}

	pushover := requestTo(t, requests, "/1/messages.json")
	form, err := url.ParseQuery(pushover.body)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("token") != testPushoverToken || form.Get("user") != testPushoverUser || !strings.Contains(form.Get("message"), "Actualización v2 de Juego") ||
		pushover.header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Fatalf("unexpected Pushover message: %v", form)
	}

	// a user and a password in the address of ntfy sign in with them
	host := strings.TrimPrefix(server.URL, "http://")
	if err := sendMessage(settings.NotificationOptions{NtfyUrl: "http://reader:pa55@" + host + "/games"}, "Title", "Text", nil); err != nil {
		t.Fatal(err)
	}
	last := received()[3]
	user, password, ok := (&http.Request{Header: last.header}).BasicAuth()
	if last.path != "/" || !ok || user != "reader" || password != "pa55" {
		t.Fatalf("ntfy with a user: %s %v", last.path, last.header)
	}
}

func TestNtfyAndGotifyAddresses(t *testing.T) {
	for address, want := range map[string][2]string{
		"https://ntfy.sh/games":            {"https://ntfy.sh/", "games"},
		"https://ntfy.sh/games/":           {"https://ntfy.sh/", "games"},
		"http://192.168.1.2:8080/ntfy/abc": {"http://192.168.1.2:8080/ntfy/", "abc"},
	} {
		root, topic, ok := ntfyTarget(address)
		if !ok || root != want[0] || topic != want[1] {
			t.Errorf("ntfyTarget(%q) = %q %q %v", address, root, topic, ok)
		}
	}
	for _, address := range []string{"https://ntfy.sh", "https://ntfy.sh/", "https://ntfy.sh/games?x=1", "ftp://ntfy.sh/games", "https://ntfy.sh/bad topic", "https://ntfy.sh/a.b"} {
		if _, _, ok := ntfyTarget(address); ok {
			t.Errorf("ntfyTarget(%q) accepted", address)
		}
	}
	for address, want := range map[string]string{
		"https://gotify.example.com":            "https://gotify.example.com/message",
		"https://gotify.example.com/":           "https://gotify.example.com/message",
		"https://example.com/gotify/message":    "https://example.com/gotify/message",
		"http://192.168.1.2:8070/gotify/":       "http://192.168.1.2:8070/gotify/message",
		"https://gotify.example.com/message?x=": "",
	} {
		if got, _ := gotifyMessages(address); got != want {
			t.Errorf("gotifyMessages(%q) = %q, want %q", address, got, want)
		}
	}
	long := fitBytes(strings.Repeat("ゲームの更新\n", 2000), ntfyMessageLimit)
	if len(long) > ntfyMessageLimit+5 || !utf8.ValidString(long) || !strings.HasSuffix(long, "\n…") {
		t.Fatalf("a long message is cut at the end of a line: %d bytes", len(long))
	}
}

// fakeSMTP is an SMTP server of the tests: STARTTLS or TLS from the start, PLAIN or LOGIN.
type fakeSMTP struct {
	listener    net.Listener
	tlsConfig   *tls.Config
	implicitTLS bool
	offerTLS    bool
	user        string
	password    string
	mechanisms  string

	mutex sync.Mutex
	mails []fakeMail
}

type fakeMail struct {
	from      string
	to        []string
	data      string
	encrypted bool
}

// testCertificate is the certificate of httptest, for 127.0.0.1, and a pool that trusts it.
func testCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.StartTLS()
	defer server.Close()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	return server.TLS.Certificates[0], pool
}

func startFakeSMTP(t *testing.T, server *fakeSMTP) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if server.implicitTLS {
		listener = tls.NewListener(listener, server.tlsConfig)
	}
	server.listener = listener
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return server
}

func (s *fakeSMTP) port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *fakeSMTP) received() []fakeMail {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]fakeMail{}, s.mails...)
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer func() { conn.Close() }()
	text := textproto.NewConn(conn)
	encrypted := s.implicitTLS
	signedIn := false
	mail := fakeMail{}
	text.PrintfLine("220 fake ESMTP")
	for {
		line, err := text.ReadLine()
		if err != nil {
			return
		}
		verb, argument, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			lines := []string{"fake"}
			if s.offerTLS && !encrypted {
				lines = append(lines, "STARTTLS")
			}
			if s.user != "" {
				lines = append(lines, "AUTH "+s.mechanisms)
			}
			for i, extension := range lines {
				separator := "-"
				if i == len(lines)-1 {
					separator = " "
				}
				text.PrintfLine("250%s%s", separator, extension)
			}
		case "STARTTLS":
			text.PrintfLine("220 go ahead")
			secure := tls.Server(conn, s.tlsConfig)
			if secure.Handshake() != nil {
				return
			}
			conn, encrypted = secure, true
			text = textproto.NewConn(conn)
		case "AUTH":
			fields := strings.Fields(argument)
			user, password := "", ""
			switch strings.ToUpper(fields[0]) {
			case "PLAIN":
				encoded := ""
				if len(fields) > 1 {
					encoded = fields[1]
				} else {
					text.PrintfLine("334 ")
					encoded, _ = text.ReadLine()
				}
				decoded, _ := base64.StdEncoding.DecodeString(encoded)
				if parts := strings.Split(string(decoded), "\x00"); len(parts) == 3 {
					user, password = parts[1], parts[2]
				}
			case "LOGIN":
				text.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte("Username:")))
				encoded, _ := text.ReadLine()
				decoded, _ := base64.StdEncoding.DecodeString(encoded)
				user = string(decoded)
				text.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte("Password:")))
				encoded, _ = text.ReadLine()
				decoded, _ = base64.StdEncoding.DecodeString(encoded)
				password = string(decoded)
			}
			if user == s.user && password == s.password {
				signedIn = true
				text.PrintfLine("235 2.7.0 Accepted")
			} else {
				// as some servers do, the words repeat the user
				text.PrintfLine("535 5.7.8 Authentication failed for %s", user)
			}
		case "MAIL":
			if s.user != "" && !signedIn {
				text.PrintfLine("530 5.7.0 Authentication required")
				continue
			}
			mail = fakeMail{from: argument, encrypted: encrypted}
			text.PrintfLine("250 ok")
		case "RCPT":
			mail.to = append(mail.to, argument)
			text.PrintfLine("250 ok")
		case "DATA":
			text.PrintfLine("354 go on")
			data, err := text.ReadDotBytes()
			if err != nil {
				return
			}
			mail.data = string(data)
			s.mutex.Lock()
			s.mails = append(s.mails, mail)
			s.mutex.Unlock()
			text.PrintfLine("250 queued")
		case "QUIT":
			text.PrintfLine("221 bye")
			return
		default:
			text.PrintfLine("502 unknown")
		}
	}
}

// bodyOf is the text of a mail in quoted-printable.
func bodyOf(t *testing.T, data string) string {
	t.Helper()
	// the server reads the lines of the mail without their \r
	_, body, ok := strings.Cut(strings.ReplaceAll(data, "\r\n", "\n"), "\n\n")
	if !ok {
		t.Fatalf("a mail without a body: %q", data)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(decoded), "\r\n", "\n")
}

func TestEmailNotifications(t *testing.T) {
	certificate, pool := testCertificate(t)
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{certificate}}
	savedRoots := smtpRootCAs
	smtpRootCAs = pool
	defer func() { smtpRootCAs = savedRoots }()
	items := []NotificationItem{{Kind: "update", Name: "ゼルダ", Detail: "v3"}}

	// STARTTLS, the usual way, with PLAIN; the mail comes from the user
	server := startFakeSMTP(t, &fakeSMTP{tlsConfig: tlsConfig, offerTLS: true, user: "library@example.com", password: "s3cret pass", mechanisms: "PLAIN LOGIN"})
	options := settings.NotificationOptions{SmtpHost: "127.0.0.1", SmtpPort: server.port(), SmtpUser: "library@example.com", SmtpPassword: "s3cret pass", SmtpTo: "me@example.com, Other <other@example.com>"}
	if err := sendNotification(options, "ja", items); err != nil {
		t.Fatal(err)
	}
	mails := server.received()
	if len(mails) != 1 {
		t.Fatalf("one mail: %+v", mails)
	}
	mail := mails[0]
	if !mail.encrypted || mail.from != "FROM:<library@example.com>" || len(mail.to) != 2 || mail.to[1] != "TO:<other@example.com>" {
		t.Fatalf("unexpected mail: %+v", mail)
	}
	if !strings.Contains(mail.data, "Subject: =?utf-8?q?") || !strings.Contains(mail.data, "Content-Type: text/plain; charset=utf-8") ||
		!strings.Contains(mail.data, "From: <library@example.com>") || !strings.Contains(bodyOf(t, mail.data), "ゼルダ") {
		t.Fatalf("unexpected mail:\n%s", mail.data)
	}

	// a wrong password: the code of the server, never its words (they repeat the user) nor the password
	options.SmtpPassword = "wrong pass"
	results := deliver(options, "Title", "Text", nil)
	if len(results) != 1 || results[0].err == nil || results[0].err.Error() != "the server refused the user or the password (535)" {
		t.Fatalf("a wrong password: %+v", results)
	}
	if text := failureText("es", results[0].err); text != "el servidor no aceptó el usuario o la contraseña (535)" {
		t.Fatalf("the reason in Spanish: %q", text)
	}

	// TLS from the start, with LOGIN only (as Outlook), from an address of its own
	implicit := startFakeSMTP(t, &fakeSMTP{tlsConfig: tlsConfig, implicitTLS: true, user: "apikey", password: "pw", mechanisms: "LOGIN"})
	options = settings.NotificationOptions{SmtpHost: "127.0.0.1", SmtpPort: implicit.port(), SmtpSecurity: settings.SMTP_TLS, SmtpUser: "apikey", SmtpPassword: "pw", SmtpFrom: "Library <library@example.com>", SmtpTo: "me@example.com"}
	if err := sendMessage(options, "Title", "Text", nil); err != nil {
		t.Fatal(err)
	}
	if mails := implicit.received(); len(mails) != 1 || !mails[0].encrypted || mails[0].from != "FROM:<library@example.com>" || !strings.Contains(mails[0].data, `From: "Library" <library@example.com>`) {
		t.Fatalf("TLS from the start: %+v", mails)
	}

	// a server of the local network, without encryption nor a password
	plain := startFakeSMTP(t, &fakeSMTP{})
	options = settings.NotificationOptions{SmtpHost: "127.0.0.1", SmtpPort: plain.port(), SmtpSecurity: settings.SMTP_NONE, SmtpFrom: "library@example.com", SmtpTo: "me@example.com"}
	if err := sendMessage(options, "Title", "Line 1\nLine 2", nil); err != nil {
		t.Fatal(err)
	}
	if mails := plain.received(); len(mails) != 1 || mails[0].encrypted || strings.TrimSpace(bodyOf(t, mails[0].data)) != "Line 1\nLine 2" {
		t.Fatalf("without encryption: %+v", mails)
	}

	// STARTTLS chosen and a server that does not offer it: nothing is sent in clear
	options.SmtpSecurity, options.SmtpUser, options.SmtpPassword = "", "library@example.com", "pw"
	if results := deliver(options, "Title", "Text", nil); results[0].err == nil || results[0].err.Error() != "the server does not offer STARTTLS" {
		t.Fatalf("no STARTTLS: %+v", results)
	}
	// a certificate the computer does not trust
	smtpRootCAs = x509.NewCertPool()
	options = settings.NotificationOptions{SmtpHost: "127.0.0.1", SmtpPort: server.port(), SmtpUser: "library@example.com", SmtpPassword: "s3cret pass", SmtpTo: "me@example.com"}
	if results := deliver(options, "Title", "Text", nil); results[0].err == nil || results[0].err.Error() != "the certificate of the server is not valid" {
		t.Fatalf("an untrusted certificate: %+v", results)
	}
}

// The test says how each channel went: green when every one took it, orange when some did not,
// red when none did. Why one failed holds neither its address nor its token.
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

// A channel that fails tells why without its address, its token or its password: not in the
// log, not in the error.
func TestChannelFailuresHoldNoSecrets(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()
	savedTelegram, savedPushover := telegramApiUrl, pushoverApiUrl
	telegramApiUrl, pushoverApiUrl = "http://127.0.0.1:1", "http://127.0.0.1:1/1/messages.json"
	defer func() { telegramApiUrl, pushoverApiUrl = savedTelegram, savedPushover }()

	server, _ := channelServer(t, func(string) int { return http.StatusForbidden })
	host := strings.TrimPrefix(server.URL, "http://")
	options := settings.NotificationOptions{
		TelegramBotToken: "123456:TELEGRAMSECRETTOKENXYZ", TelegramChatId: "-100",
		NtfyUrl: "http://ntfyuser:NTFYPASS@" + host + "/games", NtfyToken: "tk_NTFYTOKEN",
		GotifyUrl: "http://127.0.0.1:1", GotifyToken: "GOTIFYTOKEN1",
		PushoverUser: testPushoverUser, PushoverToken: testPushoverToken,
		SmtpHost: "127.0.0.1", SmtpPort: 1, SmtpUser: "library@example.com", SmtpPassword: "SMTPPASSWORD", SmtpTo: "me@example.com",
		WebhookUrl: "http://hookuser:HOOKPASS@" + host + "/hook?token=HOOKTOKEN",
	}
	err := sendMessage(options, "Title", "Text", nil)
	if err == nil {
		t.Fatal("every channel failed")
	}
	// one channel took it: the others go to the log
	working, _ := channelServer(t, nil)
	options.DiscordWebhookUrl = strings.Replace(working.URL, "http://", "http://", 1) + "/api/webhooks/1/DISCORDSECRET"
	if sendMessage(options, "Title", "Text", nil) != nil {
		t.Fatal("a channel took it")
	}
	written := err.Error()
	for _, entry := range logs.All() {
		written += "\n" + entry.Message
	}
	if !strings.Contains(written, "ntfy: the server answered 403") || !strings.Contains(written, "Email: no connection to 127.0.0.1:1") {
		t.Fatalf("the reasons are told:\n%s", written)
	}
	for _, secret := range []string{"TELEGRAMSECRET", "NTFYPASS", "tk_NTFYTOKEN", "GOTIFYTOKEN1", testPushoverToken, "SMTPPASSWORD", "HOOKPASS", "HOOKTOKEN", "DISCORDSECRET"} {
		if strings.Contains(written, secret) {
			t.Errorf("%s is in the log or the error:\n%s", secret, written)
		}
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
		for _, shown := range []string{`value="https://reader:` + secretMask + `@ntfy.example.com/games"`, `value="https://hooks.example.com/notify?token=` + secretMask + `&amp;room=7"`, `name="smtp_password" value="` + secretMask + `"`} {
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
	shown := maskedNotifications(saved)
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

func TestMaskedURL(t *testing.T) {
	for address, want := range map[string]string{
		"":                                   "",
		"https://hooks.example.com/notify":   "https://hooks.example.com/notify",
		"https://user:pw@host/x":             "https://user:" + secretMask + "@host/x",
		"https://tk_abc@host/x":              "https://" + secretMask + "@host/x",
		"https://host/x?room=7&api_key=abc":  "https://host/x?room=7&api_key=" + secretMask,
		"https://host/message?token=abc&p=5": "https://host/message?token=" + secretMask + "&p=5",
		"http://192.168.1.2:8123/api/webhook/x?Signature=y&Auth=z": "http://192.168.1.2:8123/api/webhook/x?Signature=" + secretMask + "&Auth=" + secretMask,
	} {
		if got := maskedURL(address); got != want {
			t.Errorf("maskedURL(%q) = %q, want %q", address, got, want)
		}
		if got := keptURL(maskedURL(address), address); got != address {
			t.Errorf("keptURL of the masked %q = %q", address, got)
		}
	}
	// a mask sent for another user is not taken for the saved password
	if got := keptURL("https://other:"+secretMask+"@host/x", "https://user:pw@host/x"); strings.Contains(got, "pw") {
		t.Fatalf("the mask of another user: %s", got)
	}
	// another part of the address changed, the secrets shown masked stay
	saved := "https://user:pw@hooks.example.com/notify?token=abc&room=7"
	if got := keptURL("https://user:"+secretMask+"@hooks.example.com/notify?token="+secretMask+"&room=8", saved); got != "https://user:pw@hooks.example.com/notify?token=abc&room=8" {
		t.Fatalf("another room: %s", got)
	}
	// never to another server
	if got := keptURL("https://user:"+secretMask+"@evil.example.com/notify?token="+secretMask, saved); strings.Contains(got, "pw") || strings.Contains(got, "abc") {
		t.Fatalf("a saved secret goes to another host: %s", got)
	}
}

func TestValidateNewChannels(t *testing.T) {
	valid := settings.NotificationOptions{
		NtfyUrl: "https://ntfy.sh/switch-games", NtfyToken: "tk_abc",
		GotifyUrl: "http://192.168.1.2:8070", GotifyToken: "AbCdEf.12345",
		PushoverUser: testPushoverUser, PushoverToken: testPushoverToken,
		SmtpHost: "smtp.example.com", SmtpPort: 465, SmtpSecurity: settings.SMTP_TLS, SmtpUser: "apikey", SmtpPassword: "pw", SmtpFrom: "Library <library@example.com>", SmtpTo: "me@example.com, you@example.com",
	}
	if errs := validateNotifications(valid, "en"); len(errs) != 0 {
		t.Fatalf("valid options rejected: %+v", errs)
	}
	if errs := validateNotifications(settings.NotificationOptions{SmtpHost: "192.168.1.5", SmtpSecurity: settings.SMTP_NONE, SmtpFrom: "nas@example.com", SmtpTo: "me@example.com"}, "en"); len(errs) != 0 {
		t.Fatalf("a server of the local network without a password: %+v", errs)
	}
	for field, options := range map[string]settings.NotificationOptions{
		"ntfy_url":       {NtfyUrl: "https://ntfy.sh"},
		"ntfy_token":     {NtfyUrl: "https://ntfy.sh/games", NtfyToken: "has space"},
		"gotify_token":   {GotifyUrl: "https://gotify.example.com"},
		"gotify_url":     {GotifyToken: "AbCdEf.12345"},
		"pushover_user":  {PushoverUser: "short", PushoverToken: testPushoverToken},
		"pushover_token": {PushoverUser: testPushoverUser},
		"smtp_host":      {SmtpHost: "smtp example", SmtpUser: "a@example.com", SmtpPassword: "pw", SmtpTo: "me@example.com"},
		"smtp_port":      {SmtpHost: "smtp.example.com", SmtpPort: 70000, SmtpUser: "a@example.com", SmtpPassword: "pw", SmtpTo: "me@example.com"},
		"smtp_password":  {SmtpHost: "smtp.example.com", SmtpUser: "a@example.com", SmtpTo: "me@example.com"},
		"smtp_security":  {SmtpHost: "smtp.example.com", SmtpSecurity: settings.SMTP_NONE, SmtpUser: "a@example.com", SmtpPassword: "pw", SmtpTo: "me@example.com"},
		"smtp_from":      {SmtpHost: "smtp.example.com", SmtpUser: "apikey", SmtpPassword: "pw", SmtpTo: "me@example.com"},
		"smtp_to":        {SmtpHost: "smtp.example.com", SmtpUser: "a@example.com", SmtpPassword: "pw", SmtpTo: "not an address"},
	} {
		found := false
		for _, err := range validateNotifications(options, "en") {
			found = found || err.Field == field
		}
		if !found {
			t.Errorf("%s: no error for %+v", field, options)
		}
	}
	if formPort("") != 0 || formPort("587") != 587 || formPort("x") != -1 || formSecurity("starttls") != "" || formSecurity("tls") != "tls" {
		t.Fatal("unexpected form values")
	}
	if channelNames("es", []string{"Telegram", "ntfy", "Correo"}) != "Telegram, ntfy y Correo" || channelNames("en", []string{"ntfy"}) != "ntfy" {
		t.Fatal("unexpected list of channels")
	}
	if got := strconv.Itoa(emailPort(settings.NotificationOptions{SmtpSecurity: settings.SMTP_TLS})); got != "465" {
		t.Fatalf("the port of TLS: %s", got)
	}
}
