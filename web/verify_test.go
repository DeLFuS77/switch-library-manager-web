package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func waitForTask(t *testing.T, web *Web, kind string) Task {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for web.compressionRunning() {
		if time.Now().After(deadline) {
			t.Fatal("the task did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, task := range web.taskLog().Snapshot() {
		if task.Kind == kind {
			return task
		}
	}
	t.Fatalf("no %s task", kind)
	return Task{}
}

func TestVerifyFindsDamagedFiles(t *testing.T) {
	web, path := compressWeb(t)
	web.HandleVerify()

	if code := postForm(web, "/verify/start", url.Values{}).Code; code != http.StatusAccepted {
		t.Fatalf("start: %v", code)
	}
	task := waitForTask(t, web, TASK_VERIFY)
	if task.Status != TASK_SUCCESS || task.Files != 1 || task.Damaged != 0 {
		t.Fatalf("a good file: %+v", task)
	}

	// unchanged files are not checked again
	postForm(web, "/verify/start", url.Values{})
	if task := waitForTask(t, web, TASK_VERIFY); task.Files != 0 {
		t.Fatalf("an unchanged file must be skipped: %+v", task)
	}

	data, _ := os.ReadFile(path)
	data[len(data)-50] ^= 0xFF
	os.WriteFile(path, data, 0o644)
	postForm(web, "/verify/start", url.Values{})
	task = waitForTask(t, web, TASK_VERIFY)
	if task.Status != TASK_WARNING || task.Damaged != 1 || task.Warnings[0].Text != NOTE_VERIFY_DAMAGED {
		t.Fatalf("a damaged file: %+v", task)
	}
	issues := web.getIssues()
	found := false
	for _, issue := range issues {
		if issue.File == path && strings.HasPrefix(issue.Reason, "damaged file") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the damaged file must be listed in Issues: %+v", issues)
	}
	if icon := issueIcon("damaged file: x"); !strings.Contains(icon, "bi-heartbreak") {
		t.Fatalf("icon: %s", icon)
	}
	if translated := translateIssue("es", "damaged file: x"); translated != "archivo dañado: x" {
		t.Fatalf("translation: %s", translated)
	}

	// the results are kept
	if reloaded := loadVerifyStore(web.dataFolder); len(reloaded.damaged()) != 1 || reloaded.lastRun().IsZero() {
		t.Fatal("the results must be saved")
	}
}

func TestVerifyDue(t *testing.T) {
	now := time.Now()
	if verifyDue(&settings.AppSettings{}, time.Time{}, now) {
		t.Fatal("disabled by default")
	}
	weekly := &settings.AppSettings{VerifyIntervalDays: 7}
	if !verifyDue(weekly, time.Time{}, now) || verifyDue(weekly, now.Add(-24*time.Hour), now) || !verifyDue(weekly, now.Add(-8*24*time.Hour), now) {
		t.Fatal("weekly schedule")
	}
}

func TestVerifyWorkers(t *testing.T) {
	for _, c := range []struct {
		speed string
		cores int
		want  int
	}{
		{VERIFY_SPEED_LOW, 16, 1}, {"", 1, 1}, {"", 4, 2}, {VERIFY_SPEED_NORMAL, 16, 4},
		{VERIFY_SPEED_FAST, 1, 1}, {VERIFY_SPEED_FAST, 4, 3}, {VERIFY_SPEED_FAST, 32, 8},
	} {
		if got := verifyWorkers(c.speed, c.cores); got != c.want {
			t.Errorf("%q with %d cores: %d files at a time, want %d", c.speed, c.cores, got, c.want)
		}
	}
}

func TestScheduledVerificationChecksOldResultsAgain(t *testing.T) {
	web, path := compressWeb(t)
	info, _ := os.Stat(path)
	// checked 40 days ago and fine
	web.verifications().set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checked: time.Now().AddDate(0, 0, -40)})

	web.startVerificationOf([]string{path}, verifyRecheckChanged, TRIGGER_MANUAL)
	if task := waitForTask(t, web, TASK_VERIFY); task.Files != 0 {
		t.Fatalf("an unchanged file is skipped by a manual check of the changes: %+v", task)
	}
	web.startVerificationOf([]string{path}, 30*24*time.Hour, TRIGGER_MANUAL)
	if task := waitForTask(t, web, TASK_VERIFY); task.Files != 1 {
		t.Fatalf("a result older than the interval is checked again: %+v", task)
	}
	web.startVerificationOf([]string{path}, 30*24*time.Hour, TRIGGER_MANUAL)
	if task := waitForTask(t, web, TASK_VERIFY); task.Files != 0 {
		t.Fatalf("a recent result is kept: %+v", task)
	}
}

