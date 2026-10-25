package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The bot of Telegram set for the notifications can also answer, when Settings say so: written
// to in the chat of the notifications, it tells whether a game is in the library, what is new,
// the space and the tasks, and starts a synchronization. Only that chat gets answers. The app
// asks Telegram for the messages (long polling): nothing has to reach it from outside.

const (
	// how long Telegram holds a request for messages when there is none
	telegramPollSeconds = 25
	// the games of an answer to "have", and of what was added
	telegramListed = 5
)

var (
	telegramPollClient = &http.Client{Timeout: (telegramPollSeconds + 15) * time.Second}
	// the wait while the bot is off or Telegram does not answer; replaced in tests
	telegramIdleWait = 30 * time.Second
)

type telegramUpdate struct {
	UpdateId    int64            `json:"update_id"`
	Message     *telegramMessage `json:"message"`
	ChannelPost *telegramMessage `json:"channel_post"`
}

type telegramMessage struct {
	Date int64  `json:"date"`
	Text string `json:"text"`
	Chat struct {
		Id       int64  `json:"id"`
		Username string `json:"username"`
	} `json:"chat"`
}

// startTelegramBot asks Telegram for the messages of the bot while it is set to answer.
func (web *Web) startTelegramBot() {
	go func() {
		started := time.Now()
		var offset int64
		token := ""
		for {
			options := settings.ReadSettings(web.dataFolder).Notifications
			if !options.TelegramCommands || options.TelegramBotToken == "" || options.TelegramChatId == "" {
				time.Sleep(telegramIdleWait)
				continue
			}
			if options.TelegramBotToken != token {
				token, offset = options.TelegramBotToken, 0
				web.setTelegramCommands(options)
			}
			next, err := web.pollTelegram(options, offset, started)
			if err != nil {
				// never the URL in the log: it holds the token
				web.sugarLogger.Debugf("Telegram: %v", err)
				time.Sleep(telegramIdleWait)
				continue
			}
			offset = next
		}
	}()
}

