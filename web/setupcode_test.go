package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// The code is easy to copy: three groups of four digits and capitals that cannot be taken for
// one another, new each time, and read however it was written.
func TestSetupCodeLooksRight(t *testing.T) {
	shape := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		code, err := newSetupCode()
		if err != nil {
			t.Fatal(err)
		}
		if !shape.MatchString(code) {
			t.Fatalf("a code to copy: %q", code)
		}
		if seen[code] {
			t.Fatalf("the same code twice: %q", code)
		}
		seen[code] = true
	}
	for given, want := range map[string]string{
		"7KQ4-M2XD-9HPA":         "7KQ4M2XD9HPA",
		" 7kq4 m2xd 9hpa ":       "7KQ4M2XD9HPA",
		"7kq4m2xd9hpa":           "7KQ4M2XD9HPA",
		"OIL0-1111-2222":         "011011112222",
		"":                       "",
		strings.Repeat("A", 100): strings.Repeat("A", maxSetupCodeInput),
	} {
		if got := normalizeSetupCode(given); got != want {
			t.Errorf("%q: got %q, want %q", given, got, want)
		}
	}
}

// errorIndex is the number of a known error of the users in the address of a page.
func errorIndex(err error) int {
	for i, known := range userErrors {
		if known == err {
			return i
		}
	}
	return -1
}

// setupWeb is the app on its first start: no user yet, its log kept for the test.
func setupWeb(t *testing.T) (*Web, *observer.ObservedLogs) {
	t.Helper()
	web := usersWeb(t)
	core, logs := observer.New(zap.InfoLevel)
	web.sugarLogger = zap.New(core).Sugar()
	return web, logs
}

// Without the code nobody creates the first administrator, from no address; with it, written
// as people copy it, the administrator is created, and then the code is worth nothing.
func TestFirstAdministratorNeedsTheSetupCode(t *testing.T) {
	web, logs := setupWeb(t)
	code := web.auth.setup.code
	if code == "" {
		t.Fatal("no code while there is no user")
	}
	web.logSetupCode(true)
	if logs.FilterMessage("SETUP CODE  "+code).Len() != 1 {
		t.Fatalf("the code is not in the log: %v", logs.All())
	}

	// the computer itself (a tunnel arrives so), a private address, and a forwarded one
	for _, ip := range []string{"127.0.0.1", "192.168.1.10", "172.17.0.1"} {
		c := &client{t: t, handler: web.auth.middleware(web.router), ip: ip}
		for _, given := range []string{"", "AAAA-BBBB-CCCC"} {
			created := c.do("POST", "/users/create", url.Values{"name": {"mallory"}, "password": {"alpha-secret-11"}, "setup_code": {given}})
			if created.Code != http.StatusSeeOther || !strings.Contains(created.Header().Get("Location"), "error=") {
				t.Fatalf("%s, code %q: %d %q", ip, given, created.Code, created.Header().Get("Location"))
			}
		}
	}
	if web.auth.users.Count() != 0 || web.auth.Enabled() {
		t.Fatal("an administrator was created without the code")
	}

	c := &client{t: t, handler: web.auth.middleware(web.router)}
	written := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	created := c.do("POST", "/users/create", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}, "setup_code": {written}})
	if created.Code != http.StatusSeeOther || !strings.Contains(created.Header().Get("Location"), "done=enabled") {
		t.Fatalf("with the code: %d %q", created.Code, created.Header().Get("Location"))
	}
	if user, ok := web.auth.users.Get("alice"); !ok || !user.IsAdmin() {
		t.Fatal("the first administrator was not created")
	}

	// once used, the code is gone and not written again
	if web.auth.setup.code != "" {
		t.Fatal("the code is kept after the first administrator")
	}
	if err := web.auth.checkSetupCode(code); !errors.Is(err, ErrSetupCode) {
		t.Fatalf("the used code: %v", err)
	}
	before := logs.Len()
	web.logSetupCode(true)
	if logs.Len() != before {
		t.Fatal("the code is written in the log after the first administrator")
	}
	// the next users are created by the administrator, without a code
	if added := c.do("POST", "/users/create", url.Values{"name": {"bob"}, "password": {"bravo-secret-22"}, "role": {ROLE_VIEWER}}); !strings.Contains(added.Header().Get("Location"), "done=created") {
		t.Fatalf("a second user: %q", added.Header().Get("Location"))
	}
}

