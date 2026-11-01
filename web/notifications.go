package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"go.uber.org/zap"
)

const NOTIFICATIONS_STATE_FILENAME = "notifications.json"

// cheatsRecordedKey tells in the state that the cheats of the library were looked at once.
const cheatsRecordedKey = "cheats:recorded"

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

	// replaced in tests
	telegramApiUrl = "https://api.telegram.org"
)

// NotificationItem is something new to report, e.g. an update or a DLC that became available.
type NotificationItem struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	TitleId string `json:"titleId"`
	Name    string `json:"name"`
	Detail  string `json:"detail,omitempty"`
}

// availableItems lists the missing updates and DLC of the library, as configured to notify.
func (web *Web) availableItems(lang string) []NotificationItem {
	switchDB, localDB := web.state.get()
	items := []NotificationItem{}
	if switchDB == nil || localDB == nil {
		return items
	}
	settingsObj := settings.ReadSettings(web.dataFolder)
	options := settingsObj.Notifications

	if options.NotifyUpdates {
		// the same lists as the pages: ignored titles and hidden demos are left out
		for _, title := range web.shownMissingUpdates(lang) {
			id := strings.ToUpper(title.Attributes.Id)
			items = append(items, NotificationItem{
				Key:     fmt.Sprintf("update:%s:%d", id, title.LatestUpdate),
				Kind:    "update",
				TitleId: id,
				Name:    titleName(switchDB, lang, id, title.Attributes.Name),
				Detail:  fmt.Sprintf("v%d", title.LatestUpdate),
			})
		}
	}

	if options.NotifyDlc {
		for _, title := range web.missingDLC() {
			gameName := titleName(switchDB, lang, title.Attributes.Id, title.Attributes.Name)
			for _, dlc := range title.MissingDLCItems {
				id := strings.ToUpper(dlc.Id)
				items = append(items, NotificationItem{
					Key:     "dlc:" + id,
					Kind:    "dlc",
					TitleId: strings.ToUpper(title.Attributes.Id),
					Name:    gameName,
					Detail:  titleName(switchDB, lang, id, dlc.Name),
				})
			}
		}
	}

	if options.NotifyWishlist {
		today := time.Now().Format("20060102")
		for _, id := range web.wishes().ids() {
			title, ok := switchDB.TitlesMap[strings.ToLower(id[:13])]
			if !ok {
				continue
			}
			name := titleName(switchDB, lang, id, title.Attributes.Name)
			released := title.Attributes.ReleaseDate
			// released, or in the store without a known date
			if released == 0 || strconv.Itoa(released) <= today {
				items = append(items, NotificationItem{Key: "wish:" + id, Kind: "wish", TitleId: id, Name: name})
			}
			for dlcId, dlc := range title.Dlc {
				dlcId = strings.ToUpper(dlcId)
				items = append(items, NotificationItem{Key: "wishdlc:" + dlcId, Kind: "wishdlc", TitleId: id, Name: name, Detail: titleName(switchDB, lang, dlcId, dlc.Name)})
			}
		}
	}

	if options.NotifyCheats {
		// the build is in the key: cheats for a newer version the library got are new too
		cheats := web.cheats()
		for prefix, local := range localDB.TitlesMap {
			if !local.BaseExist || local.File.Metadata == nil {
				continue
			}
			id := strings.ToUpper(local.File.Metadata.TitleId)
			if !cheats.has(id, local.LatestUpdate) {
				continue
			}
			name := titleName(switchDB, lang, id, getLocalTitleName(switchDB.TitlesMap[prefix], local))
			items = append(items, NotificationItem{Key: "cheats:" + id + ":" + cheats.buildID(id, local.LatestUpdate), Kind: "cheats", TitleId: id, Name: name, Detail: fmt.Sprintf("v%d", local.LatestUpdate)})
		}
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items
}

func (web *Web) notificationStatePath() string {
	return filepath.Join(web.dataFolder, NOTIFICATIONS_STATE_FILENAME)
}

// loadNotifiedKeys returns the items already reported, or nil when nothing was recorded yet.
func (web *Web) loadNotifiedKeys() map[string]struct{} {
	data, err := os.ReadFile(web.notificationStatePath())
	if err != nil {
		return nil
	}
	keys := []string{}
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil
	}
	result := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		result[key] = struct{}{}
	}
	return result
}

