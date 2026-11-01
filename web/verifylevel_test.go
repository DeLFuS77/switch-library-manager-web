package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// levelWeb is a library of one file whose check is made up: its signatures (unsigned lists the
// NCA files not signed by Nintendo) and its hashes (damage, when not nil), each counted when it
// runs.
func levelWeb(t *testing.T, unsigned []string, damage error) (web *Web, path string, signatureRuns *atomic.Int32, hashRuns *atomic.Int32) {
	t.Helper()
	web, path = compressWeb(t)
	web.HandleVerify()
	web.HandleIssues()
	web.HandleTasks()
	signatureRuns, hashRuns = &atomic.Int32{}, &atomic.Int32{}
	originalSignatures, originalHashes := checkFileSignatures, verifyLibraryFile
	t.Cleanup(func() { checkFileSignatures, verifyLibraryFile = originalSignatures, originalHashes })
	checkFileSignatures = func(string) (switchfs.SignatureCheck, error) {
		signatureRuns.Add(1)
		return switchfs.SignatureCheck{Checked: 2, Unsigned: unsigned}, nil
	}
	verifyLibraryFile = func(ctx context.Context, path string, progress func(int64, int64)) (switchfs.FileCheck, error) {
		hashRuns.Add(1)
		return switchfs.FileCheck{}, damage
	}
	return web, path, signatureRuns, hashRuns
}

func TestVerifyLevelNames(t *testing.T) {
	for value, want := range map[string]string{
		"": VERIFY_LEVEL_BOTH, "both": VERIFY_LEVEL_BOTH, "hash": VERIFY_LEVEL_HASH, "signature": VERIFY_LEVEL_SIGNATURE,
		// as NXPress named it before 1.4.0, and the scope of the pages of older versions
		"full": VERIFY_LEVEL_HASH, "signatures": VERIFY_LEVEL_SIGNATURE,
	} {
		if got, ok := verifyLevel(value); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", value, got, ok, want)
		}
	}
	if _, ok := verifyLevel("whole"); ok {
		t.Error("a level not known is refused")
	}
}