// Wrong codes block the code for everybody, as failed logins block an account: a client that
// changes its address, or makes up the one it forwards, is stopped too.
func TestWrongSetupCodesAreBlocked(t *testing.T) {
	web, logs := setupWeb(t)
	code := web.auth.setup.code
	for i := 0; i < maxLoginFailures; i++ {
		c := &client{t: t, handler: web.auth.middleware(web.router), ip: "10.0.0." + strconv.Itoa(i+1)}
		c.do("POST", "/users/create", url.Values{"name": {"mallory"}, "password": {"alpha-secret-11"}, "setup_code": {"WRNG-WRNG-WRNG"}}, "X-Forwarded-For", "192.168.50."+strconv.Itoa(i+1))
	}
	if err := web.auth.checkSetupCode(code); !errors.Is(err, ErrSetupCodeBlocked) {
		t.Fatalf("the right code while blocked: %v", err)
	}
	c := &client{t: t, handler: web.auth.middleware(web.router), ip: "192.168.1.99"}
	created := c.do("POST", "/users/create", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}, "setup_code": {code}})
	if web.auth.users.Count() != 0 || created.Header().Get("Location") != "/users.html?error="+strconv.Itoa(errorIndex(ErrSetupCodeBlocked)) {
		t.Fatalf("created while blocked: %q", created.Header().Get("Location"))
	}
	if logs.FilterMessage("Wrong setup code for the first administrator").Len() == 0 {
		t.Fatal("the wrong codes are not noted in the log")
	}
	// the block of the code is that of an account, never a whole day
	web.auth.limiter.mutex.Lock()
	until := web.auth.limiter.failures[setupCodeKey].blockUntil
	web.auth.limiter.mutex.Unlock()
	if wait := until.Sub(web.auth.limiter.failures[setupCodeKey].last); wait > maxAccountBlock {
		t.Fatalf("blocked for %v", wait)
	}
}

