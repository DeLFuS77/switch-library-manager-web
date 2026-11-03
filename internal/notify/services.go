package notify

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The channels added to Discord, Telegram and the webhook: ntfy, Gotify, Pushover and email.

var (
	ntfyTopicRegex   = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)
	ntfyTokenRegex   = regexp.MustCompile(`^\S{1,256}$`)
	gotifyTokenRegex = regexp.MustCompile(`^[A-Za-z0-9._-]{8,128}$`)
	pushoverKeyRegex = regexp.MustCompile(`^[A-Za-z0-9]{30}$`)
	smtpHostRegex    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	// the parameters of an address that hold a secret: ?token=..., ?api_key=..., ?password=...
	secretParameterRegex = regexp.MustCompile(`(?i)token|key|secret|pass|auth|sig`)

	// replaced in tests
	pushoverApiUrl = "https://api.pushover.net/1/messages.json"
	// the certificates the SMTP server is checked against: those of the system when nil
	smtpRootCAs *x509.CertPool
)

// the longest message each channel takes
const (
	// in bytes, with room for "…"
	ntfyMessageLimit     = 4000
	pushoverMessageLimit = 1000
	pushoverTitleLimit   = 250
	// recipients of a mail
	maxEmailRecipients = 20
)

// sendFailure is why a channel did not take a message, as the person reads it: never with the
// address nor a secret of the channel, as it goes to the log and to the page.
type sendFailure struct {
	text string
	args []any
}

func (f sendFailure) Error() string {
	return fmt.Sprintf(f.text, f.args...)
}

func failed(text string, args ...any) error {
	return sendFailure{text: text, args: args}
}

// FailureText is why a channel did not take a message, in a language.
func FailureText(lang string, err error) string {
	var failure sendFailure
	if errors.As(err, &failure) {
		return i18n.Translatef(lang, failure.text, failure.args...)
	}
	return i18n.Translate(lang, "it could not be sent")
}

// webAddress parses an address of the settings: http or https, with a host and with nothing
// after a ?, nor after a #.
func webAddress(address string) (*url.URL, bool) {
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(address, " \t\r\n") {
		return nil, false
	}
	return parsed, true
}

