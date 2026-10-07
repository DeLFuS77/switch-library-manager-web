package web

import (
	"errors"
	"strings"
)

// Passwords are checked when they are set, not when they are used, so existing accounts keep
// working. A short or very common password is refused: bcrypt and the login limiter handle the
// rest, so this is a guard against the obvious, not a full dictionary.

const (
	minPasswordLength = 10
	// bcrypt only uses the first 72 bytes
	maxPasswordLength = 72
)

var (
	ErrPasswordLength = errors.New("The password must have between 10 and 72 characters.")
	ErrPasswordCommon = errors.New("This password is too common or too easy to guess. Choose another one.")
	ErrPasswordName   = errors.New("The password must not be the user name.")
)

// commonPasswords holds the passwords guessed first in an attack, lower case. It is a short
// list of the most used passwords and of words tied to this app, not a full dictionary.
var commonPasswords = buildCommonSet(`
123456 123456789 12345678 1234567890 1234567 password password1 password123 qwerty qwerty123
qwertyuiop 111111 123123 abc123 1234 12345 000000 iloveyou admin admin123 administrator welcome
welcome1 welcome123 letmein letmein1 monkey dragon master sunshine princess football baseball
superman batman trustno1 passw0rd p@ssw0rd p@ssword qazwsx zaq12wsx 1q2w3e4r 1q2w3e4r5t
1qaz2wsx qweasdzxc asdfghjkl zxcvbnm aaaaaa abcdef abcabc changeme default root toor user guest
test test123 login654321 samsung google michael jordan hunter harley ranger shadow tigger
charlie robert thomas hockey ginger daniel starwars computer whatever pokemon nintendo switch
nintendoswitch mariokart zelda pikachu homebrew homebrew1 lockpick switchroot atmosphere
`)

func buildCommonSet(words string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, word := range strings.Fields(words) {
		set[word] = struct{}{}
	}
	return set
}

// checkPassword reports why a password cannot be used, or nil. name is "" when there is none.
func checkPassword(name, password string) error {
	if len(password) < minPasswordLength || len(password) > maxPasswordLength {
		return ErrPasswordLength
	}
	lower := strings.ToLower(strings.TrimSpace(password))
	if name != "" && lower == strings.ToLower(strings.TrimSpace(name)) {
		return ErrPasswordName
	}
	if _, common := commonPasswords[lower]; common {
		return ErrPasswordCommon
	}
	return nil
}