// Where users exist, or come from the environment, there is no code, and nothing changes.
func TestSetupCodeLeavesInstallationsWithUsersAlone(t *testing.T) {
	web := usersWeb(t)
	if err := web.auth.users.Add("alice", "alpha-secret-11", ROLE_ADMIN); err != nil {
		t.Fatal(err)
	}
	restarted, err := newAuth(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.setup.code != "" {
		t.Fatal("a code where users exist")
	}
	web.auth = restarted
	core, logs := observer.New(zap.InfoLevel)
	web.sugarLogger = zap.New(core).Sugar()
	web.logSetupCode(true)
	if logs.Len() != 0 {
		t.Fatalf("written in the log where users exist: %v", logs.All())
	}

	t.Setenv("SLM_AUTH_USERNAME", "admin")
	t.Setenv("SLM_AUTH_PASSWORD", "a-long-env-password")
	fromEnv, err := newAuth(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if fromEnv.setup.code != "" {
		t.Fatal("a code where the administrator comes from the environment")
	}
}

// The wizard checks the code with the administrator, before saving anything, and does not use
// it up: the administrator is created at the end.
func TestWizardChecksTheSetupCode(t *testing.T) {
	web, _ := setupWeb(t)
	web.HandleWizard()
	check := func(code string) WizardCheck {
		t.Helper()
		var result WizardCheck
		request := httptest.NewRequest("POST", "/wizard/admin", strings.NewReader(url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}, "setup_code": {code}}.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, request)
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, code := range []string{"", "WRNG-WRNG-WRNG"} {
		if got := check(code); got.Ok || got.Field != "setup_code" || got.Message == "" {
			t.Fatalf("code %q: %+v", code, got)
		}
	}
	if got := check(web.auth.setup.code); !got.Ok {
		t.Fatalf("the right code: %+v", got)
	}
	if web.auth.setup.code == "" || web.auth.users.Count() != 0 {
		t.Fatal("the check used the code up")
	}
}

// A backup restored while login is off creates the first users: it needs the code too. One
// without users does not, nor does a copy the app made itself.
func TestBackupWithUsersNeedsTheSetupCode(t *testing.T) {
	web, _ := setupWeb(t)
	web.HandleBackup()
	backup := zipFiles(t, map[string]string{
		settings.SETTINGS_FILENAME: "{}",
		USERS_FILENAME:             `[{"name":"mallory","role":"admin","password_hash":"x"}]`,
	})
	restore := func(code string) *httptest.ResponseRecorder {
		body := &bytes.Buffer{}
		form := multipart.NewWriter(body)
		part, _ := form.CreateFormFile("backup", "slm-backup.zip")
		part.Write(backup)
		form.WriteField("setup_code", code)
		form.Close()
		request := httptest.NewRequest("POST", "/backup/restore", body)
		request.Header.Set("Content-Type", form.FormDataContentType())
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, request)
		return recorder
	}
	if r := restore(""); r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "This backup has users") {
		t.Fatalf("without the code: %d %s", r.Code, r.Body.String())
	}
	if r := restore("WRNG-WRNG-WRNG"); r.Code != http.StatusBadRequest {
		t.Fatalf("a wrong code: %d", r.Code)
	}
	if web.auth.users.Count() != 0 {
		t.Fatal("users restored without the code")
	}
	if _, err := os.Stat(filepath.Join(web.dataFolder, USERS_FILENAME)); err == nil {
		t.Fatal("users.json written without the code")
	}

	// the page then rescans the library, which this test web cannot: the same steps directly
	if err := web.restoreBackup(backup, web.backupSetupCode(web.auth.setup.code)); err != nil {
		t.Fatal(err)
	}
	if !web.auth.Enabled() || web.auth.setup.code != "" {
		t.Fatal("the users of the backup did not end the code")
	}

	// no users in the backup, or a copy of the app: no code
	other, _ := setupWeb(t)
	if err := other.restoreBackup(zipFiles(t, map[string]string{settings.SETTINGS_FILENAME: "{}", USERS_FILENAME: "[]"}), other.backupSetupCode("")); err != nil {
		t.Fatalf("a backup without users: %v", err)
	}
	if err := other.restoreBackup(backup, nil); err != nil || !other.auth.Enabled() {
		t.Fatalf("a copy of the app: %v", err)
	}
}

// The code goes to the log only: no page, answer, header or activity has it.
func TestSetupCodeIsNeverSent(t *testing.T) {
	web, logs := setupWeb(t)
	web.HandleWizard()
	web.HandleBackup()
	web.HandleActivity()
	code := web.auth.setup.code
	plain := normalizeSetupCode(code)
	c := &client{t: t, handler: web.auth.middleware(web.withActivityLog(web.router))}
	seen := []*httptest.ResponseRecorder{
		c.do("GET", "/users.html", nil),
		c.do("GET", "/users.html?error="+strconv.Itoa(errorIndex(ErrSetupCode)), nil),
		c.do("GET", "/wizard", nil),
		c.do("POST", "/wizard/admin", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}, "setup_code": {code}}),
		c.do("POST", "/wizard/admin", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}, "setup_code": {"WRNG"}}),
		c.do("POST", "/users/create", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}, "setup_code": {"WRNG"}}),
		c.do("POST", "/users/create", url.Values{"name": {"alice"}, "password": {"alpha-secret-11"}, "setup_code": {code}}),
		c.do("GET", "/users.html?done=enabled", nil),
		c.do("GET", "/activity.html", nil),
	}
	for i, response := range seen {
		var headers strings.Builder
		response.Header().Write(&headers)
		for _, text := range []string{response.Body.String(), headers.String()} {
			if strings.Contains(text, code) || strings.Contains(strings.ToUpper(text), plain) {
				t.Fatalf("answer %d has the code", i)
			}
		}
	}
	for _, entry := range web.activities().list() {
		if strings.Contains(entry.Detail+entry.User+entry.Action, code) {
			t.Fatalf("the activity has the code: %+v", entry)
		}
	}
	// the log has it, and says nothing of the codes that were tried
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "WRNG") {
			t.Fatalf("a code tried is in the log: %q", entry.Message)
		}
	}
}
