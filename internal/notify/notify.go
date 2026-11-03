// Package notify sends the messages of the app to the channels set in Settings (Discord,
// Telegram, ntfy, Gotify, Pushover, email and a webhook), checks their settings, and keeps
// their secrets out of the pages.
package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"go.uber.org/zap"
)

var (
	// redirects are not followed: a webhook answering with one could send the request
	// to an address of the local network
	notificationClient = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	discordWebhookRegex = regexp.MustCompile(`^https://(discord\.com|discordapp\.com|ptb\.discord\.com|canary\.discord\.com)/api/webhooks/[0-9]+/[A-Za-z0-9_-]+$`)
	telegramTokenRegex  = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{20,}$`)
	telegramChatRegex   = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z0-9_]{5,})$`)
	webhookRegex        = regexp.MustCompile(`^https?://[^\s]+$`)
	// every address a notification is sent to: http or https and a host, which may be one of the
	// local network, as a server of one's own
	notificationURLRegex = regexp.MustCompile(`^https?://[^/?#\s]+([/?#]\S*)?$`)

	// the API of Telegram, replaced in tests
	TelegramApiUrl = "https://api.telegram.org"
)

// Configured reports whether a channel is set to send the messages to.
func Configured(options settings.NotificationOptions) bool {
	return len(notificationChannels(options)) > 0
}

// notificationChannel is a configured channel: its name, as the results of a test say it, and
// how it sends a message.
type notificationChannel struct {
	name string
	send func(title string, text string, items any) error
}

// notificationChannels are the configured channels, in the order of the Settings page.
func notificationChannels(options settings.NotificationOptions) []notificationChannel {
	channels := []notificationChannel{}
	add := func(name string, send func(title string, text string, items any) error) {
		channels = append(channels, notificationChannel{name: name, send: send})
	}
	if options.DiscordWebhookUrl != "" {
		add("Discord", func(title string, text string, _ any) error {
			return PostJSON(options.DiscordWebhookUrl, map[string]string{"content": FitMessage("**"+title+"**\n"+text, discordMessageLimit)})
		})
	}
	if options.TelegramBotToken != "" && options.TelegramChatId != "" {
		add("Telegram", func(title string, text string, _ any) error {
			return PostJSON(TelegramApiUrl+"/bot"+options.TelegramBotToken+"/sendMessage", map[string]string{"chat_id": options.TelegramChatId, "text": FitMessage(title+"\n"+text, TelegramMessageLimit)})
		})
	}
	if options.NtfyUrl != "" {
		add("ntfy", func(title string, text string, _ any) error { return sendNtfy(options, title, text) })
	}
	if options.GotifyUrl != "" && options.GotifyToken != "" {
		add("Gotify", func(title string, text string, _ any) error { return sendGotify(options, title, text) })
	}
	if options.PushoverUser != "" && options.PushoverToken != "" {
		add("Pushover", func(title string, text string, _ any) error { return sendPushover(options, title, text) })
	}
	if emailConfigured(options) {
		add("Email", func(title string, text string, _ any) error { return sendEmail(options, title, text) })
	}
	if options.WebhookUrl != "" {
		add("Webhook", func(title string, text string, items any) error {
			return PostJSON(options.WebhookUrl, map[string]any{"title": title, "message": text, "items": items})
		})
	}
	return channels
}

// PostJSON sends a value in JSON to an address of a channel (see post).
func PostJSON(url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return post(url, "application/json", body, nil)
}

// post sends a message to a channel. The addresses of the settings were checked when saved, but
// settings.json may have been changed by hand since: it must be http or https and have a host,
// which may be one of the local network (a server of one's own).
func post(address string, contentType string, body []byte, header http.Header) error {
	if !notificationURLRegex.MatchString(address) {
		return failed("not a valid address")
	}
	request, err := http.NewRequest(http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return failed("not a valid address")
	}
	for name, values := range header {
		request.Header[name] = values
	}
	request.Header.Set("Content-Type", contentType)
	resp, err := notificationClient.Do(request)
	if err != nil {
		// the error holds the address, which may hold a secret (Discord, Telegram): not told
		return failed("no answer from the server")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return failed("the server answered %v", resp.StatusCode)
	}
	return nil
}

// the longest message each channel takes, with room for the title
const (
	discordMessageLimit  = 1900
	TelegramMessageLimit = 4000
)

// FitMessage cuts a message to a number of characters at the end of a line, saying more
// followed: a message too long is refused by Discord and Telegram.
func FitMessage(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	cut := string(runes[:limit])
	if i := strings.LastIndex(cut, "\n"); i > 0 {
		cut = cut[:i]
	}
	return cut + "\n…"
}

// Result is what a channel did with a message: Err is nil when it took it.
type Result struct {
	Channel string
	Err     error
}