// ntfyTarget is the root of the server of an ntfy topic, and the topic. A message in JSON goes to
// the root with the topic in it: sent to the topic, ntfy shows the JSON as it is. A user and a
// password written in the address stay in the root.
func ntfyTarget(address string) (string, string, bool) {
	parsed, ok := webAddress(address)
	if !ok {
		return "", "", false
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	slash := strings.LastIndex(path, "/")
	if slash < 0 || !ntfyTopicRegex.MatchString(path[slash+1:]) {
		return "", "", false
	}
	topic := path[slash+1:]
	parsed.Path, parsed.RawPath = path[:slash+1], ""
	return parsed.String(), topic, true
}

func sendNtfy(options settings.NotificationOptions, title string, text string) error {
	root, topic, ok := ntfyTarget(options.NtfyUrl)
	if !ok {
		return failed("not a valid address")
	}
	body, err := json.Marshal(map[string]any{"topic": topic, "title": title, "message": fitBytes(text, ntfyMessageLimit), "tags": []string{"video_game"}})
	if err != nil {
		return err
	}
	header := http.Header{}
	if options.NtfyToken != "" {
		header.Set("Authorization", "Bearer "+options.NtfyToken)
	}
	return post(root, "application/json", body, header)
}

// gotifyMessages is the address of the messages of a Gotify server: <server>/message.
func gotifyMessages(address string) (string, bool) {
	parsed, ok := webAddress(address)
	if !ok {
		return "", false
	}
	parsed.Path = strings.TrimSuffix(strings.TrimSuffix(parsed.Path, "/"), "/message") + "/message"
	parsed.RawPath = ""
	return parsed.String(), true
}

func sendGotify(options settings.NotificationOptions, title string, text string) error {
	address, ok := gotifyMessages(options.GotifyUrl)
	if !ok {
		return failed("not a valid address")
	}
	// without a priority, the one of the application in Gotify
	body, err := json.Marshal(map[string]string{"title": title, "message": text})
	if err != nil {
		return err
	}
	return post(address, "application/json", body, http.Header{"X-Gotify-Key": {options.GotifyToken}})
}

func sendPushover(options settings.NotificationOptions, title string, text string) error {
	if runes := []rune(title); len(runes) > pushoverTitleLimit {
		title = string(runes[:pushoverTitleLimit])
	}
	form := url.Values{
		"token":   {options.PushoverToken},
		"user":    {options.PushoverUser},
		"title":   {title},
		"message": {FitMessage(text, pushoverMessageLimit)},
	}
	return post(pushoverApiUrl, "application/x-www-form-urlencoded", []byte(form.Encode()), nil)
}

// fitBytes is FitMessage for a channel whose limit is in bytes.
func fitBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := text[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	if i := strings.LastIndex(cut, "\n"); i > 0 {
		cut = cut[:i]
	}
	return cut + "\n…"
}

func emailConfigured(options settings.NotificationOptions) bool {
	return options.SmtpHost != "" && options.SmtpTo != ""
}

// emailSecurity is the security of the connection to the SMTP server: STARTTLS when not chosen.
func emailSecurity(options settings.NotificationOptions) string {
	if options.SmtpSecurity == "" {
		return settings.SMTP_STARTTLS
	}
	return options.SmtpSecurity
}

// emailPort is the port of the SMTP server: the usual one of its security when not written.
func emailPort(options settings.NotificationOptions) int {
	if options.SmtpPort != 0 {
		return options.SmtpPort
	}
	switch emailSecurity(options) {
	case settings.SMTP_TLS:
		return 465
	case settings.SMTP_NONE:
		return 25
	}
	return 587
}

// emailFrom is the address a mail comes from: the one written, or the user.
func emailFrom(options settings.NotificationOptions) string {
	if options.SmtpFrom != "" {
		return options.SmtpFrom
	}
	return options.SmtpUser
}

// emailRecipients are the addresses a mail goes to, separated by commas.
func emailRecipients(to string) ([]*mail.Address, bool) {
	addresses, err := mail.ParseAddressList(to)
	if err != nil || len(addresses) == 0 || len(addresses) > maxEmailRecipients {
		return nil, false
	}
	return addresses, true
}

// smtpTimeout is the longest a mail may take, from the connection to its end.
var smtpTimeout = 30 * time.Second

func sendEmail(options settings.NotificationOptions, title string, text string) error {
	from, err := mail.ParseAddress(emailFrom(options))
	if err != nil {
		return failed("the address the mail comes from is not valid")
	}
	to, ok := emailRecipients(options.SmtpTo)
	if !ok {
		return failed("the addresses the mail goes to are not valid")
	}

	security := emailSecurity(options)
	address := net.JoinHostPort(options.SmtpHost, strconv.Itoa(emailPort(options)))
	tlsConfig := &tls.Config{ServerName: options.SmtpHost, RootCAs: smtpRootCAs, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if security == settings.SMTP_TLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return connectionFailure(err, address)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(smtpTimeout))

	client, err := smtp.NewClient(conn, options.SmtpHost)
	if err != nil {
		return smtpFailure(err, false)
	}
	defer client.Close()
	if security == settings.SMTP_STARTTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return failed("the server does not offer STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return connectionFailure(err, address)
		}
	}
	if options.SmtpUser != "" {
		ok, mechanisms := client.Extension("AUTH")
		if !ok {
			return failed("the server does not ask for a user and a password")
		}
		var auth smtp.Auth = smtp.PlainAuth("", options.SmtpUser, options.SmtpPassword, options.SmtpHost)
		if offered := " " + strings.ToUpper(mechanisms) + " "; !strings.Contains(offered, " PLAIN ") && strings.Contains(offered, " LOGIN ") {
			auth = &loginAuth{user: options.SmtpUser, password: options.SmtpPassword, host: options.SmtpHost}
		}
		if err := client.Auth(auth); err != nil {
			return smtpFailure(err, true)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return smtpFailure(err, false)
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient.Address); err != nil {
			return smtpFailure(err, false)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return smtpFailure(err, false)
	}
	if _, err := writer.Write(emailMessage(from, to, title, text)); err != nil {
		return smtpFailure(err, false)
	}
	if err := writer.Close(); err != nil {
		return smtpFailure(err, false)
	}
	// the mail was taken: a server that closes without answering the end does not undo it
	client.Quit()
	return nil
}

// connectionFailure is why the connection to an SMTP server failed: the host and the port are
// told, they hold no secret.
func connectionFailure(err error, address string) error {
	var certificate *tls.CertificateVerificationError
	if errors.As(err, &certificate) {
		return failed("the certificate of the server is not valid")
	}
	return failed("no connection to %v", address)
}

// smtpFailure is why an SMTP server did not take a mail: its code only, as its words may repeat
// the user.
func smtpFailure(err error, signingIn bool) error {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		if signingIn || reply.Code == 530 || reply.Code == 534 || reply.Code == 535 {
			return failed("the server refused the user or the password (%v)", reply.Code)
		}
		return failed("the server refused the mail (%v)", reply.Code)
	}
	if signingIn {
		return failed("the password is only sent over an encrypted connection")
	}
	return failed("the connection to the server was cut")
}