func (web *Web) saveNotifiedKeys(items []NotificationItem) error {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return writeFileAtomic(web.notificationStatePath(), data)
}

func notificationsConfigured(options settings.NotificationOptions) bool {
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
			return postJSON(options.DiscordWebhookUrl, map[string]string{"content": fitMessage("**"+title+"**\n"+text, discordMessageLimit)})
		})
	}
	if options.TelegramBotToken != "" && options.TelegramChatId != "" {
		add("Telegram", func(title string, text string, _ any) error {
			return postJSON(telegramApiUrl+"/bot"+options.TelegramBotToken+"/sendMessage", map[string]string{"chat_id": options.TelegramChatId, "text": fitMessage(title+"\n"+text, telegramMessageLimit)})
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
			return postJSON(options.WebhookUrl, map[string]any{"title": title, "message": text, "items": items})
		})
	}
	return channels
}

// notifyChanges reports the updates and DLC that became available since the last check.
// The first check only records the current state, so existing gaps are not reported. It
// returns an error if the notification could not be sent.
func (web *Web) notifyChanges() error {
	settingsObj := settings.ReadSettings(web.dataFolder)
	options := settingsObj.Notifications
	// games bought since the last check leave the wishlist
	web.forgetOwnedWishes()
	if !notificationsConfigured(options) || (!options.NotifyUpdates && !options.NotifyDlc && !options.NotifyWishlist && !options.NotifyCheats) {
		return nil
	}

	lang := settingsObj.Language
	if !isSupportedLanguage(lang) {
		lang = DEFAULT_LANGUAGE
	}
	// without the titles or the library nothing is known: saved as empty, every update and
	// DLC would be reported as new by the next synchronization
	if switchDB, localDB := web.state.get(); switchDB == nil || localDB == nil {
		return nil
	}
	web.notifying.Lock()
	defer web.notifying.Unlock()

	current := web.availableItems(lang)
	notified := web.loadNotifiedKeys()

	if notified != nil {
		// the first time cheats are looked at, the games that have them already are only
		// recorded: the next ones are reported, not hundreds at once
		cheatsKnown := false
		for key := range notified {
			if strings.HasPrefix(key, "cheats:") || key == cheatsRecordedKey {
				cheatsKnown = true
				break
			}
		}
		newItems := []NotificationItem{}
		for _, item := range current {
			if _, ok := notified[item.Key]; !ok && (item.Kind != "cheats" || cheatsKnown) {
				newItems = append(newItems, item)
			}
		}
		if len(newItems) > 0 {
			if err := sendNotification(options, lang, newItems); err != nil {
				// keep the previous state, so the items are reported by the next synchronization
				web.sugarLogger.Warnf("Failed to send notification: %v", err)
				return err
			}
			web.sugarLogger.Infof("Notification sent for %d new item(s)", len(newItems))
		}
	}

	// the items of a kind turned off are kept as reported: turned on again, it does not report
	// at once everything that was missing meanwhile
	kept := append([]NotificationItem{}, current...)
	enabled := map[string]bool{"update": options.NotifyUpdates, "dlc": options.NotifyDlc, "wish": options.NotifyWishlist, "wishdlc": options.NotifyWishlist, "cheats": options.NotifyCheats}
	// the cheats were looked at, also when no game has any yet
	if options.NotifyCheats && web.cheats() != nil {
		kept = append(kept, NotificationItem{Key: cheatsRecordedKey, Kind: "cheats"})
	}
	for key := range notified {
		if kind, _, ok := strings.Cut(key, ":"); ok && !enabled[kind] {
			kept = append(kept, NotificationItem{Key: key, Kind: kind})
		}
	}
	if err := web.saveNotifiedKeys(kept); err != nil {
		web.sugarLogger.Warnf("Failed to save notification state: %v", err)
	}
	return nil
}

