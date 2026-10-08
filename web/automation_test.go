package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func newAutomationEvents() []HistoryEvent {
	return []HistoryEvent{
		{Added: true, Kind: HISTORY_GAME, Id: "0100000000010000", Name: "Known Game"},
		{Added: true, Kind: HISTORY_UPDATE, Id: "0100000000010800", Version: 65536, Name: "Known Game"},
		{Added: true, Kind: HISTORY_DLC, Id: "0100000000011001", Name: "Known DLC"},
		// removed files are not taken care of
		{Added: false, Kind: HISTORY_GAME, Id: "0100000000099000", Name: "Gone"},
	}
}

func TestAutomationIsOffByDefault(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	if settings.ReadSettings(web.dataFolder).Automation.Any() {
		t.Fatal("the automations start off")
	}
	web.queueAutomation(newAutomationEvents())
	if web.automationPending() != 0 {
		t.Fatal("nothing is queued while they are off")
	}
	// on without any step is still off
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Automation.Enabled = true })
	if settings.ReadSettings(web.dataFolder).Automation.Any() {
		t.Fatal("no step chosen")
	}
}

func TestLibraryContentPaths(t *testing.T) {
	_, localDB := testDatabases(t)
	paths := libraryContentPaths(localDB)
	for _, key := range []string{"game:0100000000010000", "update:0100000000010800:65536", "dlc:0100000000011001"} {
		if !strings.HasSuffix(paths[key], ".nsp") {
			t.Errorf("%s: %q", key, paths[key])
		}
	}
}

func TestAutomationQueueSurvivesARestart(t *testing.T) {
	saved := automationPollOverride.Load()
	automationPollOverride.Store(int64(10 * time.Millisecond))
	defer automationPollOverride.Store(saved)

	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	// wait for the background hours, which never come in this test: the files stay queued
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Automation = settings.AutomationOptions{Enabled: true, Notify: true, BackgroundHoursOnly: true}
		s.BackgroundHours = backgroundHoursNotNow(time.Now())
	})
	web.queueAutomation(newAutomationEvents())
	if pending := web.automationPending(); pending != 3 {
		t.Fatalf("three files queued: %d", pending)
	}
	// turned off: the queue stops waiting, the files are kept
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Automation.Enabled = false })
	time.Sleep(100 * time.Millisecond)

	again := &Web{dataFolder: web.dataFolder}
	if pending := again.automationPending(); pending != 3 {
		t.Fatalf("read back after a restart: %d", pending)
	}
}

// backgroundHoursNotNow are background hours that do not include the current hour.
func backgroundHoursNotNow(now time.Time) string {
	start := (now.Hour() + 2) % 24
	end := (now.Hour() + 3) % 24
	return strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

func TestAutomationMessage(t *testing.T) {
	title, text := automationMessage("en", AutomationResult{Games: []string{"Star Game"}, Updates: 2, Checked: 3, Damaged: 1, Compressed: 2, Saved: 3 << 30, Removed: 1, Moved: 4})
	for _, want := range []string{"Star Game", "2 new updates", "3 files checked, 1 damaged", "2 files compressed", "1 old updates deleted", "4 files organized"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	if title == "" {
		t.Fatal("a title")
	}
}

func TestAutomationRunsTheChosenSteps(t *testing.T) {
	saved := automationPollOverride.Load()
	automationPollOverride.Store(int64(10 * time.Millisecond))
	defer automationPollOverride.Store(saved)

	var mutex sync.Mutex
	messages := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var message map[string]any
		json.Unmarshal(body, &message)
		mutex.Lock()
		messages = append(messages, message)
		mutex.Unlock()
	}))
	defer server.Close()

	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Automation = settings.AutomationOptions{Enabled: true, Verify: true, Notify: true}
		s.Notifications.WebhookUrl = server.URL
	})
	web.queueAutomation(newAutomationEvents())

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		mutex.Lock()
		count := len(messages)
		mutex.Unlock()
		if count > 0 && web.automationPending() == 0 && !web.compressionRunning() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(messages) != 1 {
		t.Fatalf("one message: %d", len(messages))
	}
	text, _ := messages[0]["message"].(string)
	// the test files are not real games: they are checked and found damaged
	if !strings.Contains(text, "Known Game") || !strings.Contains(text, "3 files checked, 3 damaged") {
		t.Fatalf("message: %q", text)
	}
	if task, ok := web.latestTask(TASK_VERIFY); !ok || task.Trigger != TRIGGER_AUTOMATION {
		t.Fatalf("the check is a task of the automations: %+v", task)
	}
}
