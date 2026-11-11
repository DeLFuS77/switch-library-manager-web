package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const testQueue = `{"paused":false,"jobs":[
	{"id":3,"game":"Game","label":"Update v65536","state":"running","stage":"write","percent":42.5,"speed":31.2,"left":95,"size":1000},
	{"id":4,"game":"Other","state":"waiting"},
	{"id":5,"game":"Odd","state":"flying"}]}`

// The console says what its queue does; its user sees it on the web, and what they ask (pause, a
// cancel of a job) reaches the console the next time it says what it does, once.
func TestNetQueueSeenAndAskedFromTheWeb(t *testing.T) {
	_, _, bob, console, auth, keyId, _ := pushWeb(t)
	sent := sendJSON(console, "POST", "/install/queue", testQueue, "Authorization", auth)
	if sent.Code != http.StatusOK || !strings.Contains(sent.Body.String(), `"commands":[]`) {
		t.Fatalf("the queue: %d %s", sent.Code, sent.Body.String())
	}
	got := bob.do("GET", "/api/install/queue", nil)
	var answer struct {
		Queues []netQueueLive `json:"queues"`
	}
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &answer) != nil || len(answer.Queues) != 1 {
		t.Fatalf("read: %d %s", got.Code, got.Body.String())
	}
	queue := answer.Queues[0]
	if queue.Id != keyId || queue.Gone || len(queue.Jobs) != 2 || queue.Jobs[0].Stage != "write" || queue.Jobs[0].Percent != 42.5 {
		t.Errorf("the queue seen: %+v", queue)
	}
	// asked from the web
	for _, body := range []string{`{"action":"pause"}`, `{"action":"cancel","job":3}`} {
		if asked := sendJSON(bob, "POST", "/api/install/queue/"+keyId, body); asked.Code != http.StatusAccepted {
			t.Errorf("%s: %d %s", body, asked.Code, asked.Body.String())
		}
	}
	for _, body := range []string{`{"action":"explode"}`, `{"action":"cancel"}`, `not json`} {
		if asked := sendJSON(bob, "POST", "/api/install/queue/"+keyId, body); asked.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, asked.Code)
		}
	}
	if asked := sendJSON(bob, "POST", "/api/install/queue/0123456789abcdef", `{"action":"pause"}`); asked.Code != http.StatusNotFound {
		t.Errorf("a console of nobody: %d", asked.Code)
	}
	sent = sendJSON(console, "POST", "/install/queue", testQueue, "Authorization", auth)
	var commands struct {
		Commands []netQueueCommand `json:"commands"`
	}
	if json.Unmarshal(sent.Body.Bytes(), &commands) != nil || len(commands.Commands) != 2 || commands.Commands[0].Action != "pause" ||
		commands.Commands[1].Action != "cancel" || commands.Commands[1].Job != 3 || commands.Commands[1].Game != "Game" || commands.Commands[1].Id == "" {
		t.Fatalf("the commands: %s", sent.Body.String())
	}
	sent = sendJSON(console, "POST", "/install/queue", testQueue, "Authorization", auth)
	if !strings.Contains(sent.Body.String(), `"commands":[]`) {
		t.Errorf("taken twice: %s", sent.Body.String())
	}
	// only a console posts its queue
	if bad := sendJSON(bob, "POST", "/install/queue", testQueue); bad.Code == http.StatusOK {
		t.Errorf("a user posted a queue: %d", bad.Code)
	}
}

// The end of a queue: without channels nothing is sent; the message says what went well and
// what failed, the failed first.
func TestNetQueueDoneMessage(t *testing.T) {
	_, _, _, console, auth, _, _ := pushWeb(t)
	done := sendJSON(console, "POST", "/install/queue/done", `{"items":[{"game":"Game","label":"v1","ok":true}]}`, "Authorization", auth)
	if done.Code != http.StatusOK || !strings.Contains(done.Body.String(), `"sent":false`) {
		t.Errorf("done without channels: %d %s", done.Code, done.Body.String())
	}
	var body netQueueDone
	json.Unmarshal([]byte(`{"items":[{"game":"A","ok":true},{"game":"B","label":"Update v2","ok":false,"error":"No room"}]}`), &body)
	title, text, items := netQueueDoneText("en", "Living room", body)
	if title != "NXDock finished installing on Living room" || len(items) != 2 ||
		text != "1 installed, 1 failed\n✗ B · Update v2: No room\n✓ A" {
		t.Errorf("the message: %q %q", title, text)
	}
	title, _, _ = netQueueDoneText("es", "Salón", body)
	if title != "NXDock terminó de instalar en Salón" {
		t.Errorf("in Spanish: %q", title)
	}
}