// notifyNewContent reports the games and DLC that appeared in the folders.
func (web *Web) notifyNewContent(events []HistoryEvent) {
	settingsObj := settings.ReadSettings(web.dataFolder)
	options := settingsObj.Notifications
	if !notificationsConfigured(options) || !options.NotifyNewGames {
		return
	}
	lang := settingsObj.Language
	if !isSupportedLanguage(lang) {
		lang = DEFAULT_LANGUAGE
	}
	switchDB, _ := web.state.get()
	items := []NotificationItem{}
	for _, event := range events {
		if !event.Added || event.Kind == HISTORY_UPDATE {
			continue
		}
		name := titleName(switchDB, lang, event.Id, event.Name)
		if name == "" {
			name = event.Id
		}
		kind := "new"
		if event.Kind == HISTORY_DLC {
			kind = "newdlc"
		}
		items = append(items, NotificationItem{Key: kind + ":" + event.Id, Kind: kind, TitleId: event.Id, Name: name})
	}
	if len(items) == 0 {
		return
	}
	if err := sendNotification(options, lang, items); err != nil {
		web.sugarLogger.Warnf("Failed to send notification: %v", err)
		return
	}
	web.sugarLogger.Infof("Notification sent for %d new game(s) and DLC", len(items))
}

const maxNotificationLines = 20

func notificationText(lang string, items []NotificationItem) (string, string) {
	title := translatef(lang, "Switch Library Manager: %v new item(s)", len(items))
	lines := []string{}
	for i, item := range items {
		if i == maxNotificationLines {
			lines = append(lines, translatef(lang, "and %v more", len(items)-maxNotificationLines))
			break
		}
		switch item.Kind {
		case "update":
			lines = append(lines, "• "+translatef(lang, "Update %v for %v", item.Detail, item.Name))
		case "dlc":
			lines = append(lines, "• "+translatef(lang, "DLC %v for %v", item.Detail, item.Name))
		case "wish":
			lines = append(lines, "• "+translatef(lang, "On your wishlist and available: %v", item.Name))
		case "wishdlc":
			lines = append(lines, "• "+translatef(lang, "DLC %v for %v (on your wishlist)", item.Detail, item.Name))
		case "new":
			lines = append(lines, "• "+translatef(lang, "New in your library: %v", item.Name))
		case "newdlc":
			lines = append(lines, "• "+translatef(lang, "New DLC in your library: %v", item.Name))
		case "cheats":
			lines = append(lines, "• "+translatef(lang, "Cheats for %v (%v, the version you have)", item.Name, item.Detail))
		default:
			lines = append(lines, "• "+item.Name)
		}
	}
	return title, strings.Join(lines, "\n")
}