// A file whose signature alone fails is modified: no failure, no warning, in orange in Tasks and
// in Issues.
func TestASignatureAloneThatFailsIsModified(t *testing.T) {
	web, path, _, _ := levelWeb(t, []string{"a.nca"}, nil)
	if code := postForm(web, "/verify/start", url.Values{"level": {VERIFY_LEVEL_BOTH}}).Code; code != http.StatusAccepted {
		t.Fatalf("start: %d", code)
	}
	task := waitForTask(t, web, TASK_VERIFY)
	if task.Status != TASK_SUCCESS || len(task.Warnings) != 0 || task.Files != 1 || task.Damaged != 0 || task.Modified != 1 || task.Level != VERIFY_LEVEL_BOTH {
		t.Fatalf("modified is no failure: %+v", task)
	}
	if len(task.Tiles) != 1 || !task.Tiles[0].Modified || task.Tiles[0].Failed || task.Tiles[0].Note != NOTE_VERIFY_UNSIGNED {
		t.Fatalf("the cover says modified: %+v", task.Tiles)
	}
	found := false
	for _, issue := range web.getIssues() {
		if issue.File == path {
			found = issue.kind() == ISSUE_UNSIGNED && issue.Verdict() == VERIFY_MODIFIED
		}
	}
	if !found {
		t.Fatalf("Issues lists it as modified: %+v", web.getIssues())
	}
	if reason := translateIssue("es", "not signed by Nintendo: a.nca"); reason != "sin firma de Nintendo: a.nca" {
		t.Fatalf("the reason is translated: %q", reason)
	}

	page := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/tasks.html", nil)
	request.Header.Set("Accept-Language", "es")
	web.router.ServeHTTP(page, request)
	for _, want := range []string{"1 archivos comprobados, 1 modificados, 0 fallidos", "is-modified", "Modificado", "Las dos"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("Tasks shows %q", want)
		}
	}
	page = httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/issues.html", nil))
	for _, want := range []string{"Modified: not signed by Nintendo", `chip chip-warning ms-1`, `name="verify_level"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("Issues shows %q", want)
		}
	}
}

// A file whose hashes fail is failed, in red, also when its signature fails: the hashes are
// checked all the same, and Issues lists it once, as damaged.
func TestAHashThatFailsIsFailed(t *testing.T) {
	web, path, signatureRuns, hashRuns := levelWeb(t, []string{"a.nca"}, errors.New("a.nca: hash mismatch"))
	postForm(web, "/verify/start", url.Values{"level": {VERIFY_LEVEL_BOTH}})
	task := waitForTask(t, web, TASK_VERIFY)
	if signatureRuns.Load() != 1 || hashRuns.Load() != 1 {
		t.Fatalf("both checks run, the hashes after a signature that failed: %d %d", signatureRuns.Load(), hashRuns.Load())
	}
	if task.Status != TASK_WARNING || task.Damaged != 1 || task.Modified != 0 || task.Warnings[0].Text != NOTE_VERIFY_DAMAGED {
		t.Fatalf("a damaged file fails: %+v", task)
	}
	if len(task.Tiles) != 1 || !task.Tiles[0].Failed || task.Tiles[0].Modified {
		t.Fatalf("the cover says failed: %+v", task.Tiles)
	}
	kinds := []string{}
	for _, issue := range web.getIssues() {
		if issue.File == path {
			kinds = append(kinds, issue.kind())
			if issue.Verdict() != VERIFY_FAILED {
				t.Errorf("the chip of a damaged file: %q", issue.Verdict())
			}
		}
	}
	if len(kinds) != 1 || kinds[0] != ISSUE_DAMAGED {
		t.Fatalf("listed once, as damaged: %v", kinds)
	}

	page := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/tasks.html", nil)
	request.Header.Set("Accept-Language", "es")
	web.router.ServeHTTP(page, request)
	for _, want := range []string{"1 archivos comprobados, 1 fallidos", "Fallido</span>"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("Tasks shows %q", want)
		}
	}
	if strings.Contains(page.Body.String(), "modificados") {
		t.Error("a damaged file is not counted as modified")
	}
}

// Each level checks what it says: the signatures in seconds, the hashes read whole, or both.
func TestEachLevelChecksWhatItSays(t *testing.T) {
	web, path, signatureRuns, hashRuns := levelWeb(t, []string{"a.nca"}, nil)

	web.startVerificationAt(VERIFY_LEVEL_SIGNATURE, []string{path}, verifyRecheckAll, TRIGGER_MANUAL)
	task := waitForTask(t, web, TASK_VERIFY)
	if signatureRuns.Load() != 1 || hashRuns.Load() != 0 {
		t.Fatalf("the signatures only: %d %d", signatureRuns.Load(), hashRuns.Load())
	}
	if task.Level != VERIFY_LEVEL_SIGNATURE || !task.ByFiles || task.Modified != 1 || task.Total != 1 {
		t.Fatalf("a check of the signatures counts files: %+v", task)
	}
	if !web.verifications().lastRun().IsZero() {
		t.Fatal("a check of the signatures alone is not the last check of the files")
	}
	if !web.nothingToVerify([]string{path}, VERIFY_LEVEL_SIGNATURE) || web.nothingToVerify([]string{path}, VERIFY_LEVEL_HASH) || web.nothingToVerify([]string{path}, VERIFY_LEVEL_BOTH) {
		t.Fatal("the signatures are checked, the hashes not yet")
	}

	web.startVerificationAt(VERIFY_LEVEL_HASH, []string{path}, verifyRecheckAll, TRIGGER_MANUAL)
	task = waitForTask(t, web, TASK_VERIFY)
	if signatureRuns.Load() != 1 || hashRuns.Load() != 1 {
		t.Fatalf("the hashes only: %d %d", signatureRuns.Load(), hashRuns.Load())
	}
	// the hashes say nothing of the signatures: a file not signed is sound at this level
	if task.Level != VERIFY_LEVEL_HASH || task.ByFiles || task.Modified != 0 || task.Damaged != 0 || task.Tiles[0].Modified {
		t.Fatalf("a check of the hashes: %+v", task)
	}
	if web.verifications().lastRun().IsZero() {
		t.Fatal("a check of the hashes is the last check of the files")
	}

	// both: the new and changed files have both checked already
	if response := postForm(web, "/verify/start", url.Values{"level": {VERIFY_LEVEL_BOTH}}); !strings.Contains(response.Body.String(), `"nothing":true`) {
		t.Fatalf("nothing new to check at any level: %s", response.Body.String())
	}
	postForm(web, "/verify/start", url.Values{"level": {VERIFY_LEVEL_BOTH}, "all": {"true"}})
	task = waitForTask(t, web, TASK_VERIFY)
	if signatureRuns.Load() != 2 || hashRuns.Load() != 2 || task.Modified != 1 {
		t.Fatalf("both: %d %d %+v", signatureRuns.Load(), hashRuns.Load(), task)
	}

	// a file whose hashes were checked has only its signatures checked by both
	web.verifications().signatures = map[string]signatureRecord{}
	web.startVerificationAt(VERIFY_LEVEL_BOTH, []string{path}, verifyRecheckChanged, TRIGGER_MANUAL)
	task = waitForTask(t, web, TASK_VERIFY)
	if signatureRuns.Load() != 3 || hashRuns.Load() != 2 || task.Breakdown == nil || task.Breakdown.Signatures != 1 || !task.ByFiles {
		t.Fatalf("only the signatures: %d %d %+v", signatureRuns.Load(), hashRuns.Load(), task)
	}

	// a level not known is refused
	if code := postForm(web, "/verify/start", url.Values{"level": {"whole"}}).Code; code != http.StatusBadRequest {
		t.Fatalf("level not known: %d", code)
	}
}

// Headers the keys cannot read are not read again while the file stays the same; a check of the
// signatures alone leaves the file unchecked.
func TestSignaturesThatCannotBeRead(t *testing.T) {
	web, path, _, _ := levelWeb(t, nil, nil)
	runs := 0
	checkFileSignatures = func(string) (switchfs.SignatureCheck, error) {
		runs++
		return switchfs.SignatureCheck{}, errors.New("invalid NCA header")
	}
	web.startVerificationAt(VERIFY_LEVEL_SIGNATURE, []string{path}, verifyRecheckChanged, TRIGGER_MANUAL)
	task := waitForTask(t, web, TASK_VERIFY)
	if task.Files != 0 || task.Modified != 0 || len(task.Tiles) != 1 || !task.Tiles[0].Skipped || task.Tiles[0].Note != NOTE_VERIFY_NO_SIGNATURES {
		t.Fatalf("left unchecked: %+v", task)
	}
	if !web.nothingToVerify([]string{path}, VERIFY_LEVEL_SIGNATURE) || runs != 1 {
		t.Fatal("not read again while the file stays the same")
	}
	// a missing key is not the file's fault: it is checked again once the keys are there
	web.verifications().signatures = map[string]signatureRecord{}
	checkFileSignatures = func(string) (switchfs.SignatureCheck, error) {
		return switchfs.SignatureCheck{}, &switchfs.MissingKeyError{KeyName: "header_key"}
	}
	web.startVerificationAt(VERIFY_LEVEL_SIGNATURE, []string{path}, verifyRecheckChanged, TRIGGER_MANUAL)
	waitForTask(t, web, TASK_VERIFY)
	if web.nothingToVerify([]string{path}, VERIFY_LEVEL_SIGNATURE) {
		t.Fatal("a missing key leaves nothing kept")
	}
}

// The results saved by older versions still read: the checks and signatures of verify.json, the
// check of the signatures queued, and the tasks of the history.
func TestResultsOfOlderVersionsStillRead(t *testing.T) {
	folder := t.TempDir()
	damagedPath, unsignedPath := filepath.Join(folder, "Damaged.nsp"), filepath.Join(folder, "Unsigned.nsp")
	os.WriteFile(damagedPath, []byte("damaged"), 0o644)
	os.WriteFile(unsignedPath, []byte("unsigned"), 0o644)
	damagedInfo, _ := os.Stat(damagedPath)
	unsignedInfo, _ := os.Stat(unsignedPath)
	// verify.json as 1.67.0 writes it
	old := `{"last_run":"2026-10-30T10:00:00Z","records":{` +
		jsonString(damagedPath) + `:{"size":` + itoa(damagedInfo.Size()) + `,"mod_time":` + itoa(damagedInfo.ModTime().UnixNano()) + `,"ok":false,"reason":"damaged file: a.nca: hash mismatch","checked":"2026-10-30T10:00:00Z","checker":4},` +
		jsonString(unsignedPath) + `:{"size":` + itoa(unsignedInfo.Size()) + `,"mod_time":` + itoa(unsignedInfo.ModTime().UnixNano()) + `,"ok":true,"checked":"2026-10-30T10:00:00Z","modified":true,"checker":4}},` +
		`"signatures":{` + jsonString(unsignedPath) + `:{"size":` + itoa(unsignedInfo.Size()) + `,"mod_time":` + itoa(unsignedInfo.ModTime().UnixNano()) + `,"unsigned":["b.nca"],"checked":"2026-10-30T10:00:00Z"}}}`
	if err := os.WriteFile(filepath.Join(folder, VERIFY_FILENAME), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	store := loadVerifyStore(folder)
	if damaged := store.damaged(); len(damaged) != 1 || !strings.Contains(damaged[damagedPath], "hash mismatch") {
		t.Fatalf("the damaged files: %v", damaged)
	}
	if unsigned := store.unsigned(); len(unsigned[unsignedPath]) != 1 {
		t.Fatalf("the files not signed: %v", unsigned)
	}
	if record, ok := store.current(unsignedPath, unsignedInfo); !ok || !record.sound() {
		t.Fatal("a sound file stays sound")
	}
	if _, ok := store.signatureResult(unsignedPath, unsignedInfo); !ok {
		t.Fatal("its signatures stay checked")
	}

	// a check of the signatures queued by an older version
	if level := (QueuedTask{Kind: TASK_VERIFY, Signatures: true}).CheckLevel(); level != VERIFY_LEVEL_SIGNATURE {
		t.Fatalf("queued check of the signatures: %q", level)
	}
	if level := (QueuedTask{Kind: TASK_VERIFY}).CheckLevel(); level != VERIFY_LEVEL_BOTH {
		t.Fatalf("queued check: %q", level)
	}
	if level := (QueuedTask{Kind: TASK_COMPRESS, Level: "max"}).CheckLevel(); level != "" {
		t.Fatalf("a compression has no level of a check: %q", level)
	}

	// the history: a check of the signatures counted its files not signed as damaged
	history := `[{"id":2,"kind":"verify","trigger":"manual","status":"warning","started":"2026-10-30T10:00:00Z","current":3,"total":3,"message":"","files":3,"damaged":2,"warnings":[{"text":"` + NOTE_VERIFY_UNSIGNED + `","detail":"A.nsp: a.nca"},{"text":"` + NOTE_VERIFY_UNSIGNED + `","detail":"B.nsp: b.nca"}]},` +
		`{"id":1,"kind":"verify","trigger":"manual","status":"warning","started":"2026-10-29T10:00:00Z","current":9,"total":9,"message":"","files":5,"damaged":1,"warnings":[{"text":"` + NOTE_VERIFY_DAMAGED_OLD + `","detail":"C.nsp: x"}],"tiles":[{"name":"C.nsp","failed":true,"note":"` + NOTE_VERIFY_DAMAGED_OLD + `"}]}]`
	if err := os.WriteFile(filepath.Join(folder, TASKS_FILENAME), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks := loadTaskLog(folder).Snapshot()
	if tasks[0].Level != VERIFY_LEVEL_SIGNATURE || tasks[0].Modified != 2 || tasks[0].Damaged != 0 {
		t.Fatalf("an older check of the signatures: %+v", tasks[0])
	}
	if tasks[1].Level != "" || tasks[1].Damaged != 1 || tasks[1].Modified != 0 || !tasks[1].Tiles[0].Failed {
		t.Fatalf("an older check of the files: %+v", tasks[1])
	}
	if translate("es", NOTE_VERIFY_DAMAGED_OLD) == NOTE_VERIFY_DAMAGED_OLD {
		t.Fatal("the warning of older versions is still translated")
	}
}

// The activity tells the level of a check, in the language of the page.
func TestActivityTellsTheLevel(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/verify/start", strings.NewReader("level=hash&all=true"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.ParseForm()
	detail := verifyActivity(request)
	if detail != "hash + every file again" {
		t.Fatalf("detail: %q", detail)
	}
	if got := translateActivity("es", []Activity{{Detail: detail}})[0].Detail; got != "Hash + Volver a comprobar todo" {
		t.Fatalf("translated: %q", got)
	}
	old := httptest.NewRequest(http.MethodPost, "/verify/start", strings.NewReader("scope=signatures"))
	old.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	old.ParseForm()
	if got := translateActivity("es", []Activity{{Detail: verifyActivity(old)}})[0].Detail; got != "Firma" {
		t.Fatalf("the button of older pages: %q", got)
	}
}

// A file failed and a task failed are said apart where the language tells them apart; English
// says Failed for both.
func TestFailedFileAndFailedTask(t *testing.T) {
	if translate("en", "Failed (file)") != "Failed" || translate("es", "Failed (file)") != "Fallido" || translate("es", "Failed") != "Fallida" {
		t.Fatal("a failed file")
	}
}

func jsonString(text string) string {
	data, _ := json.Marshal(text)
	return string(data)
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