func TestVerificationSpeedAndTimeLeft(t *testing.T) {
	now := time.Now()
	task := Task{Kind: TASK_VERIFY, Started: now.Add(-100 * time.Second), Current: 10000, Total: 70000}
	if task.speedAt(now) != 100 {
		t.Fatalf("10000 MB in 100 s: %d MB/s", task.speedAt(now))
	}
	if task.remainingAt(now) != 10*time.Minute {
		t.Fatalf("60000 MB left at 100 MB/s: %v", task.remainingAt(now))
	}
	if (Task{Kind: TASK_SCAN, Started: task.Started, Current: 5, Total: 10}).speedAt(now) != 0 {
		t.Fatal("only verifications show a speed")
	}
}

func TestOldDamageIsCheckedAgain(t *testing.T) {
	web := newTestWeb(t)
	path := filepath.Join(t.TempDir(), "Game.nsp")
	os.WriteFile(path, []byte("x"), 0644)
	info, _ := os.Stat(path)
	store := web.verifications()
	store.set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false, Reason: "damaged file: old check"})
	if _, ok := store.current(path, info); ok || len(store.damaged()) != 0 {
		t.Fatal("a damage found by the old check is checked again, and not shown meanwhile")
	}
	store.set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false, Checker: verifyChecker})
	if _, ok := store.current(path, info); !ok || len(store.damaged()) != 1 {
		t.Fatal("a damage found by the current check stays")
	}
}

// A rewritten NCA found intact by version 2 was checked without the hashes of its sections
// past 4 GB: it is checked again, and does not count as a sound copy until then. Files that
// matched their content IDs keep their result.
func TestModifiedFilesOfAnOlderCheckAreCheckedAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Game [0100000000010000][v0].nsp")
	os.WriteFile(path, []byte("game"), 0o644)
	info, _ := os.Stat(path)
	store := &verifyStore{records: map[string]verifyRecord{}}
	base := verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checker: 2}

	store.set(path, base)
	if record, ok := store.current(path, info); !ok || !record.sound() {
		t.Fatal("a file that matched its content IDs keeps its result")
	}
	modified := base
	modified.Modified = true
	store.set(path, modified)
	if _, ok := store.current(path, info); ok {
		t.Fatal("a rewritten NCA of an older check is checked again")
	}
	modified.Checker = verifyChecker
	store.set(path, modified)
	if record, ok := store.current(path, info); !ok || !record.sound() {
		t.Fatal("checked by this version, it stands")
	}
}

// The store tells why a file is checked: never checked, changed since, or checked once more
// after an improvement of the check.
func TestWhyAFileIsChecked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Game [0100000000010000][v0].nsp")
	os.WriteFile(path, []byte("game"), 0o644)
	info, _ := os.Stat(path)
	store := &verifyStore{records: map[string]verifyRecord{}}
	if why := store.why(path, info); why != VERIFY_WHY_NEW {
		t.Fatalf("never checked: %s", why)
	}
	store.set(path, verifyRecord{Size: info.Size() + 1, ModTime: info.ModTime().UnixNano(), OK: true, Checker: verifyChecker})
	if why := store.why(path, info); why != VERIFY_WHY_CHANGED {
		t.Fatalf("changed: %s", why)
	}
	store.set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Modified: true, Checker: 2})
	if why := store.why(path, info); why != VERIFY_WHY_IMPROVED {
		t.Fatalf("modified, checked by an older version: %s", why)
	}
}