func postJSON(url string, payload any) error {
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

// sendNotification sends the items to every configured channel.
func sendNotification(options settings.NotificationOptions, lang string, items []NotificationItem) error {
	title, text := notificationText(lang, items)
	return sendMessage(options, title, text, items)
}

// the longest message each channel takes, with room for the title
const (
	discordMessageLimit  = 1900
	telegramMessageLimit = 4000
)

// fitMessage cuts a message to a number of characters at the end of a line, saying more
// followed: a message too long is refused by Discord and Telegram.
func fitMessage(text string, limit int) string {
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

// channelResult is what a channel did with a message: err is nil when it took it.
type channelResult struct {
	channel string
	err     error
}

// deliver sends a message to every configured channel at once; items go to the JSON webhook.
// The results are in the order of the channels.
func deliver(options settings.NotificationOptions, title string, text string, items any) []channelResult {
	channels := notificationChannels(options)
	results := make([]channelResult, len(channels))
	var wait sync.WaitGroup
	for i, channel := range channels {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[i] = channelResult{channel: channel.name, err: channel.send(title, text, items)}
		}()
	}
	wait.Wait()
	return results
}

// sendMessage sends a message to every configured channel; items go to the JSON webhook. It
// fails only when no channel took it: a channel that failed is reported in the log, and the
// others do not get the same message again and again because of it.
func sendMessage(options settings.NotificationOptions, title string, text string, items any) error {
	errs := []string{}
	sent := 0
	for _, result := range deliver(options, title, text, items) {
		if result.err != nil {
			errs = append(errs, result.channel+": "+result.err.Error())
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

// secretMask stands for a saved secret in the forms: the pages never get the bot token of
// Telegram nor the token of the webhook of Discord back. A field sent back as it was shown keeps
// the saved value, a new one replaces it and an empty one turns the channel off, as before.
const secretMask = "********"

// maskedNotifications are the notification settings as the pages show them.
func maskedNotifications(options settings.NotificationOptions) settings.NotificationOptions {
	options.TelegramBotToken = maskedSecret(options.TelegramBotToken)
	options.DiscordWebhookUrl = maskedWebhook(options.DiscordWebhookUrl)
	options.WebhookUrl = maskedURL(options.WebhookUrl)
	options.NtfyUrl = maskedURL(options.NtfyUrl)
	options.NtfyToken = maskedSecret(options.NtfyToken)
	options.GotifyUrl = maskedURL(options.GotifyUrl)
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
	return webhook[:strings.LastIndex(webhook, "/")+1] + secretMask
}

// keptSecrets puts the saved secrets back in the notification settings sent by a form, where the
// form sent them as they were shown.
func keptSecrets(sent settings.NotificationOptions, saved settings.NotificationOptions) settings.NotificationOptions {
	sent.TelegramBotToken = keptSecret(sent.TelegramBotToken, saved.TelegramBotToken)
	if sent.DiscordWebhookUrl != "" && sent.DiscordWebhookUrl == maskedWebhook(saved.DiscordWebhookUrl) {
		sent.DiscordWebhookUrl = saved.DiscordWebhookUrl
	}
	sent.WebhookUrl = keptURL(sent.WebhookUrl, saved.WebhookUrl)
	sent.NtfyUrl = keptURL(sent.NtfyUrl, saved.NtfyUrl)
	sent.NtfyToken = keptSecret(sent.NtfyToken, saved.NtfyToken)
	sent.GotifyUrl = keptURL(sent.GotifyUrl, saved.GotifyUrl)
	sent.GotifyToken = keptSecret(sent.GotifyToken, saved.GotifyToken)
	sent.PushoverToken = keptSecret(sent.PushoverToken, saved.PushoverToken)
	sent.SmtpPassword = keptSecret(sent.SmtpPassword, saved.SmtpPassword)
	return sent
}

// validateNotifications checks the notification settings of the settings form.
func validateNotifications(options settings.NotificationOptions, lang string) []FieldError {
	errs := []FieldError{}
	if options.DiscordWebhookUrl != "" && !discordWebhookRegex.MatchString(options.DiscordWebhookUrl) {
		errs = append(errs, FieldError{Field: "discord_webhook_url", Message: translate(lang, "Enter the webhook URL copied from Discord (https://discord.com/api/webhooks/...)")})
	}
	if (options.TelegramBotToken == "") != (options.TelegramChatId == "") {
		errs = append(errs, FieldError{Field: "telegram_chat_id", Message: translate(lang, "Telegram needs both the bot token and the chat ID")})
	} else if options.TelegramBotToken != "" {
		if !telegramTokenRegex.MatchString(options.TelegramBotToken) {
			errs = append(errs, FieldError{Field: "telegram_bot_token", Message: translate(lang, "Invalid bot token (as given by @BotFather)")})
		}
		if !telegramChatRegex.MatchString(options.TelegramChatId) {
			errs = append(errs, FieldError{Field: "telegram_chat_id", Message: translate(lang, "Invalid chat ID (a number or @channel)")})
		}
	}
	if options.WebhookUrl != "" && !webhookRegex.MatchString(options.WebhookUrl) {
		errs = append(errs, FieldError{Field: "webhook_url", Message: translate(lang, "Invalid URL")})
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
			errs = append(errs, FieldError{Field: "ntfy_url", Message: translate(lang, "Write the address of the topic, like https://ntfy.sh/my-topic")})
		}
		if options.NtfyToken != "" && !ntfyTokenRegex.MatchString(options.NtfyToken) {
			errs = append(errs, FieldError{Field: "ntfy_token", Message: translate(lang, "Invalid token")})
		}
	}
	if (options.GotifyUrl == "") != (options.GotifyToken == "") {
		errs = append(errs, FieldError{Field: emptyOf(options.GotifyUrl, "gotify_url", "gotify_token"), Message: translate(lang, "Gotify needs both the address of the server and the token of the application")})
	} else if options.GotifyUrl != "" {
		if _, ok := gotifyMessages(options.GotifyUrl); !ok {
			errs = append(errs, FieldError{Field: "gotify_url", Message: translate(lang, "Write the address of the server, like https://gotify.example.com")})
		}
		if !gotifyTokenRegex.MatchString(options.GotifyToken) {
			errs = append(errs, FieldError{Field: "gotify_token", Message: translate(lang, "Invalid token")})
		}
	}
	if (options.PushoverUser == "") != (options.PushoverToken == "") {
		errs = append(errs, FieldError{Field: emptyOf(options.PushoverUser, "pushover_user", "pushover_token"), Message: translate(lang, "Pushover needs both the user key and the token of the application")})
	} else if options.PushoverUser != "" {
		if !pushoverKeyRegex.MatchString(options.PushoverUser) {
			errs = append(errs, FieldError{Field: "pushover_user", Message: translate(lang, "Invalid key: 30 letters and numbers")})
		}
		if !pushoverKeyRegex.MatchString(options.PushoverToken) {
			errs = append(errs, FieldError{Field: "pushover_token", Message: translate(lang, "Invalid key: 30 letters and numbers")})
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
		errs = append(errs, FieldError{Field: "smtp_host", Message: translate(lang, "Write the name of the mail server, like smtp.example.com")})
	}
	if options.SmtpPort < 0 || options.SmtpPort > 65535 {
		errs = append(errs, FieldError{Field: "smtp_port", Message: translate(lang, "Invalid port")})
	}
	security := emailSecurity(options)
	if !slices.Contains(settings.SmtpSecurityChoices, security) {
		errs = append(errs, FieldError{Field: "smtp_security", Message: translate(lang, "Unknown option")})
	}
	if (options.SmtpUser == "") != (options.SmtpPassword == "") {
		field := "smtp_password"
		if options.SmtpUser == "" {
			field = "smtp_user"
		}
		errs = append(errs, FieldError{Field: field, Message: translate(lang, "Write both the user and the password, or neither")})
	} else if options.SmtpUser != "" && security == settings.SMTP_NONE {
		errs = append(errs, FieldError{Field: "smtp_security", Message: translate(lang, "Without encryption the password would travel in clear: choose STARTTLS or SSL/TLS")})
	}
	if from := emailFrom(options); from == "" {
		errs = append(errs, FieldError{Field: "smtp_from", Message: translate(lang, "Write the address the mail comes from")})
	} else if _, err := mail.ParseAddress(from); err != nil {
		message := "Invalid email address"
		if options.SmtpFrom == "" {
			// the user is not an address: the mail needs one to come from
			message = "Write the address the mail comes from"
		}
		errs = append(errs, FieldError{Field: "smtp_from", Message: translate(lang, message)})
	}
	if _, ok := emailRecipients(options.SmtpTo); !ok {
		errs = append(errs, FieldError{Field: "smtp_to", Message: translatef(lang, "Write one or more email addresses, separated by commas (at most %v)", maxEmailRecipients)})
	}
	return errs
}

// formPort is the port written in a form: 0 when empty, -1 when it is not a number.
func formPort(value string) int {
	if value == "" {
		return 0
	}
	port, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return port
}

// formSecurity is the security chosen in a form: STARTTLS, the default, is saved as "".
func formSecurity(value string) string {
	if value == settings.SMTP_STARTTLS {
		return ""
	}
	return value
}

// draftNotifications are the notification settings with the channels written in a form that is
// not saved yet, as the wizard tests them.
func draftNotifications(r *http.Request, options settings.NotificationOptions) settings.NotificationOptions {
	value := func(name string) string {
		return strings.TrimSpace(r.FormValue(name))
	}
	options.DiscordWebhookUrl = value("discord_webhook_url")
	options.WebhookUrl = value("webhook_url")
	options.TelegramBotToken = value("telegram_bot_token")
	options.TelegramChatId = value("telegram_chat_id")
	options.NtfyUrl = value("ntfy_url")
	options.NtfyToken = value("ntfy_token")
	options.GotifyUrl = value("gotify_url")
	options.GotifyToken = value("gotify_token")
	options.PushoverUser = value("pushover_user")
	options.PushoverToken = value("pushover_token")
	options.SmtpHost = value("smtp_host")
	options.SmtpPort = formPort(value("smtp_port"))
	options.SmtpSecurity = formSecurity(value("smtp_security"))
	options.SmtpUser = value("smtp_user")
	// a password may begin or end with a space
	options.SmtpPassword = r.FormValue("smtp_password")
	options.SmtpFrom = value("smtp_from")
	options.SmtpTo = value("smtp_to")
	return options
}

// NotificationTestResponse is the outcome of a test notification, channel by channel.
type NotificationTestResponse struct {
	StrongMessage string `json:"strongMessage,omitempty"`
	Message       string `json:"message,omitempty"`
	// the channels that did not take it, each with why
	Failed []string `json:"failed,omitempty"`
	// no channel took it
	GlobalError *GlobalError `json:"globalError,omitempty"`
}

// channelNames writes a list of channels: "Telegram, ntfy and Email".
func channelNames(lang string, names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return translatef(lang, "%v and %v", strings.Join(names[:len(names)-1], ", "), names[len(names)-1])
}

func (web *Web) HandleNotifications() {
	web.router.HandleFunc("/notifications/test", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		options := settings.ReadSettings(web.dataFolder).Notifications
		// the wizard tests the channels written in it, before they are saved
		if r.FormValue("draft") == "1" {
			options = keptSecrets(draftNotifications(r, options), options)
			if errs := validateNotifications(options, lang); len(errs) > 0 {
				writeGlobalError(w, http.StatusBadRequest, lang, errs[0].Message)
				return
			}
			if !notificationsConfigured(options) {
				writeGlobalError(w, http.StatusBadRequest, lang, "Write a channel first.")
				return
			}
		}
		if !notificationsConfigured(options) {
			writeGlobalError(w, http.StatusBadRequest, lang, "No notification channel is configured. Save the settings first.")
			return
		}
		items := []NotificationItem{{Kind: "test", Name: translate(lang, "Test notification: notifications work.")}}
		title, text := notificationText(lang, items)
		// every channel says how it went: one that failed is not hidden by the others
		sent, failures, logged := []string{}, []string{}, []string{}
		for _, result := range deliver(options, title, text, items) {
			name := translate(lang, result.channel)
			if result.err == nil {
				sent = append(sent, name)
				continue
			}
			failures = append(failures, name+": "+failureText(lang, result.err))
			logged = append(logged, result.channel+": "+result.err.Error())
		}
		if len(logged) > 0 {
			web.sugarLogger.Warnf("Test notification not sent to every channel: %s", strings.Join(logged, "; "))
		}
		switch {
		case len(failures) == 0:
			writeJSON(w, http.StatusOK, NotificationTestResponse{StrongMessage: translate(lang, "Sent!"), Message: translatef(lang, "To %v. Check your notification channels.", channelNames(lang, sent))})
		case len(sent) > 0:
			writeJSON(w, http.StatusOK, NotificationTestResponse{StrongMessage: translatef(lang, "Sent to %v.", channelNames(lang, sent)), Failed: failures})
		default:
			writeJSON(w, http.StatusBadGateway, NotificationTestResponse{GlobalError: &GlobalError{StrongMessage: translate(lang, "Error!"), Message: translate(lang, "It reached no channel.")}, Failed: failures})
		}
	}).Methods("POST")
}
