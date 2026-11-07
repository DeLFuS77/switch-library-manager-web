package selfupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func testKey(t *testing.T) (ed25519.PrivateKey, PublicKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := PublicKey{Key: public}
	if _, err := rand.Read(key.ID[:]); err != nil {
		t.Fatal(err)
	}
	return private, key
}

// A signature passes only made by a key of the app over these very checksums, with its trusted
// comment as it was signed; a missing one, another key, a checksum changed or a comment changed
// do not.
func TestVerify(t *testing.T) {
	private, public := testKey(t)
	_, other := testKey(t)
	data := []byte("abc  switch-library-manager-web-windows-x64.zip\n")
	signature := Sign(private, public.ID, data, Comment("2.0.3"))
	if comment, err := Verify(data, signature, []PublicKey{other, public}); err != nil || comment != "Switch Library Manager Web 2.0.3" {
		t.Fatalf("good signature: %q %v", comment, err)
	}
	for what, check := range map[string]func() error{
		"tampered checksums": func() error {
			_, err := Verify([]byte("abd  switch-library-manager-web-windows-x64.zip\n"), signature, []PublicKey{public})
			return err
		},
		"another key":       func() error { _, err := Verify(data, signature, []PublicKey{other}); return err },
		"no key":            func() error { _, err := Verify(data, signature, nil); return err },
		"missing signature": func() error { _, err := Verify(data, nil, []PublicKey{public}); return err },
		"comment changed": func() error {
			_, err := Verify(data, bytes.Replace(signature, []byte("2.0.3"), []byte("2.0.4"), 1), []PublicKey{public})
			return err
		},
		"not a signature":   func() error { _, err := Verify(data, []byte("hello"), []PublicKey{public}); return err },
		"signature cut off": func() error { _, err := Verify(data, signature[:60], []PublicKey{public}); return err },
	} {
		if err := check(); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: %v", what, err)
		}
	}
	// the key as minisign writes it reads back
	keys, err := ParsePublicKeys("# keys\n" + public.Text())
	if err != nil || len(keys) != 1 || keys[0].ID != public.ID || !keys[0].Key.Equal(public.Key) {
		t.Fatalf("public key read back: %v %v", keys, err)
	}
	// and the private one of the secret
	again, id, err := ParsePrivateKey(PrivateKeyText(private, public.ID))
	if err != nil || id != public.ID || !again.Equal(private) {
		t.Fatalf("private key read back: %v", err)
	}
	if _, _, err := ParsePrivateKey("not a key"); err == nil {
		t.Fatal("a private key that is not one is read")
	}
}

// A signature of minisign -l (legacy: of the file itself, "Ed") passes too, as minisign -V takes it.
func TestVerifyLegacy(t *testing.T) {
	private, public := testKey(t)
	data := []byte("abc  a.tar.gz\n")
	file := ed25519.Sign(private, data)
	comment := "timestamp:1 file:SHA256SUMS.txt"
	global := ed25519.Sign(private, append(append([]byte{}, file...), comment...))
	head := append(append([]byte("Ed"), public.ID[:]...), file...)
	signature := []byte("untrusted comment: signature from minisign secret key\n" + base64.StdEncoding.EncodeToString(head) + "\ntrusted comment: " + comment + "\n" + base64.StdEncoding.EncodeToString(global) + "\n")
	if got, err := Verify(data, signature, []PublicKey{public}); err != nil || got != comment {
		t.Fatalf("legacy signature: %q %v", got, err)
	}
}

// The keys the app is built with are a file it reads: none until tools/releasekey writes one.
func TestKeysFileReads(t *testing.T) {
	if _, err := ParsePublicKeys(releaseKeys); err != nil {
		t.Fatalf("release.pub: %v", err)
	}
	if Comment("v2.0.3") != Comment("2.0.3") {
		t.Fatal("the comment of a tag with v")
	}
}
