package web

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncryptedBackup(t *testing.T) {
	data := []byte("PK the zip of a backup")
	if _, err := encryptBackup(data, "short"); !errors.Is(err, ErrBackupPasswordShort) {
		t.Fatalf("short password: %v", err)
	}
	sealed, err := encryptBackup(data, "a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	if !isEncryptedBackup(sealed) || bytes.Contains(sealed, data) {
		t.Fatal("not sealed")
	}
	if _, err := decryptBackup(sealed, ""); !errors.Is(err, ErrBackupPasswordNeeded) {
		t.Fatalf("no password: %v", err)
	}
	if _, err := decryptBackup(sealed, "another long password"); !errors.Is(err, ErrBackupPasswordWrong) {
		t.Fatalf("wrong password: %v", err)
	}
	// a changed byte of the header or of the data is refused
	for _, at := range []int{len(encryptedBackupMagic) + 1, len(sealed) - 1} {
		changed := append([]byte{}, sealed...)
		changed[at] ^= 1
		if _, err := decryptBackup(changed, "a long enough password"); !errors.Is(err, ErrBackupPasswordWrong) {
			t.Fatalf("changed byte %d: %v", at, err)
		}
	}
	plain, err := decryptBackup(sealed, "a long enough password")
	if err != nil || !bytes.Equal(plain, data) {
		t.Fatalf("opened: %q %v", plain, err)
	}
	// a backup without a password is read as it is
	if plain, err := decryptBackup(data, "whatever"); err != nil || !bytes.Equal(plain, data) {
		t.Fatalf("plain backup: %v", err)
	}
	if _, err := decryptBackup(encryptedBackupMagic, "a long enough password"); !errors.Is(err, ErrBackupInvalid) {
		t.Fatalf("cut file: %v", err)
	}
}
