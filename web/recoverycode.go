package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
)

// A user who forgot their password asks for a recovery code on the login page. The code goes
// where only the owner of the server looks: the log of the app (the log of the container, or the
// window of the program) and a file in the data folder. As the setup code, it is never sent in a
// page or an answer. With it the user sets a new password, as an administrator does in Users:
// their sessions end and the activity notes it. The page answers the same whether the user
// exists or not, so it tells nobody which users there are.

const (
	// how long a recovery code works
	recoveryCodeLifetime = 15 * time.Minute
	// a new code is made at most this often: asking again sooner keeps the one there is, so
	// nobody can fill the log, or keep replacing the code of someone who is copying it
	recoveryCodeAskEvery = time.Minute
	// the key of the login limiter for the code, shared by every address, as the setup code's
	recoveryCodeKey = "recovery-code"
	// the file in the data folder that holds the code while it works
	RECOVERY_CODE_FILENAME = "recovery-code.txt"
)

var (
	ErrRecoveryCode        = errors.New("The code is not right or has expired. Ask for another one and copy it from the log of the app.")
	ErrRecoveryCodeBlocked = errors.New("Too many wrong codes. Wait a few minutes and ask for another one.")
)

// recoveryCode is the one code there is, for one user, until it expires or is used.
type recoveryCode struct {
	mutex   sync.Mutex
	code    string
	user    string
	made    time.Time
	expires time.Time
}

// newRecoveryCode makes a code for a user stored in users.json, and returns it with the name
// as it is stored. It returns "" when no code is to be written: the user does not exist, comes
// from the environment, or a code was made less than recoveryCodeAskEvery ago.
func (a *Auth) newRecoveryCode(name string, now time.Time) (code string, user string, err error) {
	stored, ok := a.users.Get(strings.TrimSpace(name))
	if !ok || !a.Enabled() {
		return "", "", nil
	}
	a.recovery.mutex.Lock()
	defer a.recovery.mutex.Unlock()
	if a.recovery.code != "" && now.Sub(a.recovery.made) < recoveryCodeAskEvery && now.Before(a.recovery.expires) {
		return "", "", nil
	}
	code, err = newSetupCode()
	if err != nil {
		return "", "", err
	}
	a.recovery.code, a.recovery.user = code, stored.Name
	a.recovery.made, a.recovery.expires = now, now.Add(recoveryCodeLifetime)
	return code, stored.Name, nil
}

// takeRecoveryCode checks a code for a user, limited as the setup code is: after a few wrong
// codes from anywhere, nobody can try for a while. A right code is used up at once, so two
// requests with it cannot both change the password.
func (a *Auth) takeRecoveryCode(name, given string, now time.Time) error {
	given = normalizeSetupCode(given)
	retryAfter, delay := a.limiter.check(recoveryCodeKey)
	if retryAfter > 0 {
		return ErrRecoveryCodeBlocked
	}
	if delay > 0 {
		sleepFor(delay)
	}
	a.recovery.mutex.Lock()
	defer a.recovery.mutex.Unlock()
	expected := normalizeSetupCode(a.recovery.code)
	if expected != "" && !now.Before(a.recovery.expires) {
		// expired: it is gone, as if it never was
		a.recovery.code, a.recovery.user = "", ""
		expected = ""
	}
	// the hashes have the same length, so the time of the comparison tells nothing of the code
	givenHash, expectedHash := sha256.Sum256([]byte(given)), sha256.Sum256([]byte(expected))
	sameCode := subtle.ConstantTimeCompare(givenHash[:], expectedHash[:]) == 1
	if expected == "" || given == "" || !sameCode || !strings.EqualFold(strings.TrimSpace(name), a.recovery.user) {
		a.limiter.fail(recoveryCodeKey)
		return ErrRecoveryCode
	}
	a.limiter.succeed(recoveryCodeKey)
	a.recovery.code, a.recovery.user = "", ""
	return nil
}

// endRecoveryCode forgets a code that expired, when it is still the one there is.
func (a *Auth) endRecoveryCode(code string) bool {
	a.recovery.mutex.Lock()
	defer a.recovery.mutex.Unlock()
	if a.recovery.code == "" || a.recovery.code != code {
		return false
	}
	a.recovery.code, a.recovery.user = "", ""
	return true
}

// recoveryCodePath is the file that holds the code while it works.
func (web *Web) recoveryCodePath() string {
	return filepath.Join(web.dataFolder, RECOVERY_CODE_FILENAME)
}

// removeRecoveryCodeFile deletes the file of the code: used, expired, or left by a start before.
func (web *Web) removeRecoveryCodeFile() {
	if err := os.Remove(web.recoveryCodePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		web.sugarLogger.Warnf("Could not delete %s: %v", RECOVERY_CODE_FILENAME, err)
	}
}

