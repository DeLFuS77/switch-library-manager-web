package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/notify"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const NOTIFICATIONS_STATE_FILENAME = "notifications.json"

// cheatsRecordedKey tells in the state that the cheats of the library were looked at once.
const cheatsRecordedKey = "cheats:recorded"

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
	return durable.WriteFile(web.notificationStatePath(), data)
}

// notifyChanges reports the updates and DLC that became available since the last check.
// The first check only records the current state, so existing gaps are not reported. It
// returns an error if the notification could not be sent.
func (web *Web) notifyChanges() error {
	settingsObj := settings.ReadSettings(web.dataFolder)
	options := settingsObj.Notifications
	// games bought since the last check leave the wishlist
	web.forgetOwnedWishes()
	if !notify.Configured(options) || (!options.NotifyUpdates && !options.NotifyDlc && !options.NotifyWishlist && !options.NotifyCheats) {
		return nil
	}

	lang := settingsObj.Language
	if !i18n.IsSupportedLanguage(lang) {
		lang = i18n.DEFAULT_LANGUAGE
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
	if !notify.Configured(options) || !options.NotifyNewGames {
		return
	}
	lang := settingsObj.Language
	if !i18n.IsSupportedLanguage(lang) {
		lang = i18n.DEFAULT_LANGUAGE
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
	title := i18n.Translatef(lang, "Switch Library Manager: %v new item(s)", len(items))
	lines := []string{}
	for i, item := range items {
		if i == maxNotificationLines {
			lines = append(lines, i18n.Translatef(lang, "and %v more", len(items)-maxNotificationLines))
			break
		}
		switch item.Kind {
		case "update":
			lines = append(lines, "• "+i18n.Translatef(lang, "Update %v for %v", item.Detail, item.Name))
		case "dlc":
			lines = append(lines, "• "+i18n.Translatef(lang, "DLC %v for %v", item.Detail, item.Name))
		case "wish":
			lines = append(lines, "• "+i18n.Translatef(lang, "On your wishlist and available: %v", item.Name))
		case "wishdlc":
			lines = append(lines, "• "+i18n.Translatef(lang, "DLC %v for %v (on your wishlist)", item.Detail, item.Name))
		case "new":
			lines = append(lines, "• "+i18n.Translatef(lang, "New in your library: %v", item.Name))
		case "newdlc":
			lines = append(lines, "• "+i18n.Translatef(lang, "New DLC in your library: %v", item.Name))
		case "cheats":
			lines = append(lines, "• "+i18n.Translatef(lang, "Cheats for %v (%v, the version you have)", item.Name, item.Detail))
		default:
			lines = append(lines, "• "+item.Name)
		}
	}
	return title, strings.Join(lines, "\n")
}

// sendNotification sends the items to every configured channel.
func sendNotification(options settings.NotificationOptions, lang string, items []NotificationItem) error {
	title, text := notificationText(lang, items)
	return notify.Send(options, title, text, items)
}

// validateNotifications checks the notification settings of the settings form.
func validateNotifications(options settings.NotificationOptions, lang string) []FieldError {
	errs := []FieldError{}
	for _, err := range notify.Validate(options, lang) {
		errs = append(errs, FieldError(err))
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
	return i18n.Translatef(lang, "%v and %v", strings.Join(names[:len(names)-1], ", "), names[len(names)-1])
}

func (web *Web) HandleNotifications() {
	web.router.HandleFunc("/notifications/test", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		options := settings.ReadSettings(web.dataFolder).Notifications
		// the wizard tests the channels written in it, before they are saved
		if r.FormValue("draft") == "1" {
			options = notify.KeptSecrets(draftNotifications(r, options), options)
			if errs := validateNotifications(options, lang); len(errs) > 0 {
				writeGlobalError(w, http.StatusBadRequest, lang, errs[0].Message)
				return
			}
			if !notify.Configured(options) {
				writeGlobalError(w, http.StatusBadRequest, lang, "Write a channel first.")
				return
			}
		}
		if !notify.Configured(options) {
			writeGlobalError(w, http.StatusBadRequest, lang, "No notification channel is configured. Save the settings first.")
			return
		}
		items := []NotificationItem{{Kind: "test", Name: i18n.Translate(lang, "Test notification: notifications work.")}}
		title, text := notificationText(lang, items)
		// every channel says how it went: one that failed is not hidden by the others
		sent, failures, logged := []string{}, []string{}, []string{}
		for _, result := range notify.Deliver(options, title, text, items) {
			name := i18n.Translate(lang, result.Channel)
			if result.Err == nil {
				sent = append(sent, name)
				continue
			}
			failures = append(failures, name+": "+notify.FailureText(lang, result.Err))
			logged = append(logged, result.Channel+": "+result.Err.Error())
		}
		if len(logged) > 0 {
			web.sugarLogger.Warnf("Test notification not sent to every channel: %s", strings.Join(logged, "; "))
		}
		switch {
		case len(failures) == 0:
			writeJSON(w, http.StatusOK, NotificationTestResponse{StrongMessage: i18n.Translate(lang, "Sent!"), Message: i18n.Translatef(lang, "To %v. Check your notification channels.", channelNames(lang, sent))})
		case len(sent) > 0:
			writeJSON(w, http.StatusOK, NotificationTestResponse{StrongMessage: i18n.Translatef(lang, "Sent to %v.", channelNames(lang, sent)), Failed: failures})
		default:
			writeJSON(w, http.StatusBadGateway, NotificationTestResponse{GlobalError: &GlobalError{StrongMessage: i18n.Translate(lang, "Error!"), Message: i18n.Translate(lang, "It reached no channel.")}, Failed: failures})
		}
	}).Methods("POST")
}
