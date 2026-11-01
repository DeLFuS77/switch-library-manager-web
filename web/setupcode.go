package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The first administrator is created with a setup code. While there is no user the app makes a
// new code each time it starts and writes it in its log, and asks for it wherever the first users
// come from: Users, the setup wizard, the API and a backup restored with users in it. Being on
// the local network is not enough: behind Docker Desktop, Podman without root or a tunnel, a
// client from the internet arrives with a private address, or with the one of this computer.
// The code is never sent in a page, an answer or the activity log.

const (
	// Crockford's base 32: digits and capitals without I, L, O and U, so the code is read and
	// typed without doubts; 12 of them are 60 bits
	setupCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	setupCodeLength   = 12
	// the key of the login limiter for the code, shared by every address: a botnet, or a client
	// that makes up the address it forwards, is stopped as a single address is
	setupCodeKey = "setup-code"
	// the code is written again when a page asks for it, at most this often, so it is found at
	// the end of a long log
	setupCodeRemindEvery = time.Minute
	// the longest code read; anything longer is wrong
	maxSetupCodeInput = 64
)

var (
	ErrSetupCodeMissing = errors.New("Write the setup code: it is in the log of the app, where it says SETUP CODE.")
	ErrSetupCode        = errors.New("The setup code is not right. Copy it from the log of the app, where it says SETUP CODE.")
	ErrSetupCodeBlocked = errors.New("Too many wrong setup codes. Wait a few minutes, or start the app again for a new code.")
	ErrBackupSetupCode  = errors.New("This backup has users: to restore them, write the setup code. It is in the log of the app, where it says SETUP CODE.")
)

// setupCode is the code of the first administrator: "" when login is on.
type setupCode struct {
	mutex  sync.Mutex
	code   string
	logged time.Time
}

// newSetupCode makes a code of setupCodeLength characters, in groups of four.
func newSetupCode() (string, error) {
	random := make([]byte, setupCodeLength)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	code := make([]byte, 0, setupCodeLength+setupCodeLength/4)
	for i, b := range random {
		if i > 0 && i%4 == 0 {
			code = append(code, '-')
		}
		// 32 letters: the last five bits of a random byte pick one, each as likely as the others
		code = append(code, setupCodeAlphabet[b&31])
	}
	return string(code), nil
}

// normalizeSetupCode reads a code as it was written: small letters, spaces and dashes are
// allowed, and O, I and L are taken for 0, 1 and 1, as Crockford's base 32 does.
func normalizeSetupCode(code string) string {
	if len(code) > maxSetupCodeInput {
		code = code[:maxSetupCodeInput]
	}
	var normalized strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch {
		case unicode.IsSpace(r) || r == '-':
		case r == 'O':
			normalized.WriteByte('0')
		case r == 'I' || r == 'L':
			normalized.WriteByte('1')
		default:
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

// startSetupCode makes the code of this start when login is off.
func (a *Auth) startSetupCode() error {
	if a.Enabled() {
		return nil
	}
	code, err := newSetupCode()
	if err != nil {
		return err
	}
	a.setup.mutex.Lock()
	a.setup.code = code
	a.setup.mutex.Unlock()
	return nil
}

// checkSetupCode checks a setup code against the one of this start, limited as the logins are:
// after a few wrong codes from anywhere, nobody can try for a while. An empty code is not
// counted, so a client that does not know about the code is just told.
func (a *Auth) checkSetupCode(given string) error {
	given = normalizeSetupCode(given)
	if given == "" {
		return ErrSetupCodeMissing
	}
	retryAfter, delay := a.limiter.check(setupCodeKey)
	if retryAfter > 0 {
		return ErrSetupCodeBlocked
	}
	if delay > 0 {
		sleepFor(delay)
	}
	a.setup.mutex.Lock()
	expected := normalizeSetupCode(a.setup.code)
	a.setup.mutex.Unlock()
	// the hashes have the same length, so the time of the comparison tells nothing of the code
	givenHash, expectedHash := sha256.Sum256([]byte(given)), sha256.Sum256([]byte(expected))
	if expected == "" || a.Enabled() || subtle.ConstantTimeCompare(givenHash[:], expectedHash[:]) != 1 {
		a.limiter.fail(setupCodeKey)
		return ErrSetupCode
	}
	a.limiter.succeed(setupCodeKey)
	return nil
}

// endSetupCode forgets the code once login is on: it is never asked again.
func (a *Auth) endSetupCode() {
	if !a.Enabled() {
		return
	}
	a.setup.mutex.Lock()
	a.setup.code = ""
	a.setup.mutex.Unlock()
}

// setupCodeToLog returns the code when it is to be written in the log: at the start, and again
// when a page asks for it, at most once every setupCodeRemindEvery.
func (a *Auth) setupCodeToLog(now time.Time, always bool) string {
	if a.Enabled() {
		return ""
	}
	a.setup.mutex.Lock()
	defer a.setup.mutex.Unlock()
	if a.setup.code == "" || (!always && now.Sub(a.setup.logged) < setupCodeRemindEvery) {
		return ""
	}
	a.setup.logged = now
	return a.setup.code
}

// logSetupCode writes the setup code in the log, in a block easy to find in the log of a
// container: plain ASCII, as the console of Windows shows it too.
func (web *Web) logSetupCode(always bool) {
	if web.auth == nil {
		return
	}
	code := web.auth.setupCodeToLog(time.Now(), always)
	if code == "" {
		return
	}
	for _, line := range []string{
		"======================================================================",
		"SETUP CODE  " + code,
		"There is no administrator yet. This code is asked when the first one",
		"is created, in Users or in the setup wizard. It works once, and it",
		"changes every time the app starts.",
		"======================================================================",
	} {
		web.sugarLogger.Info(line)
	}
}

// setupCodeFailed notes a setup code that was missing or wrong, never the code itself, and
// writes the code in the log again so it is found at its end.
func (web *Web) setupCodeFailed(err error) {
	if errors.Is(err, ErrSetupCode) || errors.Is(err, ErrSetupCodeBlocked) {
		web.sugarLogger.Warn("Wrong setup code for the first administrator")
	}
	web.logSetupCode(false)
}