// writeRecoveryCode writes the code in the log, in a block easy to find in the log of a container
// (plain ASCII, as the console of Windows shows it too), and in the file of the data folder.
func (web *Web) writeRecoveryCode(code, user string, expires time.Time) {
	lines := []string{
		"======================================================================",
		"RECOVERY CODE  " + code + "   for the user \"" + user + "\"",
		"Someone asked to reset the password of this user on the login page.",
		"Write this code there with the new password. It works once, for 15",
		"minutes. It is also in " + RECOVERY_CODE_FILENAME + " in the data folder.",
		"If it was not you, ignore it: the password stays as it is.",
		"======================================================================",
	}
	for _, line := range lines {
		web.sugarLogger.Info(line)
	}
	text := "RECOVERY CODE  " + code + "\n" +
		"User: " + user + "\n" +
		"Works until: " + expires.Format("2006-01-02 15:04 MST") + "\n\n" +
		"Write this code on the page \"Forgot your password?\" of the login page, with the new\n" +
		"password. It works once. This file is deleted when the code is used.\n"
	// only the owner of the server reads it
	if err := os.WriteFile(web.recoveryCodePath(), []byte(text), 0o600); err != nil {
		web.sugarLogger.Warnf("Could not write %s: %v", RECOVERY_CODE_FILENAME, err)
	}
}

// RecoverPageData is the page of a forgotten password: the name, then the code and the password.
type RecoverPageData struct {
	Lang  string
	Name  string
	Error string
	// the code was asked: the page asks for it, with the new password
	Asked bool
	// where the log of the app is seen: the container's, or the window of the program
	InContainer bool
}

func (web *Web) handleRecovery() {
	page := web.mustParseTemplates(web.embedFS, "resources/recover.html")
	web.router.HandleFunc("/recover.html", web.recoverPage(page)).Methods("GET")
	web.router.HandleFunc("/recover/ask", web.askRecoveryCode(page)).Methods("POST")
	web.router.HandleFunc("/recover/reset", web.resetWithRecoveryCode(page)).Methods("POST")
}

// recoverPage asks for the name of the user; without login there is no password to recover.
func (web *Web) recoverPage(page templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !web.auth.Enabled() {
			redirectLocal(w, r, "/index.html")
			return
		}
		web.renderRecover(w, r, page, RecoverPageData{})
	}
}

// askRecoveryCode writes a code for the user in the log and the data folder, and asks for it.
func (web *Web) askRecoveryCode(page templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !web.auth.Enabled() {
			redirectLocal(w, r, "/index.html")
			return
		}
		data := RecoverPageData{Name: strings.TrimSpace(r.FormValue("name"))}
		if data.Name == "" {
			web.renderRecover(w, r, page, data)
			return
		}
		code, user, err := web.auth.newRecoveryCode(data.Name, time.Now())
		if err != nil {
			web.sugarLogger.Errorf("Could not make a recovery code: %v", err)
			http.Error(w, "could not make a recovery code", http.StatusInternalServerError)
			return
		}
		if code != "" {
			web.writeRecoveryCode(code, user, time.Now().Add(recoveryCodeLifetime))
			// not used in time: the code ends, and its file goes, unless a newer one took its place
			time.AfterFunc(recoveryCodeLifetime, func() {
				if web.auth.endRecoveryCode(code) {
					web.removeRecoveryCodeFile()
				}
			})
		}
		// the same page whether the user exists or not
		data.Asked = true
		web.renderRecover(w, r, page, data)
	}
}

// resetWithRecoveryCode sets the new password with the code, as an administrator does in Users:
// the sessions of the user end, and the activity notes it.
func (web *Web) resetWithRecoveryCode(page templateSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !web.auth.Enabled() {
			redirectLocal(w, r, "/index.html")
			return
		}
		lang := web.requestLanguage(r)
		data := RecoverPageData{Name: strings.TrimSpace(r.FormValue("name")), Asked: true}
		password := r.FormValue("password")
		// a password that cannot be set does not use up the code
		if err := checkPassword(data.Name, password); err != nil {
			data.Error = i18n.Translate(lang, err.Error())
			w.WriteHeader(http.StatusBadRequest)
			web.renderRecover(w, r, page, data)
			return
		}
		if err := web.auth.takeRecoveryCode(data.Name, r.FormValue("code"), time.Now()); err != nil {
			web.sugarLogger.Warn("Wrong recovery code")
			data.Error = i18n.Translate(lang, err.Error())
			status := http.StatusUnauthorized
			if errors.Is(err, ErrRecoveryCodeBlocked) {
				status = http.StatusTooManyRequests
			}
			w.WriteHeader(status)
			web.renderRecover(w, r, page, data)
			return
		}
		web.removeRecoveryCodeFile()
		// the same path as an administrator in Users: a new hash ends the sessions of the user
		if err := web.auth.users.SetPassword(data.Name, password); err != nil {
			data.Error = i18n.Translate(lang, err.Error())
			w.WriteHeader(http.StatusBadRequest)
			web.renderRecover(w, r, page, data)
			return
		}
		web.auth.forget()
		user := data.Name
		if stored, ok := web.auth.users.Get(data.Name); ok {
			user = stored.Name
		}
		web.sugarLogger.Infof("Password of %q changed with a recovery code", user)
		web.activities().Add(activity.Entry{Time: time.Now(), User: user, Action: activity.ACTION_USER_PASS, Detail: recoveryActivityDetail, IP: clientIp(r)})
		http.Redirect(w, r, "/login.html?done=recovered", http.StatusSeeOther)
	}
}

// recoveryActivityDetail tells in Activity a password changed with a recovery code.
const recoveryActivityDetail = "with a recovery code"

// renderRecover writes the page of a forgotten password.
func (web *Web) renderRecover(w http.ResponseWriter, r *http.Request, templates templateSet, data RecoverPageData) {
	data.Lang = web.requestLanguage(r)
	data.InContainer = runningInContainer()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := templates.execute(w, data.Lang, data); err != nil {
		web.sugarLogger.Errorf("executing template failed: %v", err)
	}
}
