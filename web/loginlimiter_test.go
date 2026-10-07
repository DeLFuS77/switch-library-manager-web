package web

import (
	"testing"
	"time"
)

func TestLimiterBlocksKeyAfterFailures(t *testing.T) {
	l := newLoginLimiter()
	key := "ip:203.0.113.5"
	for i := 0; i < maxLoginFailures-1; i++ {
		l.fail(key)
	}
	if retry, _ := l.check(key); retry != 0 {
		t.Fatalf("not blocked yet: %v", retry)
	}
	l.fail(key)
	if retry, _ := l.check(key); retry <= 0 || retry > loginWindow {
		t.Fatalf("blocked for about the window: %v", retry)
	}
	// a success clears the key
	l.succeed(key)
	if retry, _ := l.check(key); retry != 0 {
		t.Fatalf("still blocked after a success: %v", retry)
	}
}

func TestLimiterBlockGrowsAndIsCapped(t *testing.T) {
	l := newLoginLimiter()
	key := "ip:203.0.113.6"
	for i := 0; i < maxLoginFailures; i++ {
		l.fail(key)
	}
	first, _ := l.check(key)
	l.fail(key)
	second, _ := l.check(key)
	if second <= first {
		t.Fatalf("the block must grow: %v then %v", first, second)
	}
	for i := 0; i < 40; i++ {
		l.fail(key)
	}
	if retry, _ := l.check(key); retry > maxLoginBlock+time.Minute {
		t.Fatalf("the block must be capped at %v: %v", maxLoginBlock, retry)
	}
}

func TestLimiterAccountBlockIsCappedLowerThanAddress(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < maxLoginFailures+30; i++ {
		l.fail("ip:203.0.113.7", "user:carol")
	}
	ip, _ := l.check("ip:203.0.113.7")
	account, _ := l.check("user:carol")
	if account > maxAccountBlock+time.Minute {
		t.Fatalf("the account block must be capped at %v: %v", maxAccountBlock, account)
	}
	if ip <= account {
		t.Fatalf("an address is blocked longer than an account: ip %v, account %v", ip, account)
	}
}

func TestLimiterAddsAGrowingDelay(t *testing.T) {
	l := newLoginLimiter()
	key := "ip:203.0.113.8"
	for i := 0; i < tarpitAfter; i++ {
		l.fail(key)
	}
	_, delay := l.check(key)
	if delay <= 0 || delay > maxTarpit {
		t.Fatalf("a delay after a few failures, up to the cap: %v", delay)
	}
	for i := 0; i < 20; i++ {
		l.fail(key)
	}
	// the delay only matters before the block; it never exceeds the cap
	if _, d := l.check(key); d > maxTarpit {
		t.Fatalf("the delay is capped at %v: %v", maxTarpit, d)
	}
}

func TestLimiterGlobalFloodDelaysEveryAttempt(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < globalFailureCap; i++ {
		l.fail("ip:198.51.100." + string(rune('0'+i%10)))
	}
	// a fresh key, never failed, still gets the flood delay
	_, delay := l.check("ip:203.0.113.9")
	if delay < globalFloodDelay {
		t.Fatalf("a flood from everywhere delays every attempt: %v", delay)
	}
}

func TestLimiterOneAddressDoesNotBlockAnotherAccount(t *testing.T) {
	l := newLoginLimiter()
	// one address tries many accounts: the address is blocked, the accounts are not
	for i := 0; i < maxLoginFailures; i++ {
		l.fail("ip:203.0.113.10", "user:name"+string(rune('a'+i)))
	}
	if retry, _ := l.check("ip:203.0.113.10"); retry <= 0 {
		t.Fatal("the address must be blocked")
	}
	if retry, _ := l.check("user:alice"); retry != 0 {
		t.Fatalf("an account tried once is not blocked: %v", retry)
	}
}

func TestPasswordPolicy(t *testing.T) {
	cases := map[string]error{
		"a-good-long-pass":  nil,
		"short9":            ErrPasswordLength,
		"password1":         ErrPasswordLength, // 9 chars
		"passw0rd1234":      nil,
		"nintendoswitch":    ErrPasswordCommon,
		"NintendoSwitch":    ErrPasswordCommon, // case-insensitive
		"administrator":     ErrPasswordCommon,
		"Alice-the-admin-1": nil,
	}
	for password, want := range cases {
		if got := checkPassword("alice", password); got != want {
			t.Errorf("checkPassword(%q) = %v, want %v", password, got, want)
		}
	}
	if checkPassword("alice-longname", "alice-longname") != ErrPasswordName {
		t.Error("the password must not be the user name")
	}
}
