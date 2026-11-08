package web

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// recoveryWeb is the app with an administrator, its log kept for the test.
func recoveryWeb(t *testing.T) (*Web, *observer.ObservedLogs, http.Handler) {
	t.Helper()
	web := usersWeb(t)
	if err := web.auth.users.Add("alice", "alpha-secret-11", ROLE_ADMIN); err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zap.InfoLevel)
	web.sugarLogger = zap.New(core).Sugar()
	return web, logs, web.auth.middleware(web.router)
}

var recoveryCodeInLog = regexp.MustCompile(`^RECOVERY CODE  ([0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4})   for the user "([^"]+)"$`)

// loggedRecoveryCodes are the codes written in the log, oldest first.
func loggedRecoveryCodes(logs *observer.ObservedLogs) []string {
	var codes []string
	for _, entry := range logs.All() {
		if match := recoveryCodeInLog.FindStringSubmatch(entry.Message); match != nil {
			codes = append(codes, match[1])
		}
	}
	return codes
}

// A user who forgot their password asks for a code on the login page; it goes to the log and the
// data folder, never to the page. With it the password changes, the sessions of the user end,
// the activity notes it and the file goes. The code works once.
func TestRecoveryCodeSetsANewPassword(t *testing.T) {
	web, logs, handler := recoveryWeb(t)

	signedIn := &client{t: t, handler: handler}
	signedIn.do("POST", "/login.html", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}})
	if code := signedIn.do("GET", "/index.html", nil).Code; code != http.StatusOK {
		t.Fatalf("signed in: %v", code)
	}

	anonymous := &client{t: t, handler: handler, ip: "10.1.2.3"}
	if page := anonymous.do("GET", "/login.html", nil); !strings.Contains(page.Body.String(), `href="/recover.html"`) {
		t.Fatal("the login page has no link to recover the password")
	}
	if page := anonymous.do("GET", "/recover.html", nil); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `action="/recover/ask"`) {
		t.Fatalf("the page of a forgotten password: %v", page.Code)
	}
	asked := anonymous.do("POST", "/recover/ask", url.Values{"name": {"ALICE"}})
	codes := loggedRecoveryCodes(logs)
	if asked.Code != http.StatusOK || len(codes) != 1 {
		t.Fatalf("asked: %v, codes in the log %v", asked.Code, codes)
	}
	code := codes[0]
	if strings.Contains(asked.Body.String(), code) || strings.Contains(asked.Body.String(), normalizeSetupCode(code)) {
		t.Fatal("the code is sent in the page")
	}
	if !strings.Contains(asked.Body.String(), `action="/recover/reset"`) {
		t.Fatal("the page does not ask for the code")
	}
	file, err := os.ReadFile(web.recoveryCodePath())
	if err != nil || !strings.Contains(string(file), code) || !strings.Contains(string(file), "alice") {
		t.Fatalf("the code is not in %s: %v %q", RECOVERY_CODE_FILENAME, err, file)
	}

	// written as people copy it
	written := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	reset := anonymous.do("POST", "/recover/reset", url.Values{"name": {"alice"}, "code": {written}, "password": {"juliet-secret-77"}})
	if reset.Code != http.StatusSeeOther || reset.Header().Get("Location") != "/login.html?done=recovered" {
		t.Fatalf("reset: %v %q %s", reset.Code, reset.Header().Get("Location"), reset.Body.String())
	}
	if _, ok := web.auth.verify("alice", "juliet-secret-77"); !ok {
		t.Fatal("the new password does not work")
	}
	if _, ok := web.auth.verify("alice", "alpha-secret-11"); ok {
		t.Fatal("the old password still works")
	}
	if code := signedIn.do("GET", "/index.html", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("the session of before must end: %v", code)
	}
	if _, err := os.Stat(web.recoveryCodePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s is kept after the code was used: %v", RECOVERY_CODE_FILENAME, err)
	}
	entries := web.activities().List()
	if len(entries) == 0 || entries[0].Action != activity.ACTION_USER_PASS || entries[0].User != "alice" || entries[0].Detail != recoveryActivityDetail || entries[0].IP != "10.1.2.3" {
		t.Fatalf("the activity: %+v", entries)
	}
	if done := anonymous.do("GET", "/login.html?done=recovered", nil); !strings.Contains(done.Body.String(), "The password was changed. Log in with the new one.") {
		t.Fatal("the login page does not tell the password was changed")
	}

	// once used, the code is worth nothing
	again := anonymous.do("POST", "/recover/reset", url.Values{"name": {"alice"}, "code": {code}, "password": {"kilo-secret-66"}})
	if again.Code != http.StatusUnauthorized || !strings.Contains(again.Body.String(), "The code is not right or has expired.") {
		t.Fatalf("the code used twice: %v", again.Code)
	}
	if _, ok := web.auth.verify("alice", "juliet-secret-77"); !ok {
		t.Fatal("a used code changed the password again")
	}
}

