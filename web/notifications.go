package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const NOTIFICATIONS_STATE_FILENAME = "notifications.json"

var (
	// redirects are not followed: a webhook answering with one could send the request
	// to an address of the local network
	notificationClient = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	discordWebhookRegex = regexp.MustCompile(`^https://(discord\.com|discordapp\.com|ptb\.discord\.com|canary\.discord\.com)/api/webhooks/[0-9]+/[A-Za-z0-9_-]+$`)
	telegramTokenRegex  = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{20,}$`)
	telegramChatRegex   = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z0-9_]{5,})$`)
	webhookRegex        = regexp.MustCompile(`^https?://[^\s]+$`)

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
		for _, title := range web.missingUpdates() {
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
	return options.DiscordWebhookUrl != "" || (options.TelegramBotToken != "" && options.TelegramChatId != "") || options.WebhookUrl != ""
}

// notifyChanges reports the updates and DLC that became available since the last check.
// The first check only records the current state, so existing gaps are not reported. It
// returns an error if the notification could not be sent.
func (web *Web) notifyChanges() error {
	settingsObj := settings.ReadSettings(web.dataFolder)
	options := settingsObj.Notifications
	// games bought since the last check leave the wishlist
	web.forgetOwnedWishes()
	if !notificationsConfigured(options) || (!options.NotifyUpdates && !options.NotifyDlc && !options.NotifyWishlist) {
		return nil
	}

	lang := settingsObj.Language
	if !isSupportedLanguage(lang) {
		lang = DEFAULT_LANGUAGE
	}

	current := web.availableItems(lang)
	notified := web.loadNotifiedKeys()

	if notified != nil {
		newItems := []NotificationItem{}
		for _, item := range current {
			if _, ok := notified[item.Key]; !ok {
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

	if err := web.saveNotifiedKeys(current); err != nil {
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
	resp, err := notificationClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		// the error may contain the URL with a secret (Discord, Telegram), do not log it
		return errors.New("request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server answered %d", resp.StatusCode)
	}
	return nil
}

// sendNotification sends the items to every configured channel.
func sendNotification(options settings.NotificationOptions, lang string, items []NotificationItem) error {
	title, text := notificationText(lang, items)
	return sendMessage(options, title, text, items)
}

// sendMessage sends a message to every configured channel; items go to the JSON webhook.
func sendMessage(options settings.NotificationOptions, title string, text string, items any) error {
	errs := []string{}

	if options.DiscordWebhookUrl != "" {
		if err := postJSON(options.DiscordWebhookUrl, map[string]string{"content": "**" + title + "**\n" + text}); err != nil {
			errs = append(errs, "Discord: "+err.Error())
		}
	}
	if options.TelegramBotToken != "" && options.TelegramChatId != "" {
		url := telegramApiUrl + "/bot" + options.TelegramBotToken + "/sendMessage"
		if err := postJSON(url, map[string]string{"chat_id": options.TelegramChatId, "text": title + "\n" + text}); err != nil {
			errs = append(errs, "Telegram: "+err.Error())
		}
	}
	if options.WebhookUrl != "" {
		if err := postJSON(options.WebhookUrl, map[string]any{"title": title, "message": text, "items": items}); err != nil {
			errs = append(errs, "Webhook: "+err.Error())
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
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
	return errs
}

func (web *Web) HandleNotifications() {
	web.router.HandleFunc("/notifications/test", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		options := settings.ReadSettings(web.dataFolder).Notifications
		if !notificationsConfigured(options) {
			writeGlobalError(w, http.StatusBadRequest, lang, "No notification channel is configured. Save the settings first.")
			return
		}
		items := []NotificationItem{{Kind: "test", Name: translate(lang, "Test notification: notifications work.")}}
		if err := sendNotification(options, lang, items); err != nil {
			writeJSON(w, http.StatusBadGateway, ErrorResponse{GlobalError: GlobalError{StrongMessage: translate(lang, "Error!"), Message: err.Error()}, FieldErrors: []FieldError{}})
			return
		}
		writeJSON(w, http.StatusOK, SuccessResponse{StrongMessage: translate(lang, "Sent!"), Message: translate(lang, "Check your notification channels.")})
	}).Methods("POST")
}