// pollTelegram answers the messages that came to the chat since offset and gives the offset of
// the next ones. Messages from before since (the start of the app) are not answered.
func (web *Web) pollTelegram(options settings.NotificationOptions, offset int64, since time.Time) (int64, error) {
	query := url.Values{"timeout": {fmt.Sprint(telegramPollSeconds)}, "offset": {fmt.Sprint(offset)}, "allowed_updates": {`["message","channel_post"]`}}
	response, err := telegramPollClient.Get(telegramApiUrl + "/bot" + options.TelegramBotToken + "/getUpdates?" + query.Encode())
	if err != nil {
		return offset, errors.New("Telegram does not answer")
	}
	defer response.Body.Close()
	var body struct {
		Ok          bool             `json:"ok"`
		Result      []telegramUpdate `json:"result"`
		Description string           `json:"description"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || !body.Ok {
		return offset, fmt.Errorf("the messages could not be read: %s (%d)", body.Description, response.StatusCode)
	}
	lang := web.botLanguage()
	for _, update := range body.Result {
		offset = max(offset, update.UpdateId+1)
		message := update.Message
		if message == nil {
			message = update.ChannelPost
		}
		if message == nil || !telegramSameChat(message, options.TelegramChatId) || time.Unix(message.Date, 0).Before(since.Add(-time.Minute)) {
			continue
		}
		answer := web.answerTelegram(message.Text, lang)
		if answer == "" {
			continue
		}
		if err := postJSON(telegramApiUrl+"/bot"+options.TelegramBotToken+"/sendMessage", map[string]string{"chat_id": options.TelegramChatId, "text": fitMessage(answer, telegramMessageLimit)}); err != nil {
			web.sugarLogger.Debugf("Telegram: the answer could not be sent: %v", err)
		}
	}
	return offset, nil
}

// telegramSameChat tells a message of the chat of the notifications: its number, or the name
// of a channel (@name).
func telegramSameChat(message *telegramMessage, chat string) bool {
	if name, ok := strings.CutPrefix(chat, "@"); ok {
		return strings.EqualFold(message.Chat.Username, name)
	}
	return fmt.Sprint(message.Chat.Id) == chat
}

// setTelegramCommands gives the bot its menu of commands, in the language of the app.
func (web *Web) setTelegramCommands(options settings.NotificationOptions) {
	lang := web.botLanguage()
	commands := []map[string]string{}
	for _, command := range botCommands {
		commands = append(commands, map[string]string{"command": botCommandName(command.name, command.spanish, lang), "description": translate(lang, command.help)})
	}
	if err := postJSON(telegramApiUrl+"/bot"+options.TelegramBotToken+"/setMyCommands", map[string]any{"commands": commands}); err != nil {
		web.sugarLogger.Debugf("Telegram: the commands could not be set: %v", err)
	}
}

// botLanguage is the language of the answers: the one of the app, else English.
func (web *Web) botLanguage() string {
	lang := settings.ReadSettings(web.dataFolder).Language
	if !isSupportedLanguage(lang) {
		return DEFAULT_LANGUAGE
	}
	return lang
}

// botCommands are what the bot answers, with the words that ask for each: in English, and in
// Spanish, which its menu shows in Spanish ("¿tengo zelda?" asks as "/have zelda" does).
var botCommands = []struct {
	name    string
	spanish string
	words   []string
	help    string
}{
	{"have", "tengo", []string{"have"}, "Tells whether a game is in the library"},
	{"new", "nuevo", []string{"novedades"}, "What was added this week, and the updates and DLC waiting"},
	{"space", "espacio", nil, "The size of the library and the free space"},
	{"status", "estado", []string{"tareas"}, "The tasks at work"},
	{"sync", "sincroniza", []string{"sincronizar"}, "Starts a synchronization"},
	{"help", "ayuda", []string{"start"}, "What the bot answers"},
}

// botCommandName is the name of a command in the menu of the bot, for lang.
func botCommandName(name string, spanish string, lang string) string {
	if lang == "es" {
		return spanish
	}
	return name
}

// answerTelegram is the answer to a message of the chat, in lang.
func (web *Web) answerTelegram(text string, lang string) string {
	text = strings.Trim(strings.TrimSpace(text), "¿?¡!.")
	word, rest, _ := strings.Cut(text, " ")
	word = strings.ToLower(strings.TrimPrefix(word, "/"))
	// "/have@NameOfTheBot" in a group
	word, _, _ = strings.Cut(word, "@")
	command := ""
	for _, candidate := range botCommands {
		if word == candidate.name || word == candidate.spanish || hasValue(candidate.words, word) {
			command = candidate.name
		}
	}
	switch command {
	case "have":
		return web.botHave(strings.TrimSpace(rest), lang)
	case "new":
		return web.botNew(lang)
	case "space":
		return web.botSpace(lang)
	case "status":
		return web.botStatus(lang)
	case "sync":
		return web.botSync(lang)
	case "help":
		return botHelp(lang)
	}
	return translate(lang, "I do not know that. /help tells what I answer.")
}

func botHelp(lang string) string {
	lines := []string{translate(lang, "I answer about your library:")}
	for _, command := range botCommands {
		example := "/" + botCommandName(command.name, command.spanish, lang)
		if command.name == "have" {
			example += " zelda"
		}
		lines = append(lines, example+" · "+translate(lang, command.help))
	}
	return strings.Join(lines, "\n")
}

// botHave tells the games of the library whose name has the words asked, a few of them.
func (web *Web) botHave(query string, lang string) string {
	if query == "" {
		return translate(lang, "Write the name of a game after it: /have zelda")
	}
	filter := &TitleItemFilter{Keyword: query, WithMissing: "0"}
	filter.Normalize()
	items, p, _ := web.getLibraryWithFacets(filter, lang)
	if len(items) == 0 {
		return translatef(lang, "No game of your library is called %v.", query)
	}
	lines := []string{translatef(lang, "Yes, %v in your library:", p.NumItems)}
	for i, item := range items {
		if i == telegramListed {
			lines = append(lines, translatef(lang, "and %v more", p.NumItems-telegramListed))
			break
		}
		line := "• " + item.Name
		if regions := append([]string{regionCode(item.Region)}, item.OtherFlags...); regions[0] != "" {
			line += " (" + strings.Join(regions, ", ") + ")"
		}
		if item.LocalUpdate > 0 {
			line += fmt.Sprintf(" · v%d", item.LocalUpdate)
		}
		if item.DlcOwned > 0 {
			line += fmt.Sprintf(" · %d DLC", item.DlcOwned)
		}
		if item.UpdateAvailable {
			line += " · " + translate(lang, "Update available")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// botNew tells the games added in the last week, and the updates and DLC waiting.
func (web *Web) botNew(lang string) string {
	filter := &TitleItemFilter{WithMissing: "0", SortBy: "added", SortOrder: "desc", PerPage: 96}
	filter.Normalize()
	items, _, _ := web.getLibraryWithFacets(filter, lang)
	since := time.Now().AddDate(0, 0, -7)
	added := []string{}
	for _, item := range items {
		if item.Added.After(since) {
			added = append(added, item.Name)
		}
	}
	lines := []string{}
	switch {
	case len(added) == 0:
		lines = append(lines, translate(lang, "No game was added this week."))
	case len(added) > telegramListed:
		lines = append(lines, translatef(lang, "Added this week: %v", strings.Join(added[:telegramListed], ", "))+" "+translatef(lang, "and %v more", len(added)-telegramListed))
	default:
		lines = append(lines, translatef(lang, "Added this week: %v", strings.Join(added, ", ")))
	}
	counts := web.navCounts()
	lines = append(lines, translatef(lang, "Updates waiting: %v", counts.Updates), translatef(lang, "DLC you do not have: %v", counts.Dlc))
	return strings.Join(lines, "\n")
}

// botSpace tells the size of the library and the free space of its folder.
func (web *Web) botSpace(lang string) string {
	stats := web.getStatistics(lang)
	text := translatef(lang, "Your library takes %v.", formatSize(stats.TotalSize))
	if free, err := diskFree(settings.ReadSettings(web.dataFolder).Folder); err == nil {
		text += "\n" + translatef(lang, "Free in its folder: %v.", formatSize(int64(free)))
	}
	return text
}

// botStatus tells the tasks at work, with how far each one is.
func (web *Web) botStatus(lang string) string {
	lines := []string{}
	for _, task := range web.taskLog().Snapshot() {
		if !task.Running() {
			continue
		}
		name := task.Kind
		if label, ok := taskKindNames[task.Kind]; ok {
			name = translate(lang, label)
		}
		if task.Total > 0 && task.Current >= 0 {
			name += fmt.Sprintf(" · %d %%", min(task.Current*100/task.Total, 100))
		}
		lines = append(lines, "• "+name)
	}
	if len(lines) == 0 {
		return translate(lang, "No task is at work.")
	}
	return translate(lang, "At work:") + "\n" + strings.Join(lines, "\n")
}

// botSync starts a synchronization, as the button of the app does.
func (web *Web) botSync(lang string) string {
	if web.state.IsSynchronizing() {
		return translate(lang, "A synchronization is already running.")
	}
	if !web.Synchronize(TRIGGER_MANUAL) {
		return translate(lang, "The synchronization could not start now: a task works on the files. Try again later.")
	}
	return translate(lang, "Synchronization started.")
}