// loginAuth signs in with LOGIN, for the servers that do not take PLAIN (Outlook). As PlainAuth,
// it never sends the password without encryption.
type loginAuth struct {
	user, password, host string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.password), nil
	}
	return nil, errors.New("unexpected question of the server")
}

// emailMessage is a mail in plain text, with its subject and its text in UTF-8.
func emailMessage(from *mail.Address, to []*mail.Address, subject string, text string) []byte {
	recipients := make([]string, len(to))
	for i, address := range to {
		recipients[i] = address.String()
	}
	id := make([]byte, 12)
	rand.Read(id)
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]

	var message bytes.Buffer
	header := func(name string, value string) {
		message.WriteString(name + ": " + value + "\r\n")
	}
	header("From", from.String())
	header("To", strings.Join(recipients, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", strings.NewReplacer("\r", " ", "\n", " ").Replace(subject)))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	header("Auto-Submitted", "auto-generated")
	message.WriteString("\r\n")
	body := quotedprintable.NewWriter(&message)
	body.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n")))
	body.Close()
	return message.Bytes()
}

// MaskedURL is an address with the mask in place of a password written in it, and of the values
// of the parameters that look like secrets: https://user:********@host/notify?token=********. A
// user without a password may be a token: it is masked too.
func MaskedURL(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || address == "" {
		return address
	}
	// written by hand: the address would write the mask escaped
	userinfo := ""
	if parsed.User != nil {
		userinfo = SecretMask + "@"
		if _, ok := parsed.User.Password(); ok {
			userinfo = url.User(parsed.User.Username()).String() + ":" + SecretMask + "@"
		}
		parsed.User = nil
	}
	changed := userinfo != ""
	if parsed.RawQuery != "" {
		parameters := strings.Split(parsed.RawQuery, "&")
		for i, parameter := range parameters {
			if name, _, found := strings.Cut(parameter, "="); found && secretParameterRegex.MatchString(name) {
				parameters[i] = name + "=" + SecretMask
				changed = true
			}
		}
		parsed.RawQuery = strings.Join(parameters, "&")
	}
	if !changed {
		return address
	}
	prefix := parsed.Scheme + "://"
	return prefix + userinfo + strings.TrimPrefix(parsed.String(), prefix)
}

// KeptURL is the address sent by a form, with the saved secrets where the form sent the mask:
// also when another part of the address was changed, but only for the same host, so a secret
// never goes to another server.
func KeptURL(sent string, saved string) string {
	if sent == "" || saved == "" || !strings.Contains(sent, SecretMask) {
		return sent
	}
	if sent == MaskedURL(saved) {
		return saved
	}
	sentURL, err := url.Parse(sent)
	if err != nil {
		return sent
	}
	savedURL, err := url.Parse(saved)
	if err != nil || !strings.EqualFold(sentURL.Host, savedURL.Host) {
		return sent
	}
	if sentURL.User != nil && savedURL.User != nil {
		password, hasPassword := sentURL.User.Password()
		savedPassword, savedHasPassword := savedURL.User.Password()
		switch {
		case hasPassword && password == SecretMask && savedHasPassword && sentURL.User.Username() == savedURL.User.Username():
			sentURL.User = url.UserPassword(savedURL.User.Username(), savedPassword)
		case !hasPassword && sentURL.User.Username() == SecretMask && !savedHasPassword:
			sentURL.User = savedURL.User
		}
	}
	savedValues := map[string]string{}
	for _, parameter := range strings.Split(savedURL.RawQuery, "&") {
		if name, value, found := strings.Cut(parameter, "="); found {
			savedValues[name] = value
		}
	}
	parameters := strings.Split(sentURL.RawQuery, "&")
	for i, parameter := range parameters {
		if name, value, found := strings.Cut(parameter, "="); found && value == SecretMask {
			if savedValue, ok := savedValues[name]; ok {
				parameters[i] = name + "=" + savedValue
			}
		}
	}
	sentURL.RawQuery = strings.Join(parameters, "&")
	return sentURL.String()
}

// keptSecret is a secret sent by a form, or the saved one when the form sent the mask.
func keptSecret(sent string, saved string) string {
	if sent == SecretMask && saved != "" {
		return saved
	}
	return sent
}

// maskedSecret is a secret as the pages show it.
func maskedSecret(secret string) string {
	if secret == "" {
		return ""
	}
	return SecretMask
}

// ServiceView is a notification channel as the Settings page and the wizard show it.
type ServiceView struct {
	Key string
	// in English, translated by the page
	Name string
	Icon string
	// where it sends, without its secrets: "" when it is off
	Target string
}

// Services are the notification channels in the order of the pages.
func Services(options settings.NotificationOptions) []ServiceView {
	targets := notificationTargets(options)
	services := []ServiceView{
		{Key: "discord", Name: "Discord", Icon: "bi-discord"},
		{Key: "telegram", Name: "Telegram", Icon: "bi-telegram"},
		{Key: "ntfy", Name: "ntfy", Icon: "bi-bell"},
		{Key: "gotify", Name: "Gotify", Icon: "bi-hdd-network"},
		{Key: "pushover", Name: "Pushover", Icon: "bi-phone-vibrate"},
		{Key: "email", Name: "Email", Icon: "bi-envelope-at"},
		{Key: "webhook", Name: "Other webhook", Icon: "bi-broadcast"},
	}
	for i := range services {
		services[i].Target = targets[services[i].Key]
	}
	return services
}

// notificationTargets says where each configured channel sends, without its secrets, for the
// rows of the Settings page: "" for the channels that are off.
func notificationTargets(options settings.NotificationOptions) map[string]string {
	hostAndPath := func(address string) string {
		parsed, err := url.Parse(address)
		if err != nil {
			return ""
		}
		return parsed.Host + strings.TrimSuffix(parsed.EscapedPath(), "/")
	}
	targets := map[string]string{}
	if options.DiscordWebhookUrl != "" {
		parts := strings.Split(options.DiscordWebhookUrl, "/")
		targets["discord"] = "Webhook " + parts[len(parts)-2]
	}
	if options.TelegramBotToken != "" && options.TelegramChatId != "" {
		targets["telegram"] = "Chat " + options.TelegramChatId
	}
	if options.NtfyUrl != "" {
		targets["ntfy"] = hostAndPath(options.NtfyUrl)
	}
	if options.GotifyUrl != "" && options.GotifyToken != "" {
		targets["gotify"] = hostAndPath(options.GotifyUrl)
	}
	if options.PushoverUser != "" && options.PushoverToken != "" {
		targets["pushover"] = options.PushoverUser[:min(4, len(options.PushoverUser))] + "…"
	}
	if emailConfigured(options) {
		targets["email"] = options.SmtpTo
	}
	if options.WebhookUrl != "" {
		if parsed, err := url.Parse(options.WebhookUrl); err == nil {
			targets["webhook"] = parsed.Host
		}
	}
	return targets
}