// A code lasts recoveryCodeLifetime; then it is gone, and its file too.
func TestRecoveryCodeExpires(t *testing.T) {
	web, _, _ := recoveryWeb(t)
	start := time.Now()
	code, user, err := web.auth.newRecoveryCode("alice", start)
	if err != nil || code == "" || user != "alice" {
		t.Fatalf("a code: %q %q %v", code, user, err)
	}
	if err := web.auth.takeRecoveryCode("alice", code, start.Add(recoveryCodeLifetime)); !errors.Is(err, ErrRecoveryCode) {
		t.Fatalf("an expired code: %v", err)
	}
	// nor later, even right on time again
	if err := web.auth.takeRecoveryCode("alice", code, start); !errors.Is(err, ErrRecoveryCode) {
		t.Fatalf("an expired code, again: %v", err)
	}

	// one just in time works
	code, _, _ = web.auth.newRecoveryCode("alice", start)
	if err := web.auth.takeRecoveryCode("alice", code, start.Add(recoveryCodeLifetime-time.Second)); err != nil {
		t.Fatalf("a code in time: %v", err)
	}

	// the file of a code nobody used goes when it expires, unless a newer code took its place
	code, user, _ = web.auth.newRecoveryCode("alice", start.Add(time.Hour))
	web.writeRecoveryCode(code, user, start.Add(time.Hour+recoveryCodeLifetime))
	if !web.auth.endRecoveryCode(code) {
		t.Fatal("the code did not end")
	}
	if web.auth.endRecoveryCode(code) {
		t.Fatal("a code ended twice")
	}
	web.removeRecoveryCodeFile()
	if _, err := os.Stat(web.recoveryCodePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s is kept: %v", RECOVERY_CODE_FILENAME, err)
	}
	newer, _, _ := web.auth.newRecoveryCode("alice", start.Add(2*time.Hour))
	if web.auth.endRecoveryCode(code) {
		t.Fatal("an old code ended the newer one")
	}
	if err := web.auth.takeRecoveryCode("alice", newer, start.Add(2*time.Hour)); err != nil {
		t.Fatalf("the newer code: %v", err)
	}
}

// Only one code at a time: asking again soon keeps the one there is, later a new one takes its
// place. A code is for the user it was asked for.
func TestOneRecoveryCodeAtATime(t *testing.T) {
	web, _, _ := recoveryWeb(t)
	if err := web.auth.users.Add("bob", "bravo-secret-22", ROLE_VIEWER); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	first, _, _ := web.auth.newRecoveryCode("alice", start)
	if again, _, _ := web.auth.newRecoveryCode("alice", start.Add(10*time.Second)); again != "" {
		t.Fatal("a second code within a minute")
	}
	if other, _, _ := web.auth.newRecoveryCode("bob", start.Add(20*time.Second)); other != "" {
		t.Fatal("a code for another user within a minute")
	}
	second, _, _ := web.auth.newRecoveryCode("alice", start.Add(recoveryCodeAskEvery))
	if second == "" || second == first {
		t.Fatalf("no new code after a minute: %q", second)
	}
	if err := web.auth.takeRecoveryCode("alice", first, start.Add(recoveryCodeAskEvery)); !errors.Is(err, ErrRecoveryCode) {
		t.Fatalf("the code replaced: %v", err)
	}
	if err := web.auth.takeRecoveryCode("bob", second, start.Add(recoveryCodeAskEvery)); !errors.Is(err, ErrRecoveryCode) {
		t.Fatalf("the code of alice for bob: %v", err)
	}
	if err := web.auth.takeRecoveryCode("alice", second, start.Add(recoveryCodeAskEvery)); err != nil {
		t.Fatalf("the code of now: %v", err)
	}
}

