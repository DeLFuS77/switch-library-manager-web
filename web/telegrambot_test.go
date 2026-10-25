package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func botWeb(t *testing.T) *Web {
	t.Helper()
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	web.state.set(switchDB, localDB)
	return web
}

// The bot answers whether a game is in the library, what is new, the space and the tasks, in
// English and in Spanish, with the words of both.
func TestTelegramAnswers(t *testing.T) {
	web := botWeb(t)
	for _, c := range []struct {
		text, lang, want string
	}{
		{"/have known", "en", "Known Game"},
		{"/have@SlmBot KNOWN", "en", "Yes, 1 in your library"},
		{"¿tengo known?", "es", "Sí, 1 en tu biblioteca"},
		{"/have nothing like it", "en", "No game of your library is called nothing like it."},
		{"/have", "en", "Write the name of a game"},
		{"/new", "en", "Updates waiting: 1"},
		{"/space", "en", "Your library takes"},
		{"estado", "es", "No hay ninguna tarea en marcha."},
		{"/help", "es", "/tengo zelda"},
		{"hello", "en", "I do not know that."},
	} {
		if got := web.answerTelegram(c.text, c.lang); !strings.Contains(got, c.want) {
			t.Errorf("%q (%s): %q, want %q", c.text, c.lang, got, c.want)
		}
	}
	if got := web.answerTelegram("/have known", "en"); !strings.Contains(got, "(US)") || !strings.Contains(got, "v65536") || !strings.Contains(got, "1 DLC") {
		t.Errorf("the region, the update and the DLC of the game: %q", got)
	}
}

// Only the chat of the notifications gets answers, and not to the messages from before the
// app started; the offset goes past every message read.
func TestTelegramAnswersItsChatOnly(t *testing.T) {
	web := botWeb(t)
	now := time.Now().Unix()
	var mutex sync.Mutex
	sent := []map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			if r.URL.Query().Get("offset") != "7" {
				t.Errorf("offset: %s", r.URL.Query().Get("offset"))
			}
			fmt.Fprintf(w, `{"ok":true,"result":[
				{"update_id":7,"message":{"date":%d,"text":"/have known","chat":{"id":42}}},
				{"update_id":8,"message":{"date":%d,"text":"/space","chat":{"id":99}}},
				{"update_id":9,"message":{"date":%d,"text":"/status","chat":{"id":42}}},
				{"update_id":10,"channel_post":{"date":%d,"text":"/help","chat":{"id":-100,"username":"MyChannel"}}}
			]}`, now, now, now-3600, now)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			mutex.Lock()
			sent = append(sent, body)
			mutex.Unlock()
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(server.Close)
	saved := telegramApiUrl
	telegramApiUrl = server.URL
	t.Cleanup(func() { telegramApiUrl = saved })

	options := settings.NotificationOptions{TelegramBotToken: "123456:abcdefghijklmnopqrstuvwx", TelegramChatId: "42", TelegramCommands: true}
	offset, err := web.pollTelegram(options, 7, time.Now())
	if err != nil || offset != 11 {
		t.Fatalf("offset %d, %v", offset, err)
	}
	if len(sent) != 1 || sent[0]["chat_id"] != "42" || !strings.Contains(sent[0]["text"], "Known Game") {
		t.Fatalf("one answer, to the chat of the notifications: %v", sent)
	}

	// a channel, by its name
	sent = sent[:0]
	options.TelegramChatId = "@mychannel"
	if _, err := web.pollTelegram(options, 7, time.Now()); err != nil || len(sent) != 1 || !strings.Contains(sent[0]["text"], "/have zelda") {
		t.Fatalf("the channel gets its answer: %v %v", sent, err)
	}
}

// Telegram refusing the request (another program reads the messages of the bot) is an error
// without the token.
func TestTelegramRefused(t *testing.T) {
	web := botWeb(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"ok":false,"description":"Conflict: terminated by other getUpdates request"}`))
	}))
	t.Cleanup(server.Close)
	saved := telegramApiUrl
	telegramApiUrl = server.URL
	t.Cleanup(func() { telegramApiUrl = saved })
	options := settings.NotificationOptions{TelegramBotToken: "123456:abcdefghijklmnopqrstuvwx", TelegramChatId: "42"}
	offset, err := web.pollTelegram(options, 3, time.Now())
	if err == nil || offset != 3 || strings.Contains(err.Error(), options.TelegramBotToken) || !strings.Contains(err.Error(), "Conflict") {
		t.Fatalf("refused: %d %v", offset, err)
	}
}