// Deliver sends a message to every configured channel at once; items go to the JSON webhook.
// The results are in the order of the channels.
func Deliver(options settings.NotificationOptions, title string, text string, items any) []Result {
	channels := notificationChannels(options)
	results := make([]Result, len(channels))
	var wait sync.WaitGroup
	for i, channel := range channels {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[i] = Result{Channel: channel.name, Err: channel.send(title, text, items)}
		}()
	}
	wait.Wait()
	return results
}

// Send sends a message to every configured channel; items go to the JSON webhook. It
// fails only when no channel took it: a channel that failed is reported in the log, and the
// others do not get the same message again and again because of it.
func Send(options settings.NotificationOptions, title string, text string, items any) error {
	errs := []string{}
	sent := 0
	for _, result := range Deliver(options, title, text, items) {
		if result.Err != nil {
			errs = append(errs, result.Channel+": "+result.Err.Error())
		} else {
			sent++
		}
	}

	if len(errs) > 0 {
		if sent > 0 {
			zap.S().Warnf("Notification not sent to every channel: %s", strings.Join(errs, "; "))
			return nil
		}
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// SecretMask stands for a saved secret in the forms: the pages never get the bot token of
// Telegram nor the token of the webhook of Discord back. A field sent back as it was shown keeps
// the saved value, a new one replaces it and an empty one turns the channel off, as before.
const SecretMask = "********"

// Masked are the notification settings as the pages show them.
func Masked(options settings.NotificationOptions) settings.NotificationOptions {
	options.TelegramBotToken = maskedSecret(options.TelegramBotToken)
	options.DiscordWebhookUrl = maskedWebhook(options.DiscordWebhookUrl)
	options.WebhookUrl = MaskedURL(options.WebhookUrl)
	options.NtfyUrl = MaskedURL(options.NtfyUrl)
	options.NtfyToken = maskedSecret(options.NtfyToken)
	options.GotifyUrl = MaskedURL(options.GotifyUrl)
	options.GotifyToken = maskedSecret(options.GotifyToken)
	options.PushoverToken = maskedSecret(options.PushoverToken)
	options.SmtpPassword = maskedSecret(options.SmtpPassword)
	return options
}

// maskedWebhook is a webhook of Discord with the mask in place of its token, the part after the
// last slash: https://discord.com/api/webhooks/<id>/********
func maskedWebhook(webhook string) string {
	if webhook == "" {
		return ""
	}
	return webhook[:strings.LastIndex(webhook, "/")+1] + SecretMask
}

// KeptSecrets puts the saved secrets back in the notification settings sent by a form, where the
// form sent them as they were shown.
func KeptSecrets(sent settings.NotificationOptions, saved settings.NotificationOptions) settings.NotificationOptions {
	sent.TelegramBotToken = keptSecret(sent.TelegramBotToken, saved.TelegramBotToken)
	if sent.DiscordWebhookUrl != "" && sent.DiscordWebhookUrl == maskedWebhook(saved.DiscordWebhookUrl) {
		sent.DiscordWebhookUrl = saved.DiscordWebhookUrl
	}
	sent.WebhookUrl = KeptURL(sent.WebhookUrl, saved.WebhookUrl)
	sent.NtfyUrl = KeptURL(sent.NtfyUrl, saved.NtfyUrl)
	sent.NtfyToken = keptSecret(sent.NtfyToken, saved.NtfyToken)
	sent.GotifyUrl = KeptURL(sent.GotifyUrl, saved.GotifyUrl)
	sent.GotifyToken = keptSecret(sent.GotifyToken, saved.GotifyToken)
	sent.PushoverToken = keptSecret(sent.PushoverToken, saved.PushoverToken)
	sent.SmtpPassword = keptSecret(sent.SmtpPassword, saved.SmtpPassword)
	return sent
}

// FieldError is a field of the notification settings that is not valid, and why.
type FieldError struct {
	Field   string
	Message string
}

// Validate checks the notification settings of the settings form.
func Validate(options settings.NotificationOptions, lang string) []FieldError {
	errs := []FieldError{}
	if options.DiscordWebhookUrl != "" && !discordWebhookRegex.MatchString(options.DiscordWebhookUrl) {
		errs = append(errs, FieldError{Field: "discord_webhook_url", Message: i18n.Translate(lang, "Enter the webhook URL copied from Discord (https://discord.com/api/webhooks/...)")})
	}
	if (options.TelegramBotToken == "") != (options.TelegramChatId == "") {
		errs = append(errs, FieldError{Field: "telegram_chat_id", Message: i18n.Translate(lang, "Telegram needs both the bot token and the chat ID")})
	} else if options.TelegramBotToken != "" {
		if !telegramTokenRegex.MatchString(options.TelegramBotToken) {
			errs = append(errs, FieldError{Field: "telegram_bot_token", Message: i18n.Translate(lang, "Invalid bot token (as given by @BotFather)")})
		}
		if !telegramChatRegex.MatchString(options.TelegramChatId) {
			errs = append(errs, FieldError{Field: "telegram_chat_id", Message: i18n.Translate(lang, "Invalid chat ID (a number or @channel)")})
		}
	}
	if options.WebhookUrl != "" && !webhookRegex.MatchString(options.WebhookUrl) {
		errs = append(errs, FieldError{Field: "webhook_url", Message: i18n.Translate(lang, "Invalid URL")})
	}
	// both fields of a channel, or none: the message goes to the one left empty
	emptyOf := func(first string, firstField string, secondField string) string {
		if first == "" {
			return firstField
		}
		return secondField
	}
	if options.NtfyUrl != "" || options.NtfyToken != "" {
		if _, _, ok := ntfyTarget(options.NtfyUrl); !ok {
			errs = append(errs, FieldError{Field: "ntfy_url", Message: i18n.Translate(lang, "Write the address of the topic, like https://ntfy.sh/my-topic")})
		}
		if options.NtfyToken != "" && !ntfyTokenRegex.MatchString(options.NtfyToken) {
			errs = append(errs, FieldError{Field: "ntfy_token", Message: i18n.Translate(lang, "Invalid token")})
		}
	}
	if (options.GotifyUrl == "") != (options.GotifyToken == "") {
		errs = append(errs, FieldError{Field: emptyOf(options.GotifyUrl, "gotify_url", "gotify_token"), Message: i18n.Translate(lang, "Gotify needs both the address of the server and the token of the application")})
	} else if options.GotifyUrl != "" {
		if _, ok := gotifyMessages(options.GotifyUrl); !ok {
			errs = append(errs, FieldError{Field: "gotify_url", Message: i18n.Translate(lang, "Write the address of the server, like https://gotify.example.com")})
		}
		if !gotifyTokenRegex.MatchString(options.GotifyToken) {
			errs = append(errs, FieldError{Field: "gotify_token", Message: i18n.Translate(lang, "Invalid token")})
		}
	}
	if (options.PushoverUser == "") != (options.PushoverToken == "") {
		errs = append(errs, FieldError{Field: emptyOf(options.PushoverUser, "pushover_user", "pushover_token"), Message: i18n.Translate(lang, "Pushover needs both the user key and the token of the application")})
	} else if options.PushoverUser != "" {
		if !pushoverKeyRegex.MatchString(options.PushoverUser) {
			errs = append(errs, FieldError{Field: "pushover_user", Message: i18n.Translate(lang, "Invalid key: 30 letters and numbers")})
		}
		if !pushoverKeyRegex.MatchString(options.PushoverToken) {
			errs = append(errs, FieldError{Field: "pushover_token", Message: i18n.Translate(lang, "Invalid key: 30 letters and numbers")})
		}
	}
	return append(errs, validateEmail(options, lang)...)
}

// validateEmail checks the settings of the mail, when one of them is written.
func validateEmail(options settings.NotificationOptions, lang string) []FieldError {
	errs := []FieldError{}
	if options.SmtpHost == "" && options.SmtpTo == "" && options.SmtpUser == "" && options.SmtpPassword == "" && options.SmtpFrom == "" {
		return errs
	}
	if host := options.SmtpHost; !smtpHostRegex.MatchString(host) && net.ParseIP(host) == nil {
		errs = append(errs, FieldError{Field: "smtp_host", Message: i18n.Translate(lang, "Write the name of the mail server, like smtp.example.com")})
	}
	if options.SmtpPort < 0 || options.SmtpPort > 65535 {
		errs = append(errs, FieldError{Field: "smtp_port", Message: i18n.Translate(lang, "Invalid port")})
	}
	security := emailSecurity(options)
	if !slices.Contains(settings.SmtpSecurityChoices, security) {
		errs = append(errs, FieldError{Field: "smtp_security", Message: i18n.Translate(lang, "Unknown option")})
	}
	if (options.SmtpUser == "") != (options.SmtpPassword == "") {
		field := "smtp_password"
		if options.SmtpUser == "" {
			field = "smtp_user"
		}
		errs = append(errs, FieldError{Field: field, Message: i18n.Translate(lang, "Write both the user and the password, or neither")})
	} else if options.SmtpUser != "" && security == settings.SMTP_NONE {
		errs = append(errs, FieldError{Field: "smtp_security", Message: i18n.Translate(lang, "Without encryption the password would travel in clear: choose STARTTLS or SSL/TLS")})
	}
	if from := emailFrom(options); from == "" {
		errs = append(errs, FieldError{Field: "smtp_from", Message: i18n.Translate(lang, "Write the address the mail comes from")})
	} else if _, err := mail.ParseAddress(from); err != nil {
		message := "Invalid email address"
		if options.SmtpFrom == "" {
			// the user is not an address: the mail needs one to come from
			message = "Write the address the mail comes from"
		}
		errs = append(errs, FieldError{Field: "smtp_from", Message: i18n.Translate(lang, message)})
	}
	if _, ok := emailRecipients(options.SmtpTo); !ok {
		errs = append(errs, FieldError{Field: "smtp_to", Message: i18n.Translatef(lang, "Write one or more email addresses, separated by commas (at most %v)", maxEmailRecipients)})
	}
	return errs
}