// Wrong codes block the code for everybody, as with the setup code: a client that changes its
// address is stopped too.
func TestWrongRecoveryCodesAreBlocked(t *testing.T) {
	web, logs, handler := recoveryWeb(t)
	anonymous := &client{t: t, handler: handler}
	anonymous.do("POST", "/recover/ask", url.Values{"name": {"alice"}})
	codes := loggedRecoveryCodes(logs)
	if len(codes) != 1 {
		t.Fatalf("codes in the log: %v", codes)
	}
	for i := 0; i < maxLoginFailures; i++ {
		c := &client{t: t, handler: handler, ip: "10.0.0." + strconv.Itoa(i+1)}
		c.do("POST", "/recover/reset", url.Values{"name": {"alice"}, "code": {"WRNG-WRNG-WRNG"}, "password": {"juliet-secret-77"}}, "X-Forwarded-For", "192.168.50."+strconv.Itoa(i+1))
	}
	blocked := anonymous.do("POST", "/recover/reset", url.Values{"name": {"alice"}, "code": {codes[0]}, "password": {"juliet-secret-77"}})
	if blocked.Code != http.StatusTooManyRequests || !strings.Contains(blocked.Body.String(), "Too many wrong codes.") {
		t.Fatalf("the right code while blocked: %v", blocked.Code)
	}
	if _, ok := web.auth.verify("alice", "alpha-secret-11"); !ok {
		t.Fatal("the password changed while blocked")
	}
	if logs.FilterMessage("Wrong recovery code").Len() == 0 {
		t.Fatal("the wrong codes are not noted in the log")
	}
	// the block is that of an account, never a whole day
	web.auth.limiter.mutex.Lock()
	failure := web.auth.limiter.failures[recoveryCodeKey]
	web.auth.limiter.mutex.Unlock()
	if wait := failure.blockUntil.Sub(failure.last); wait <= 0 || wait > maxAccountBlock {
		t.Fatalf("blocked for %v", wait)
	}
}

// The page answers the same whether the user exists or not; no code is made for a user that does
// not exist, nor for the one of SLM_AUTH_USERNAME, which is changed in the environment.
func TestRecoveryCodeTellsNothingOfTheUsers(t *testing.T) {
	t.Setenv("SLM_AUTH_USERNAME", "envadmin")
	t.Setenv("SLM_AUTH_PASSWORD", "echo-secret-55")
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	auth, err := newAuth(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	web.auth = auth
	web.HandleUsers()
	core, logs := observer.New(zap.InfoLevel)
	web.sugarLogger = zap.New(core).Sugar()
	if err := web.auth.users.Add("alice", "alpha-secret-11", ROLE_ADMIN); err != nil {
		t.Fatal(err)
	}
	handler := web.auth.middleware(web.router)

	anonymous := &client{t: t, handler: handler}
	ghost := anonymous.do("POST", "/recover/ask", url.Values{"name": {"ghost"}})
	env := anonymous.do("POST", "/recover/ask", url.Values{"name": {"envadmin"}})
	if codes := loggedRecoveryCodes(logs); len(codes) != 0 {
		t.Fatalf("codes for users that cannot have one: %v", codes)
	}
	if _, err := os.Stat(web.recoveryCodePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file for a user that cannot have a code")
	}
	alice := anonymous.do("POST", "/recover/ask", url.Values{"name": {"alice"}})
	sameAnswer := func(body, name string) string { return strings.ReplaceAll(body, `value="`+name+`"`, `value=""`) }
	if ghost.Code != alice.Code || env.Code != alice.Code || sameAnswer(ghost.Body.String(), "ghost") != sameAnswer(alice.Body.String(), "alice") {
		t.Fatal("the page tells whether the user exists")
	}
	if len(loggedRecoveryCodes(logs)) != 1 {
		t.Fatal("no code for a user that exists")
	}
}

// A password that cannot be set does not use up the code; without login there is nothing to
// recover.
func TestRecoveryCodeKeptForAWeakPassword(t *testing.T) {
	_, logs, handler := recoveryWeb(t)
	anonymous := &client{t: t, handler: handler}
	anonymous.do("POST", "/recover/ask", url.Values{"name": {"alice"}})
	code := loggedRecoveryCodes(logs)[0]
	weak := anonymous.do("POST", "/recover/reset", url.Values{"name": {"alice"}, "code": {code}, "password": {"short"}})
	if weak.Code != http.StatusBadRequest {
		t.Fatalf("a weak password: %v", weak.Code)
	}
	if reset := anonymous.do("POST", "/recover/reset", url.Values{"name": {"alice"}, "code": {code}, "password": {"juliet-secret-77"}}); reset.Code != http.StatusSeeOther {
		t.Fatalf("the code after a weak password: %v", reset.Code)
	}

	open, _ := setupWeb(t)
	page := (&client{t: t, handler: open.auth.middleware(open.router)}).do("GET", "/recover.html", nil)
	if page.Code != http.StatusSeeOther {
		t.Fatalf("without login: %v", page.Code)
	}
}

// A file left by a start before goes when the app starts: its code is not in memory any more.
func TestRecoveryCodeFileOfAStartBefore(t *testing.T) {
	web, _, _ := recoveryWeb(t)
	if err := os.WriteFile(web.recoveryCodePath(), []byte("RECOVERY CODE  OLD0-OLD0-OLD0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	web.removeRecoveryCodeFile()
	if _, err := os.Stat(web.recoveryCodePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file of before is kept: %v", err)
	}
	if err := web.auth.takeRecoveryCode("alice", "OLD0-OLD0-OLD0", time.Now()); !errors.Is(err, ErrRecoveryCode) {
		t.Fatalf("a code of before: %v", err)
	}
}
