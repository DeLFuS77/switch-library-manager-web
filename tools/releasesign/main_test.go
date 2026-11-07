package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/selfupdate"
)

// The checksums are signed for their version with the key of the secret when it is one of the
// keys of the app; without the key, or with another, nothing is signed and the release goes on.
func TestSign(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	var id [8]byte
	rand.Read(id[:])
	key := selfupdate.PublicKey{ID: id, Key: public}
	otherPublic, _, _ := ed25519.GenerateKey(rand.Reader)
	other := selfupdate.PublicKey{ID: id, Key: otherPublic}
	secret := selfupdate.PrivateKeyText(private, id)
	sums := filepath.Join(t.TempDir(), "SHA256SUMS.txt")
	os.WriteFile(sums, []byte("0123  switch-library-manager-web-windows-x64.zip\n"), 0o644)

	for what, c := range map[string]struct {
		secret string
		keys   []selfupdate.PublicKey
		signed bool
	}{
		"the key of the app":          {secret, []selfupdate.PublicKey{key}, true},
		"no key":                      {"", []selfupdate.PublicKey{key}, false},
		"not a key":                   {"hello", []selfupdate.PublicKey{key}, false},
		"a key the app does not have": {secret, []selfupdate.PublicKey{other}, false},
		"an app without keys":         {secret, nil, false},
	} {
		os.Remove(sums + ".minisig")
		note := sign(c.secret, "2.0.3", sums, c.keys)
		signature, err := os.ReadFile(sums + ".minisig")
		if c.signed != (err == nil) || c.signed != (note == "") {
			t.Errorf("%s: note %q, signature %v", what, note, err == nil)
			continue
		}
		if strings.Contains(note, secret) {
			t.Errorf("%s: the note tells the key", what)
		}
		if c.signed {
			data, _ := os.ReadFile(sums)
			if comment, err := selfupdate.Verify(data, signature, c.keys); err != nil || comment != "Switch Library Manager Web 2.0.3" {
				t.Errorf("%s: %q %v", what, comment, err)
			}
		}
	}
}
