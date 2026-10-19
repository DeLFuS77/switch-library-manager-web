package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A task that fails is told; one cancelled, or with the switch off, is not.
func TestFailedTasksAreNotified(t *testing.T) {
	web := newTestWeb(t)
	var mutex sync.Mutex
	messages := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mutex.Lock()
		messages = append(messages, body["title"].(string)+" | "+body["message"].(string))
		mutex.Unlock()
	}))
	t.Cleanup(server.Close)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Notifications = settings.NotificationOptions{WebhookUrl: server.URL, NotifyTaskFailures: true}
		s.Language = "es"
	})
	told := func(want int) []string {
		t.Helper()
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			mutex.Lock()
			n := len(messages)
			mutex.Unlock()
			if n >= want {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
		mutex.Lock()
		defer mutex.Unlock()
		return append([]string(nil), messages...)
	}

	id := web.taskLog().Start(TASK_COMPRESS, TRIGGER_MANUAL)
	web.taskLog().Finish(id, &TaskNote{Text: NOTE_COMPRESS_FAILED, Detail: "Game.nsp: broken"})
	if got := told(1); len(got) != 1 || !strings.Contains(got[0], "Compresión") || !strings.Contains(got[0], "Game.nsp: broken") {
		t.Fatalf("a failed compression is told: %q", got)
	}

	// a task that went well, or cancelled by the user, is not
	id = web.taskLog().Start(TASK_SCAN, TRIGGER_MANUAL)
	web.taskLog().Finish(id, nil)
	id = web.taskLog().Start(TASK_COMPRESS, TRIGGER_MANUAL)
	web.taskLog().Finish(id, &TaskNote{Text: NOTE_COMPRESS_CANCELED})
	if got := told(2); len(got) != 1 {
		t.Fatalf("only failures are told: %q", got)
	}

	// the switch off
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Notifications.NotifyTaskFailures = false })
	id = web.taskLog().Start(TASK_VERIFY, TRIGGER_MANUAL)
	web.taskLog().Finish(id, &TaskNote{Text: NOTE_SCAN_FAILED})
	if got := told(2); len(got) != 1 {
		t.Fatalf("turned off: %q", got)
	}
}
